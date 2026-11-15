package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"rehearsal/internal/events"
	"rehearsal/internal/ident"
	"rehearsal/internal/ledger"
	"rehearsal/internal/leftover"
)

// envListener names the file a left-behind server writes its port to.
// The server exits when a file of that name with ".quit" added appears.
const envListener = "REHEARSE_TEST_LISTENER"

func listenUntilTold(portFile string) {
	_, _ = syscall.Setsid() // out of the agent's session; fails only where it is already apart
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(3)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	if os.WriteFile(portFile+".tmp", []byte(port), 0o600) != nil || os.Rename(portFile+".tmp", portFile) != nil {
		os.Exit(3)
	}
	for end := time.Now().Add(time.Minute); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(portFile + ".quit"); err == nil {
			break
		}
	}
	os.Exit(0)
}

// leavesAServer is an agent's script that starts this test binary as a
// server in the background and carries on once it listens, and the
// environment that goes with it.
func leavesAServer(t *testing.T) (script, portFile string, env []string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	portFile = filepath.Join(t.TempDir(), "port")
	t.Cleanup(func() { _ = os.WriteFile(portFile+".quit", nil, 0o600) }) // whatever the test did, the server goes
	script = `"$LEFT_BEHIND" >/dev/null 2>&1 & while [ ! -s "$` + envListener + `" ]; do sleep 0.05; done`
	return script, portFile, []string{"LEFT_BEHIND=" + self, envListener + "=" + portFile}
}

func portIn(t *testing.T, portFile string) int {
	t.Helper()
	data, err := os.ReadFile(portFile)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func accepts(port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func closes(t *testing.T, port int) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); accepts(port); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("port %d is still open", port)
		}
	}
}

// exported is the session's record, one entry per line.
func exported(t *testing.T, s *session) string {
	t.Helper()
	cmd := exec.Command(buildRehearse(t), "export", "--format", "jsonl")
	cmd.Dir, cmd.Env = s.workDir, s.cmd.Env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("export: %v\n%s", err, out)
	}
	verify := exec.Command(buildRehearse(t), "verify")
	verify.Dir, verify.Env = s.workDir, s.cmd.Env
	if out, err := verify.CombinedOutput(); err != nil {
		t.Errorf("the record does not verify: %v\n%s", err, out)
	}
	return string(out)
}

func entries(t *testing.T, record string, typ events.Type) []events.Event {
	t.Helper()
	var found []events.Event
	for _, line := range strings.Split(strings.TrimSpace(record), "\n") {
		var ev events.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		if ev.Type == typ {
			found = append(found, ev)
		}
	}
	return found
}

func TestWhatTheAgentLeftRunningIsPrintedAndLeftAlone(t *testing.T) {
	script, portFile, env := leavesAServer(t)
	s := startWith(t, t.TempDir(), []string{"--no-hold"}, script, env...)
	if code, _ := s.finish(); code != 0 {
		t.Fatalf("exit %d\n%s", code, s.output)
	}
	port := portIn(t, portFile)
	out := s.output.String()
	for _, want := range []string{"left running: process ", fmt.Sprintf("listening on port %d", port), "Nothing was stopped. Run with --stop-leftovers"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not say %q:\n%s", want, out)
		}
	}
	if !accepts(port) {
		t.Error("the server the agent left was stopped, and nobody asked for that")
	}

	record := exported(t, s)
	left := entries(t, record, events.TypeProcessLeft)
	if len(left) != 1 {
		t.Fatalf("%d process_left entries, want 1:\n%s", len(left), record)
	}
	var p events.ProcessLeft
	if err := json.Unmarshal(left[0].Data, &p); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	if p.ID != "p1" || p.PID <= 0 || p.Command != self || len(p.Ports) != 1 || p.Ports[0] != port || p.Started.IsZero() {
		t.Errorf("recorded %+v, want the server on port %d started as %q", p, port, self)
	}
	if got := entries(t, record, events.TypeProcessStopped); len(got) != 0 {
		t.Errorf("%d process_stopped entries for a session that stopped nothing", len(got))
	}
}

