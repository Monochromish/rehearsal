package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"rehearsal/internal/events"
	"rehearsal/internal/ident"
	"rehearsal/internal/ledger"
)

func buildRehearse(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "rehearse")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build rehearse: %v\n%s", err, out)
	}
	return bin
}

// cleanEnv drops settings that would steer the wrapped command around the
// proxy, and keeps our state in a temp dir.
func cleanEnv(t *testing.T) []string {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		key := strings.ToLower(strings.SplitN(kv, "=", 2)[0])
		if key == "no_proxy" || key == "xdg_state_home" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "XDG_STATE_HOME="+t.TempDir())
}

func TestCanaryReachesLocalServer(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not installed")
	}
	bin := buildRehearse(t)

	uploads := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("test server could not read the upload: %v", err)
		}
		uploads <- string(body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	workDir := t.TempDir()
	cmd := exec.Command(bin, "--no-open", "--", "curl", "-sS", "--data-binary", "@CANARY.txt", srv.URL+"/upload?token=do-not-store")
	cmd.Dir = workDir
	cmd.Env = cleanEnv(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rehearse curl failed: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(filepath.Join(workDir, "rehearsal-ledger.json"))
	if err != nil {
		t.Fatalf("ledger file not written: %v\n%s", err, out)
	}
	var l ledger.Ledger
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}

	word := ident.Value(l.Watching, ident.IDCanaryWord)
	if word == "" {
		t.Fatal("ledger does not list the canary word among watched identifiers")
	}
	select {
	case uploaded := <-uploads:
		if strings.TrimSpace(uploaded) != word {
			t.Errorf("test server received %q, want the canary word %q", uploaded, word)
		}
	default:
		t.Fatalf("the upload never reached the test server:\n%s", out)
	}

	var server *ledger.Server
	for i := range l.Servers {
		if l.Servers[i].Host == host {
			server = &l.Servers[i]
		}
	}
	if server == nil {
		t.Fatalf("ledger has no server %q: %s", host, raw)
	}
	if server.Requests != 1 || server.Category != events.CategoryOther || !server.Flagged {
		t.Errorf("server = {requests %d, category %s, flagged %t}, want {1, other, true}", server.Requests, server.Category, server.Flagged)
	}
	if len(server.Received) != 1 || server.Received[0].ID != ident.IDCanaryWord {
		t.Fatalf("server received %+v, want only the canary word", server.Received)
	}
	if got := server.Received[0]; got.Kind != events.KindCanary || len(got.Where) != 1 || got.Where[0] != events.WhereBody {
		t.Errorf("canary recorded as kind=%s where=%v, want canary in the body", got.Kind, got.Where)
	}

	if l.ExitCode == nil || *l.ExitCode != 0 || l.EndedAt == nil {
		t.Errorf("session end not recorded: exit=%v ended=%v", l.ExitCode, l.EndedAt)
	}
	if strings.Contains(string(raw), "do-not-store") {
		t.Error("the query string was stored in the ledger")
	}
	if !strings.Contains(string(out), host) || !strings.Contains(string(out), "EVERYTHING ELSE") {
		t.Errorf("terminal summary does not mention the server:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(workDir, "CANARY.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("CANARY.txt was not removed at the end of the session (stat: %v)", err)
	}
}

func TestExitCodeIsPassedThrough(t *testing.T) {
	bin := buildRehearse(t)
	cmd := exec.Command(bin, "--no-open", "--", "sh", "-c", "exit 3")
	cmd.Dir = t.TempDir()
	cmd.Env = cleanEnv(t)
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("rehearse exited with %v, want exit code 3", err)
	}
}

func TestExistingCanaryFileIsNotDeleted(t *testing.T) {
	bin := buildRehearse(t)
	workDir := t.TempDir()
	path := filepath.Join(workDir, "CANARY.txt")
	if err := os.WriteFile(path, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--no-open", "--", "true")
	cmd.Dir = workDir
	cmd.Env = cleanEnv(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rehearse failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("a CANARY.txt that was there before the session is gone: %v", err)
	}
}

func TestUnknownCommandFailsClearly(t *testing.T) {
	bin := buildRehearse(t)
	cmd := exec.Command(bin, "--no-open", "--", "no-such-agent-command")
	cmd.Dir = t.TempDir()
	cmd.Env = cleanEnv(t)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 127 {
		t.Fatalf("rehearse exited with %v, want exit code 127", err)
	}
	if !strings.Contains(string(out), `cannot find the command "no-such-agent-command"`) {
		t.Errorf("error does not name the missing command:\n%s", out)
	}
}
