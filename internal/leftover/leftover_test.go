package leftover

import (
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	envHelper = "LEFTOVER_HELPER"
	envPort   = "LEFTOVER_PORTFILE"
	envQuit   = "LEFTOVER_QUIT"
)

// Run with LEFTOVER_HELPER set, this test binary is a small server: the
// process an agent leaves behind, and the unrelated one with the same
// name. It takes itself out of the session it was started in.
func TestMain(m *testing.M) {
	if mode := os.Getenv(envHelper); mode != "" {
		helper(mode)
		return
	}
	os.Exit(m.Run())
}

func helper(mode string) {
	_, _ = syscall.Setsid() // fails only for a process that leads its group, which is detached enough
	if mode == "stubborn" {
		signal.Ignore(syscall.SIGTERM)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(3)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	file := os.Getenv(envPort)
	if os.WriteFile(file+".tmp", []byte(port), 0o600) != nil || os.Rename(file+".tmp", file) != nil {
		os.Exit(3)
	}
	// until told to go, and never for long enough to outlive a test run
	for end := time.Now().Add(time.Minute); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(os.Getenv(envQuit)); err == nil {
			break
		}
	}
	os.Exit(0)
}

type left struct {
	port int
	quit string
}

// session runs a shell as the agent. The shell starts the helper in the
// background, waits until it listens, and exits, leaving it running.
func session(t *testing.T, tr *Tracker, mode string, clearEnv bool) left {
	t.Helper()
	dir := t.TempDir()
	portFile, quit := filepath.Join(dir, "port"), filepath.Join(dir, "quit")
	t.Cleanup(func() { _ = os.WriteFile(quit, nil, 0o600) }) // whatever the test did, the helper goes

	script := `"$0" & while [ ! -s "$` + envPort + `" ]; do sleep 0.05; done`
	if clearEnv {
		// the helper starts with none of the agent's environment, and the
		// agent stays long enough for the helper to be seen as its child
		script = `env -i ` + envHelper + `="$` + envHelper + `" ` + envPort + `="$` + envPort + `" ` + envQuit + `="$` + envQuit + `" ` + script + `; sleep 1`
	}
	cmd := exec.Command("sh", "-c", script, os.Args[0])
	cmd.Env = append(os.Environ(), tr.Env(), envHelper+"="+mode, envPort+"="+portFile, envQuit+"="+quit)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tr.Follow(cmd.Process.Pid)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the stand-in agent failed: %v", err)
	}
	return left{port: readPort(t, portFile), quit: quit}
}

func readPort(t *testing.T, file string) int {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		data, err := os.ReadFile(file)
		if err == nil {
			port, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			return port
		}
		if time.Now().After(end) {
			t.Fatalf("the helper never wrote its port: %v", err)
		}
	}
}

func listening(port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func waitClosed(t *testing.T, port int) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); listening(port); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("port %d is still open", port)
		}
	}
}

func only(t *testing.T, got []Process, port int) Process {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("listed %d processes, want the one the agent left: %+v", len(got), got)
	}
	p := got[0]
	if len(p.Ports) != 1 || p.Ports[0] != port {
		t.Errorf("ports %v, want [%d]", p.Ports, port)
	}
	if !strings.HasPrefix(p.Command, os.Args[0]) {
		t.Errorf("command %q, want the command line it was started with, beginning %q", p.Command, os.Args[0])
	}
	if p.Status != Running || p.Seconds < 0 || p.Seconds > 60 || time.Since(p.Started) > time.Minute {
		t.Errorf("status %q, running %d seconds since %v", p.Status, p.Seconds, p.Started)
	}
	return p
}

