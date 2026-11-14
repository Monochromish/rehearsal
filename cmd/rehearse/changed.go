package main

import (
	"errors"
	"os"
	"path/filepath"

	"rehearsal/internal/changes"
	"rehearsal/internal/events"
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
	return changes.Load(c.sessionDir, l.Changes)
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
	// The log says that files changed and by how much. What they changed
	// to is the project's own text, and goes in the session's folder.
	if err := os.WriteFile(filepath.Join(c.sessionDir, changes.File), []byte(patch), 0o600); err != nil {
		return err
	}
	ev.Truncated = truncated
	ev.Files, ev.Added, ev.Removed = changes.Totals(changes.Parse(patch))
	return nil
}
