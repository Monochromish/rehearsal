package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"rehearsal/internal/changes"
	"rehearsal/internal/events"
	"rehearsal/internal/record"
	"rehearsal/internal/server"
	"rehearsal/internal/webui"
)

type reviewOptions struct {
	noModel bool
	noOpen  bool
	port    int
}

func reviewCommand() *cobra.Command {
	var opts reviewOptions
	cmd := &cobra.Command{
		Use:   "review [range]",
		Short: "Sort any set of changes by how much reading they need, with no agent and no proxy",
		Long: "With no range, the changes are everything not yet committed: staged, unstaged and new files.\n" +
			"A range is one git understands: A..B compares two commits, A...B compares B with the point\n" +
			"where it left A, and a single name compares that commit with the files as they are now.\n\n" +
			"The changes are split into parts and sorted by rules into those to read, those to skim and\n" +
			"mechanical ones. The local model, when there is one, then adds a sentence to each part to\n" +
			"read or skim. Nothing in the repository is changed, and nothing is sent anywhere else.\n" +
			"The review is kept as a session of its own, so the app window can show it.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReview(cmd.Context(), cmd.OutOrStdout(), first(args), opts)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.noModel, "no-model", false, "sort by the rules alone: do not ask the local model for anything")
	f.BoolVar(&opts.noOpen, "no-open", false, "do not open the app window, even when "+envApp+" names one")
	f.IntVar(&opts.port, "port", 0, "port to serve the review on for the app window (default: any free port)")
	return cmd
}

// reviewRange turns what the user asked for into the two trees to
// compare, and words for what that is.
func reviewRange(workDir, scratch, asked string) (from, to, what string, err error) {
	working := func() (string, error) { return changes.Snapshot(workDir, scratch) }
	switch a, b, two := strings.Cut(asked, ".."); {
	case asked == "":
		if from, err = changes.Tree(workDir, "HEAD"); err != nil {
			// a repository with no commit yet: everything in it is new
			if from, err = changes.EmptyTree(workDir); err != nil {
				return "", "", "", err
			}
		}
		to, err = working()
		return from, to, "uncommitted changes", err
	case two && strings.HasPrefix(b, "."):
		// A...B: what B has that the point where it left A does not
		b = strings.TrimPrefix(b, ".")
		if a == "" || b == "" {
			return "", "", "", fmt.Errorf("%q names only one side of a range", asked)
		}
		base, err := changes.MergeBase(workDir, a, b)
		if err != nil {
			return "", "", "", err
		}
		if from, err = changes.Tree(workDir, base); err != nil {
			return "", "", "", err
		}
		to, err = changes.Tree(workDir, b)
		return from, to, "changes in " + asked, err
	case two:
		if a == "" {
			return "", "", "", fmt.Errorf("%q does not say where the range starts", asked)
		}
		if b == "" {
			b = "HEAD"
		}
		if from, err = changes.Tree(workDir, a); err != nil {
			return "", "", "", err
		}
		to, err = changes.Tree(workDir, b)
		return from, to, "changes in " + a + ".." + b, err
	default:
		if from, err = changes.Tree(workDir, asked); err != nil {
			return "", "", "", err
		}
		to, err = working()
		return from, to, "changes since " + asked, err
	}
}

func runReview(ctx context.Context, w io.Writer, asked string, opts reviewOptions) (err error) {
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find the working directory: %w", err)
	}
	state, err := stateDir()
	if err != nil {
		return err
	}
	sessionDir := filepath.Join(state, "sessions", time.Now().Format("20060102-150405")+"-"+strings.ToLower(rand.Text()[:6]))
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	from, to, what, err := reviewRange(workDir, sessionDir, asked)
	if err != nil {
		// nothing was recorded, so no empty session is left behind
		return errors.Join(fmt.Errorf("review: %w", err), os.RemoveAll(sessionDir))
	}
	patch, truncated, err := changes.Patch(workDir, from, to)
	if err != nil {
		return errors.Join(fmt.Errorf("review: %w", err), os.RemoveAll(sessionDir))
	}
	if err := os.WriteFile(filepath.Join(sessionDir, changes.File), []byte(patch), 0o600); err != nil {
		return fmt.Errorf("keep the diff: %w", err)
	}
	log, err := events.Open(filepath.Join(sessionDir, "events.db"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, log.Close()) }()
	signer, keyErr := signingKey()
	if _, err := log.Append(events.TypeSessionStarted, events.SessionStarted{
		Command: []string{"review"}, WorkDir: workDir, ProviderHosts: []string{}, Identifiers: []events.Identifier{},
		ToolVersion: version, Unsigned: keyErr != nil, Review: what,
	}); err != nil {
		return err
	}
	files, added, removed := changes.Totals(changes.Parse(patch))
	if _, err := log.Append(events.TypeChangesCaptured, events.ChangesCaptured{
		InRepo: true, From: from, To: to, Files: files, Added: added, Removed: removed, Truncated: truncated,
	}); err != nil {
		return err
	}
	c := &sessionChanges{workDir: workDir, sessionDir: sessionDir, inRepo: true, from: from}

	fmt.Fprintf(w, "%s, in %s: %s, %d added and %d removed.\n", upperFirst(what), workDir, countOf(files, "file", "files"), added, removed)
	if truncated {
		fmt.Fprintln(w, "The diff was too long to keep whole, and is cut short.")
	}
	model := investigator(investigateOptions{noModel: opts.noModel})

	// The window, when one is named, is opened before the model reads, so
	// that the sorted list is on screen at once and the sentences fill in.
	app := os.Getenv(envApp)
	window := app != "" && !opts.noOpen && files > 0
	if window {
		web, err := server.Start(log, webui.FS(), opts.port)
		if err != nil {
			return err
		}
		defer func() {
			stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = errors.Join(err, web.Shutdown(stop))
		}()
		web.ShowChanges(func() (changes.Review, error) { return c.review(log) })
		web.ServeSessions(filepath.Dir(sessionDir), filepath.Base(sessionDir))
		described := server.Info{Version: version}
		if model != nil {
			described.Reviewer = model.Name()
			web.UseInvestigator(model)
		}
		web.Describe(described)
		if oerr := openApp(app, web.URL); oerr != nil {
			fmt.Fprintf(os.Stderr, "rehearse: could not open the app window named by %s (%v). The review is below.\n", envApp, oerr)
			window = false
		}
	}

	if model != nil && files > 0 {
		fmt.Fprintf(w, "Asking %s what each part to read or skim does (at most %d parts and %d seconds)…\n", model.Name(), maxPartsRead, int(readingBudget.Seconds()))
		if reader, ok := model.(changeReader); ok {
			if rerr := c.read(ctx, log, reader, time.Now); rerr != nil {
				fmt.Fprintf(os.Stderr, "rehearse: the local model could not read the changes: %v\n", rerr)
			}
		}
	}
	r, err := c.review(log)
	if err != nil {
		return err
	}
	printReview(w, r)
	if _, err := log.Append(events.TypeSessionEnded, events.SessionEnded{}); err != nil {
		return err
	}
	if window {
		fmt.Fprintln(w, "\nThe app window is showing this review. Press Ctrl-C here to close it.")
		wait, stop := signal.NotifyContext(ctx, os.Interrupt)
		<-wait.Done()
		stop()
	}
	if signer != nil {
		// sealed first: an entry written after the signature would make the record fail to verify
		entries, finalHash := log.Seal()
		if err := record.WriteCheckpoint(sessionDir, record.SignCheckpoint(signer, filepath.Base(sessionDir), entries, finalHash, time.Now())); err != nil {
			return err
		}
	}
	fmt.Fprintf(w, "\nKept as the session %s.\n", filepath.Base(sessionDir))
	return nil
}

