package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"rehearsal/internal/changes"
)

// `rehearse review` with no agent and no proxy: uncommitted changes by
// default, a range when one is given, sorted by the same rules as a
// session's changes, with the repository left exactly as it was.
func TestReviewWithoutASession(t *testing.T) {
	bin := buildRehearse(t)
	work, _ := repo(t)
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(work, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := cleanEnv(t)
	run := func(args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"review", "--no-model"}, args...)...)
		cmd.Dir, cmd.Env = work, env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := run()
	if err != nil || !strings.Contains(out, "Uncommitted changes, in "+work+": 0 files") || !strings.Contains(out, "Nothing has changed.") {
		t.Errorf("with nothing changed: %v\n%s", err, out)
	}

	// committed: code and a lockfile
	write("src/pay.go", "package pay\n\nfunc total(a, b int) int {\n\treturn a + b\n}\n")
	write("package-lock.json", "{\n  \"version\": \"1.0.0\"\n}\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "-q", "-m", "Add pay")
	// uncommitted: a staged change to code, an unstaged change to the README, a new file
	write("src/pay.go", "package pay\n\nfunc total(a, b int) int {\n\treturn a - b\n}\n")
	git(t, work, "add", "src/pay.go")
	write("README.md", "hello\n\nRun the tests with make.\n")
	write("src/new_test.go", "package pay\n\nimport \"testing\"\n\nfunc TestTotal(t *testing.T) {}\n")
	statusBefore := git(t, work, "status", "--porcelain=v2")

	out, err = run()
	if err != nil {
		t.Fatalf("review: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Uncommitted changes, in " + work + ": 3 files", "To read (1)", "src/pay.go:1  +1 -1  changes code",
		"To skim (2)", "README.md:1  +2 -0  documentation", "src/new_test.go", "Kept as the session ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the review lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "package-lock.json") || strings.Contains(out, "model:") {
		t.Errorf("the review lists a committed file, or a sentence from a model that was not asked:\n%s", out)
	}
	if got := git(t, work, "status", "--porcelain=v2"); got != statusBefore {
		t.Errorf("git status changed:\n%s\nwas:\n%s", got, statusBefore)
	}
	diff := read(t, sessionFile(t, env, changes.File))
	if !strings.Contains(diff, "-\treturn a + b") || !strings.Contains(diff, "+Run the tests with make.") {
		t.Errorf("the kept diff:\n%s", diff)
	}
	// the review is a session like any other: its record verifies
	verify := exec.Command(bin, "verify")
	verify.Dir, verify.Env = work, env
	if out, err := verify.CombinedOutput(); err != nil || !strings.Contains(string(out), "Record verified") {
		t.Errorf("verify of the review's session: %v\n%s", err, out)
	}

	// a range of commits, and one commit against the files as they are
	out, err = run("HEAD~1..HEAD")
	if err != nil || !strings.Contains(out, "Changes in HEAD~1..HEAD, in "+work+": 2 files") || !strings.Contains(out, "Mechanical (1)") || !strings.Contains(out, "package-lock.json  +3 -0  lockfile") {
		t.Errorf("a range: %v\n%s", err, out)
	}
	out, err = run("HEAD~1")
	if err != nil || !strings.Contains(out, "Changes since HEAD~1, in "+work+": 4 files") {
		t.Errorf("one commit against the working files: %v\n%s", err, out)
	}
	git(t, work, "branch", "side", "HEAD~1")
	out, err = run("side...main")
	if err != nil || !strings.Contains(out, "Changes in side...main, in "+work+": 2 files") {
		t.Errorf("a three-dot range: %v\n%s", err, out)
	}

	// what cannot be reviewed says why, and leaves no session behind
	sessions := func() int {
		entries, err := os.ReadDir(filepath.Dir(filepath.Dir(sessionFile(t, env, changes.File))))
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := sessions()
	for _, bad := range []string{"no-such-branch", "..HEAD", "--output=/tmp/x..HEAD", "main...no-such-branch"} {
		if out, err := run(bad); err == nil {
			t.Errorf("review %q succeeded:\n%s", bad, out)
		}
	}
	if sessions() != before {
		t.Errorf("a review that failed left a session behind: %d folders, was %d", sessions(), before)
	}
	outside := exec.Command(bin, "review", "--no-model")
	outside.Dir, outside.Env = t.TempDir(), env
	if out, err := outside.CombinedOutput(); err == nil || !strings.Contains(string(out), "not inside a git repository") {
		t.Errorf("outside a repository: %v\n%s", err, out)
	}
}
