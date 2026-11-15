package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"rehearsal/internal/changes"
	"rehearsal/internal/events"
	"rehearsal/internal/review"
	"rehearsal/internal/sandbox"
)

// sessionChanges records the working tree before the agent starts, and
// works out what changed once it has exited.
type sessionChanges struct {
	workDir, sessionDir string
	inRepo              bool
	from                string
	// why the tree could not be recorded, when it could not
	problem string
	// applied to the diff before it is kept: a secret the agent put in a
	// tracked file is replaced by its name. Nil keeps the diff as it is.
	mask func(diff string) string
}

func startChanges(workDir, sessionDir string) *sessionChanges {
	c := &sessionChanges{workDir: workDir, sessionDir: sessionDir, inRepo: true}
	tree, err := changes.Snapshot(workDir, sessionDir)
	switch {
	case errors.Is(err, changes.ErrNotARepo):
		c.inRepo = false
	case err != nil:
		c.problem = err.Error()
	default:
		c.from = tree
	}
	return c
}

// finish writes the session's diff and says in the log what was captured.
// With a sandbox the working tree has not changed: what the agent did is
// in the sandbox's layer, and that is put over the first tree.
func (c *sessionChanges) finish(log *events.Log, box *sandbox.Box) error {
	ev := events.ChangesCaptured{InRepo: c.inRepo, Problem: c.problem, From: c.from}
	if c.inRepo && c.problem == "" {
		if err := c.capture(&ev, box); err != nil {
			// a session is not lost for want of its diff
			ev.Problem = err.Error()
		}
	}
	_, err := log.Append(events.TypeChangesCaptured, ev)
	return err
}

// review is the session's changes sorted for reading, as the log and the
// session's folder have them now.
func (c *sessionChanges) review(log *events.Log) (changes.Review, error) {
	l, err := current(log)
	if err != nil {
		return changes.Review{}, err
	}
	r, err := changes.Load(c.sessionDir, l.Changes)
	r.Annotate(l.ChangeNotes, l.ChangesReading)
	return r, err
}

// What the local model may spend on a session's changes.
const (
	maxPartsRead  = 40
	readingBudget = 90 * time.Second
)

// changeReader is the local model, as far as reading a change goes.
type changeReader interface {
	Change(ctx context.Context, path, reason, diff string) (review.ChangeNote, error)
}

// read has the local model say what each part to read or skim does, the
// parts most in need of reading first. It stops at maxPartsRead parts or
// readingBudget, whichever comes first, and says in the log what it left.
// What it writes can add to what the rules decided and never take away:
// a part can go from skim to read, and nothing else changes.
func (c *sessionChanges) read(ctx context.Context, log *events.Log, model changeReader, now func() time.Time) error {
	r, err := c.review(log)
	if err != nil {
		return err
	}
	total := len(changes.ToRead(r.Parts, len(r.Parts)))
	parts := changes.ToRead(r.Parts, maxPartsRead)
	if len(parts) == 0 {
		return nil
	}
	if _, err := log.Append(events.TypeChangesReading, events.ChangesReading{Started: true, Parts: len(parts)}); err != nil {
		return err
	}
	deadline := now().Add(readingBudget)
	ctx, cancel := context.WithTimeout(ctx, readingBudget)
	defer cancel()
	done, failed, why := 0, 0, ""
	for _, p := range parts {
		if !now().Before(deadline) || ctx.Err() != nil {
			why = fmt.Sprintf("the %d seconds allowed for it ran out", int(readingBudget.Seconds()))
			break
		}
		note, err := model.Change(ctx, p.Path, p.Reason, p.Text)
		if err != nil {
			// one unusable answer costs one sentence; two in a row and
			// the model is taken to have gone
			if failed++; failed >= 2 {
				why = "the model stopped answering"
				break
			}
			continue
		}
		failed = 0
		if _, err := log.Append(events.TypeChangeReviewed, events.ChangeReviewed{
			PartID: p.ID, Sentence: note.Sentence, Raised: note.Read && p.Level == changes.Skim,
		}); err != nil {
			return err
		}
		done++
	}
	left := total - done
	switch {
	case left == 0 || why != "":
	case total > len(parts):
		why = fmt.Sprintf("only the first %d are read", maxPartsRead)
	default:
		why = "the model gave no usable answer for them"
	}
	_, err = log.Append(events.TypeChangesReading, events.ChangesReading{Parts: done, Left: left, Why: why})
	return err
}

func (c *sessionChanges) capture(ev *events.ChangesCaptured, box *sandbox.Box) error {
	var err error
	if box == nil {
		ev.To, err = changes.Snapshot(c.workDir, c.sessionDir)
	} else {
		var layer []changes.LayerFile
		entries, lerr := box.LayerFiles()
		if lerr != nil {
			return lerr
		}
		for _, e := range entries {
			layer = append(layer, changes.LayerFile{Path: e.Path, Deleted: e.Deleted, Source: e.Source, Mode: e.Mode})
		}
		ev.To, err = changes.Overlay(c.workDir, c.sessionDir, c.from, layer)
	}
	if err != nil {
		return err
	}
	patch, truncated, err := changes.Patch(c.workDir, c.from, ev.To)
	if err != nil {
		return err
	}
	if c.mask != nil {
		patch = c.mask(patch)
	}
	// The log says that files changed and by how much. What they changed
	// to is the project's own text, and goes in the session's folder.
	if err := os.WriteFile(filepath.Join(c.sessionDir, changes.File), []byte(patch), 0o600); err != nil {
		return err
	}
	ev.Truncated = truncated
	ev.Files, ev.Added, ev.Removed = changes.Totals(changes.Parse(patch))
	return nil
}
