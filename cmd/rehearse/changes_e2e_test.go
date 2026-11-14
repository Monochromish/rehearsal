package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rehearsal/internal/ledger"
)

// What an agent did to the project's files is recorded for the session:
// how much in the log, and the diff itself in the session's folder.
func TestASessionRecordsWhatChangedInTheProject(t *testing.T) {
	work, _ := repo(t)
	if err := os.WriteFile(filepath.Join(work, "notes.txt"), []byte("mine, never added\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "notes.txt") // staged work in progress
	statusBefore := git(t, work, "status", "--porcelain=v2")

	s := start(t, work, `echo changed >> README.md && mkdir -p src && echo 'package main' > src/main.go && echo more >> notes.txt`)
	code, l := s.finish()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, s.output)
	}
	c := l.Changes
	if c == nil || !c.InRepo || c.Problem != "" || c.Files != 3 || c.Added != 3 || c.Removed != 0 || c.From == "" || c.To == "" {
		t.Fatalf("changes = %+v, want three files and three added lines in a repository", c)
	}
	diff := read(t, sessionFile(t, s.cmd.Env, "changes.diff"))
	for _, want := range []string{"+changed", "diff --git a/src/main.go b/src/main.go\nnew file mode", "+more"} {
		if !strings.Contains(diff, want) {
			t.Errorf("the session's diff is missing %q:\n%s", want, diff)
		}
	}
	if strings.Contains(diff, "CANARY") {
		t.Errorf("the session's diff includes Rehearsal's own file:\n%s", diff)
	}
	if info, err := os.Stat(sessionFile(t, s.cmd.Env, "changes.diff")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the diff file: %v, %v; want one only the user can read", info, err)
	}
	// what the user had staged is still staged, and nothing more
	want := strings.Replace(statusBefore, "A. N...", "AM N...", 1)
	if got := git(t, work, "status", "--porcelain=v2"); !sameStatus(got, want) {
		t.Errorf("git status after the session:\n%s\nwant what was staged before, plus the agent's own edits unstaged:\n%s", got, want)
	}
}

// sameStatus compares two porcelain listings by path and state, leaving
// out the hashes, which differ once a file's contents have.
func sameStatus(got, want string) bool {
	state := func(s string) string {
		var out []string
		for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 && f[0] == "1" && filepath.Base(f[len(f)-1]) == "notes.txt" {
				out = append(out, f[1]+" "+f[len(f)-1])
			}
		}
		return strings.Join(out, "\n")
	}
	return state(got) == state(want)
}

func TestChangesOutsideARepositoryAreNotCaptured(t *testing.T) {
	work := t.TempDir()
	// so that a repository above the temporary folder is not taken for the project's
	s := start(t, work, `echo made > made.txt`, "GIT_CEILING_DIRECTORIES="+filepath.Dir(work))
	code, l := s.finish()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, s.output)
	}
	if c := l.Changes; c == nil || c.InRepo || c.Files != 0 || c.From != "" || c.Problem != "" {
		t.Errorf("changes = %+v, want only the fact that this is not a repository", c)
	}
	paths, err := filepath.Glob(filepath.Join(s.state, "rehearsal", "sessions", "*", "changes.diff"))
	if err != nil || len(paths) != 0 {
		t.Errorf("a diff was written outside a repository: %v (%v)", paths, err)
	}
}

// In a sandbox the agent's changes are in the sandbox's layer. They are
// what changed in the session whether or not they are ever applied.
func TestSandboxedChangesAreCapturedFromTheLayer(t *testing.T) {
	needSandbox(t)
	work, _ := repo(t)
	if err := os.MkdirAll(filepath.Join(work, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "old", "gone.txt"), []byte("going\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "Add old")

	s := startWith(t, work, []string{"--sandbox"}, `echo changed >> README.md && rm -r old && echo new > new.txt`)
	s.waitFor(ledger.PhaseDeciding)
	if status := s.decide(`{"decision":"discard"}`); status != 202 {
		t.Fatalf("discard answered with %d", status)
	}
	_, l := s.finish()
	c := l.Changes
	if c == nil || !c.InRepo || c.Problem != "" || c.Files != 3 {
		t.Fatalf("changes = %+v, want the three files the agent touched in the sandbox", c)
	}
	diff := read(t, sessionFile(t, s.cmd.Env, "changes.diff"))
	for _, want := range []string{"+changed", "diff --git a/old/gone.txt b/old/gone.txt\ndeleted file mode", "diff --git a/new.txt b/new.txt\nnew file mode"} {
		if !strings.Contains(diff, want) {
			t.Errorf("the session's diff is missing %q:\n%s", want, diff)
		}
	}
	// nothing was allowed, so the real project is as it was
	if read(t, filepath.Join(work, "README.md")) != "hello\n" {
		t.Error("README.md changed in the real project")
	}
	if _, err := os.Stat(filepath.Join(work, "new.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("new.txt is in the real project: %v", err)
	}
}