// printReview lists the parts as the window would: those to read, those
// to skim, then the mechanical ones, each with the rules' reason and, when
// the model wrote one, its sentence.
func printReview(w io.Writer, r changes.Review) {
	if len(r.Parts) == 0 {
		fmt.Fprintln(w, "\nNothing has changed.")
		return
	}
	groups := []struct {
		level changes.Level
		name  string
	}{{changes.Read, "To read"}, {changes.Skim, "To skim"}, {changes.Mechanical, "Mechanical"}}
	for _, g := range groups {
		var parts []changes.Part
		for _, p := range r.Parts {
			if p.Level == g.level {
				parts = append(parts, p)
			}
		}
		if len(parts) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s (%d)\n", g.name, len(parts))
		for _, p := range parts {
			where := p.Path
			if p.Line > 0 {
				where = fmt.Sprintf("%s:%d", p.Path, p.Line)
			}
			reason := p.Reason
			if p.Raised {
				reason += "; the model said to read it"
			}
			fmt.Fprintf(w, "  %s  +%d -%d  %s\n", where, p.Added, p.Removed, reason)
			if p.Sentence != "" {
				fmt.Fprintf(w, "      model: %s\n", p.Sentence)
			}
		}
	}
	if r.ReadingNote != "" {
		fmt.Fprintf(w, "\n%s\n", r.ReadingNote)
	}
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
