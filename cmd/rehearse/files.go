package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"rehearsal/internal/events"
	"rehearsal/internal/hold"
	"rehearsal/internal/ledger"
	"rehearsal/internal/redact"
	"rehearsal/internal/sandbox"
)

// how often the sandbox's layer is looked at for sensitive writes
const watchEvery = 2 * time.Second

// Paths under the home folder that a sandboxed agent may write to for
// real, because it keeps its own state there and stops working, or logs
// the user out, when it cannot:
//
//	.claude       Claude Code's login, history and settings
//	.claude.json  Claude Code's own record of projects and start-up state
//	.codex        the same for Codex
//	.cache, .npm  download caches that tools fail without
//
// Everything else under home is read-only inside the sandbox.
var writableInHome = []string{".claude", ".claude.json", ".codex", ".cache", ".npm"}

// sandboxWritable is the default writable paths that exist, and any the
// user added.
func sandboxWritable(extra []string) []string {
	out := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		for _, name := range writableInHome {
			if p := filepath.Join(home, name); pathExists(p) {
				out = append(out, p)
			}
		}
	}
	for _, p := range extra {
		if abs, err := filepath.Abs(p); err == nil && pathExists(abs) {
			out = append(out, abs)
		}
	}
	return out
}

// Folders under the home folder that hold keys and cloud logins. A
// sandboxed agent sees them empty. What it reads can't be observed, so the
// only way to know these were not read is for them not to be there.
var hiddenInHome = []string{".ssh", ".aws", ".config/gcloud", ".azure", ".kube", ".gnupg"}

func sandboxHidden() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range hiddenInHome {
		out = append(out, filepath.Join(home, filepath.FromSlash(name)))
	}
	return out
}

func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// canaryPlanted is when the session's canary file was written, by the
// clock of the file system the project is on. Real files modified after
// it changed during the session. Reading it from a file avoids comparing
// this machine's clock with another's, which matters on a Windows drive.
func canaryPlanted(workDir string) time.Time {
	info, err := os.Stat(filepath.Join(workDir, canaryFile))
	if err != nil {
		return time.Now()
	}
	return info.ModTime()
}

// fileChanges turns what a sandboxed agent did to the project's files
// into held actions, and answers the page's questions about them.
type fileChanges struct {
	box     *sandbox.Box
	log     *events.Log
	holder  *hold.Holder
	workDir string
	keep    []string

	mu   sync.Mutex
	byID map[string]events.FileChange

	stopWatch func()
	watchErr  error
}

// watch looks at the layer every few seconds while the agent runs and
// records writes to sensitive paths as they appear.
func (f *fileChanges) watch() {
	w := f.box.NewWatch()
	done, finished := make(chan struct{}), make(chan struct{})
	note := func() {
		found, err := w.New()
		for _, s := range found {
			if _, aerr := f.log.Append(events.TypeSensitiveWrite, s); aerr != nil {
				err = errors.Join(err, aerr)
			}
		}
		if err != nil && f.watchErr == nil {
			f.watchErr = fmt.Errorf("watch the sandbox for sensitive writes: %w", err)
		}
	}
	go func() {
		defer close(finished)
		tick := time.NewTicker(watchEvery)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				note()
			case <-done:
				note() // whatever the agent did last
				return
			}
		}
	}()
	f.stopWatch = sync.OnceFunc(func() {
		close(done)
		<-finished
	})
}

// hold lists the changes in the layer as held actions. It is called once,
// when the agent has exited.
func (f *fileChanges) hold() error {
	f.stopWatch()
	changes, err := f.box.Changes()
	if err != nil {
		return err
	}
	for _, c := range changes {
		change := c.FileChange
		id, err := f.holder.Hold(events.ActionHeld{
			Kind: events.ActionFile, WorkDir: f.workDir, Destination: "your project folder", Subject: change.Path,
			File: &change, SecretFiles: c.SecretFiles,
		}, hold.Payload{})
		if err != nil {
			return err
		}
		f.mu.Lock()
		f.byID[id] = change
		f.mu.Unlock()
	}
	return f.watchErr
}

// diff is the text of one held file change, with anything that looks like
// a credential masked.
func (f *fileChanges) diff(id string) (string, error) {
	f.mu.Lock()
	change, ok := f.byID[id]
	f.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("%q is not a held file change", id)
	}
	text, err := f.box.Diff(change)
	if err != nil {
		return "", err
	}
	return redact.Text(text, f.keep...), nil
}

// finish deletes the layer, unless a change the user allowed did not make
// it into the real project. Then the layer is the only copy of the
// agent's work, and it is left where it is.
func (f *fileChanges) finish() error {
	l, err := ledger.FromLog(f.log)
	if err != nil {
		return err
	}
	for _, a := range l.Actions {
		if a.Kind == events.ActionFile && (a.Outcome == ledger.Failed || a.Outcome == ledger.NotApplied) {
			fmt.Fprintf(os.Stderr, "rehearse: a file change you allowed was not applied. The agent's versions of the files are kept in %s\n", f.box.Layer())
			return nil
		}
	}
	return f.box.Remove()
}