func TestStopLeftoversStopsWhatTheAgentLeft(t *testing.T) {
	script, portFile, env := leavesAServer(t)
	s := startWith(t, t.TempDir(), []string{"--no-hold", "--stop-leftovers"}, script, env...)
	if code, _ := s.finish(); code != 0 {
		t.Fatalf("exit %d\n%s", code, s.output)
	}
	port := portIn(t, portFile)
	closes(t, port)
	if out := s.output.String(); !strings.Contains(out, "\n  stopped: process ") || strings.Contains(out, "Nothing was stopped") {
		t.Errorf("the output does not say the server was stopped:\n%s", out)
	}
	stopped := entries(t, exported(t, s), events.TypeProcessStopped)
	if len(stopped) != 1 {
		t.Fatalf("%d process_stopped entries, want 1", len(stopped))
	}
	var p events.ProcessStopped
	if err := json.Unmarshal(stopped[0].Data, &p); err != nil {
		t.Fatal(err)
	}
	if p.ID != "p1" || p.Outcome != string(leftover.Stopped) {
		t.Errorf("recorded %+v, want p1 stopped", p)
	}
}

func TestThePageListsAndStopsWhatTheAgentLeft(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not installed")
	}
	srv := newUploads(t)
	script, portFile, env := leavesAServer(t)
	// something is held, so rehearse stays for the page's decision
	s := start(t, t.TempDir(), script+`; curl -sS -o /dev/null --data-binary @CANARY.txt `+srv.address()+`/upload`, env...)

	list := func() leftover.List {
		t.Helper()
		res, err := http.Get(s.url + "/api/leftovers")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		if cerr := res.Body.Close(); err != nil || cerr != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("list: %d %v %v\n%s", res.StatusCode, err, cerr, body)
		}
		var l leftover.List
		if err := json.Unmarshal(body, &l); err != nil {
			t.Fatalf("%v\n%s", err, body)
		}
		return l
	}
	stop := func(body string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, s.url+"/api/leftovers/stop", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", s.url)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		text, err := io.ReadAll(res.Body)
		if cerr := res.Body.Close(); err != nil || cerr != nil {
			t.Fatal(err, cerr)
		}
		return res.StatusCode, string(text)
	}

	s.waitFor(ledger.PhaseDeciding)
	port := portIn(t, portFile)
	l := list()
	if !l.Listed || !l.Open || len(l.Processes) != 1 {
		t.Fatalf("the list: %+v\n%s", l, s.output)
	}
	p := l.Processes[0]
	if p.Status != leftover.Running || len(p.Ports) != 1 || p.Ports[0] != port {
		t.Errorf("listed %+v, want the server running on port %d", p, port)
	}
	// the session's mark is the agent's alone and is not part of what is shown
	if strings.Contains(p.Command, leftover.EnvMark) {
		t.Errorf("the command shown carries the session's mark: %q", p.Command)
	}

	if status, text := stop(`{"stop":["` + strconv.Itoa(p.PID) + `"]}`); status != http.StatusBadRequest {
		t.Errorf("a process number in place of a listed process was answered %d: %s", status, text)
	}
	if !accepts(port) {
		t.Fatal("a refused request stopped the server")
	}
	status, text := stop(`{"stop":["` + p.ID + `"]}`)
	var answer struct {
		Processes []leftover.Process `json:"processes"`
	}
	if err := json.Unmarshal([]byte(text), &answer); err != nil || status != http.StatusOK {
		t.Fatalf("stop answered %d: %s (%v)", status, text, err)
	}
	if len(answer.Processes) != 1 || answer.Processes[0].Status != leftover.Stopped {
		t.Errorf("stop reported %+v, want it stopped", answer.Processes)
	}
	closes(t, port)
	if l := list(); l.Open || l.Processes[0].Status != leftover.Stopped {
		t.Errorf("afterwards the list is %+v", l)
	}

	if status := s.decide(`{"decision":"discard"}`); status != http.StatusAccepted {
		t.Fatalf("discard answered %d", status)
	}
	if code, _ := s.finish(); code != 0 {
		t.Errorf("exit %d\n%s", code, s.output)
	}
	if out := s.output.String(); !strings.Contains(out, "\n  stopped: process ") {
		t.Errorf("the output does not say the server was stopped:\n%s", out)
	}
	if got := srv.received(); len(got) != 0 {
		t.Errorf("the discarded upload was sent: %q", got)
	}
	if got := entries(t, exported(t, s), events.TypeProcessStopped); len(got) != 1 {
		t.Errorf("%d process_stopped entries, want 1", len(got))
	}
}

// leftLines are the lines of the list rehearse prints at the end: what
// became of each process, its number and its command.
func leftLines(out string) [][]string {
	return regexp.MustCompile(`(?m)^  ([a-z ]+): process (\d+), [^:]*: (.*)$`).FindAllStringSubmatch(out, -1)
}