func TestAChildThatOutlivesTheAgentIsListedAndStopped(t *testing.T) {
	for name, clearEnv := range map[string]bool{"carrying the mark": false, "with its environment cleared": true} {
		t.Run(name, func(t *testing.T) {
			tr := New(nil)
			l := session(t, tr, "listen", clearEnv)
			p := only(t, tr.Conclude(), l.port)

			if listed, now := tr.Processes(); !listed || len(now) != 1 || now[0].Status != Running {
				t.Fatalf("before anything is stopped: listed %v, %+v", listed, now)
			}
			stopped, err := tr.Stop([]string{p.ID}, 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if len(stopped) != 1 || stopped[0].PID != p.PID || stopped[0].Status != Stopped {
				t.Fatalf("stop reported %+v, want process %d stopped", stopped, p.PID)
			}
			waitClosed(t, l.port)
			if _, now := tr.Processes(); now[0].Status != Stopped {
				t.Errorf("afterwards the list says %q", now[0].Status)
			}
			// asked again, it says what happened and signals nothing
			if again, err := tr.Stop([]string{p.ID}, time.Second); err != nil || again[0].Status != Stopped {
				t.Errorf("a second stop: %+v, %v", again, err)
			}
		})
	}
}

func TestAProcessThatIgnoresThePoliteSignalIsForced(t *testing.T) {
	tr := New(nil)
	l := session(t, tr, "stubborn", false)
	p := only(t, tr.Conclude(), l.port)
	stopped, err := tr.Stop([]string{p.ID}, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if stopped[0].Status != Forced {
		t.Fatalf("status %q, want forced", stopped[0].Status)
	}
	waitClosed(t, l.port)
}

func TestAnUnrelatedProcessWithTheSameNameIsLeftAlone(t *testing.T) {
	// the same program with the same name, started outside the session:
	// one before it and one while it runs
	outside := func() (int, *exec.Cmd) {
		dir := t.TempDir()
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), envHelper+"=listen", envPort+"="+filepath.Join(dir, "port"), envQuit+"="+filepath.Join(dir, "quit"))
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
		return readPort(t, filepath.Join(dir, "port")), cmd
	}
	beforePort, before := outside()

	tr := New(nil)
	l := session(t, tr, "listen", false)
	duringPort, during := outside()
	p := only(t, tr.Conclude(), l.port)
	for _, other := range []*exec.Cmd{before, during} {
		if p.PID == other.Process.Pid {
			t.Fatalf("listed process %d, which the session did not start", p.PID)
		}
		// it cannot be named, by its number or by anything else
		if _, err := tr.Stop([]string{strconv.Itoa(other.Process.Pid)}, time.Second); err == nil {
			t.Error("a process that is not on the list was accepted")
		}
	}

	if _, err := tr.Stop([]string{p.ID}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, l.port)
	for _, port := range []int{beforePort, duringPort} {
		if !listening(port) {
			t.Errorf("the unrelated process on port %d was stopped", port)
		}
	}
}

func TestAProcessThatExitsInTheMeantimeIsReportedAsGone(t *testing.T) {
	tr := New(nil)
	l := session(t, tr, "listen", false)
	p := only(t, tr.Conclude(), l.port)

	// it exits by itself, after it was listed and before anyone stops it
	if err := os.WriteFile(l.quit, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, l.port)
	for end := time.Now().Add(5 * time.Second); tr.alive(&p); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the helper did not exit")
		}
	}

	stopped, err := tr.Stop([]string{p.ID}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 1 || stopped[0].Status != Gone {
		t.Fatalf("stop reported %+v, want already gone", stopped)
	}
	if _, now := tr.Processes(); now[0].Status != Gone {
		t.Errorf("the list says %q", now[0].Status)
	}
}

func TestNothingIsListedOrStoppedBeforeTheAgentExitsOrAfterTheSession(t *testing.T) {
	tr := New(nil)
	if listed, now := tr.Processes(); listed || len(now) != 0 {
		t.Errorf("listed %v, %+v before the agent exited", listed, now)
	}
	if _, err := tr.Stop(nil, time.Second); err != ErrNotListed {
		t.Errorf("stop before the agent exited: %v", err)
	}
	l := session(t, tr, "listen", false)
	p := only(t, tr.Conclude(), l.port)
	tr.Close()
	if _, err := tr.Stop([]string{p.ID}, time.Second); err != ErrClosed {
		t.Errorf("stop after the session: %v", err)
	}
	if !listening(l.port) {
		t.Error("the process was stopped after the session was over")
	}
}