func running(pid string) bool {
	data, err := os.ReadFile(filepath.Join("/proc", pid, "stat"))
	if err != nil {
		return false
	}
	// After the name in brackets comes the state. Z is a process that has
	// exited and not yet been collected.
	rest := strings.TrimSpace(string(data[bytes.LastIndexByte(data, ')')+1:]))
	return !strings.HasPrefix(rest, "Z")
}

// A process the agent leaves inside the sandbox goes on running there. It
// is listed and stopped from outside, and the sandbox's own first
// process, which stays as long as anything inside does, is not listed.
func TestInTheSandboxWhatTheAgentLeftIsListedAndStopped(t *testing.T) {
	needSandbox(t)
	const script = `sleep 273 >/dev/null 2>&1 & sleep 0.2`

	t.Run("left running", func(t *testing.T) {
		work, _ := repo(t)
		s := startWith(t, work, []string{"--sandbox"}, script)
		// rehearse is done when it says where the ledger was saved. The
		// sandbox's first process holds this test's pipe open until the
		// sleep ends, so the exit itself is waited for after that.
		for end := time.Now().Add(60 * time.Second); !strings.Contains(s.output.String(), "\nSaved:"); time.Sleep(50 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("rehearse did not finish:\n%s", s.output)
			}
		}
		defer func() {
			if code, _ := s.finish(); code != 0 {
				t.Errorf("exit %d\n%s", code, s.output)
			}
		}()
		lines := leftLines(s.output.String())
		if len(lines) != 1 || lines[0][1] != "left running" || lines[0][3] != "sleep 273" {
			t.Fatalf("listed %q, want only the sleep, left running:\n%s", lines, s.output)
		}
		pid := lines[0][2]
		if !running(pid) {
			t.Fatalf("process %s was stopped, and nobody asked for that", pid)
		}
		// the test's own to end: it is the sleep this test's agent started
		if args, err := os.ReadFile(filepath.Join("/proc", pid, "cmdline")); err != nil || string(args) != "sleep\x00273\x00" {
			t.Fatalf("process %s is %q (%v), not the sleep the agent started", pid, args, err)
		}
		n, err := strconv.Atoi(pid)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(n, syscall.SIGKILL); err != nil {
			t.Error(err)
		}
	})

	t.Run("stopped", func(t *testing.T) {
		work, _ := repo(t)
		s := startWith(t, work, []string{"--sandbox", "--stop-leftovers"}, script)
		if code, _ := s.finish(); code != 0 {
			t.Fatalf("exit %d\n%s", code, s.output)
		}
		lines := leftLines(s.output.String())
		if len(lines) != 1 || lines[0][1] != "stopped" || lines[0][3] != "sleep 273" {
			t.Fatalf("listed %q, want only the sleep, stopped:\n%s", lines, s.output)
		}
		for end := time.Now().Add(5 * time.Second); running(lines[0][2]); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("process %s is still running", lines[0][2])
			}
		}
	})
}

// With the page open and nothing else to decide, rehearse waits for the
// page to say what to stop. An answer that stops nothing is an answer.
func TestRehearseWaitsForThePageToChoose(t *testing.T) {
	script, portFile, env := leavesAServer(t)
	log, err := events.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := log.Close(); err != nil {
			t.Error(err)
		}
	}()
	left := followLeftovers(log, ident.NewMatcher(nil), nil)
	agent := exec.Command("sh", "-c", script)
	agent.Env = append(append(os.Environ(), env...), left.tracker.Env())
	if err := agent.Start(); err != nil {
		t.Fatal(err)
	}
	left.tracker.Follow(agent.Process.Pid)
	if err := agent.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := left.list(false); err != nil {
		t.Fatal(err)
	}
	port := portIn(t, portFile)

	finished := make(chan error, 1)
	go func() { finished <- left.finish(true, "http://localhost:1") }()
	select {
	case err := <-finished:
		t.Fatalf("rehearse did not wait for the page (%v)", err)
	case <-time.After(700 * time.Millisecond):
	}
	if got, err := left.Stop(nil); err != nil || len(got) != 0 {
		t.Fatalf("an answer that stops nothing: %+v, %v", got, err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("rehearse kept waiting after the page answered")
	}
	if !accepts(port) {
		t.Error("the server was stopped by an answer that named nothing")
	}
	if _, err := left.Stop([]string{"p1"}); err != leftover.ErrClosed {
		t.Errorf("a stop after the session was over: %v", err)
	}
}
