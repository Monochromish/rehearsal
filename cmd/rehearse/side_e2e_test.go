package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
)

// twoWorlds finds a port with the two after it free, and the same ten
// ports on, which is what side-by-side.sh uses.
func twoWorlds(t *testing.T) int {
	t.Helper()
	for base := 21000 + os.Getpid()%7000; base < 40000; base += 37 {
		var held []net.Listener
		free := true
		for _, port := range []int{base, base + 1, base + 2, base + 10, base + 11, base + 12} {
			l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				free = false
				break
			}
			held = append(held, l)
		}
		for _, l := range held {
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if free {
			return base
		}
	}
	t.Fatal("no free ports for the two demo worlds")
	return 0
}

// holding lists the files under dir that contain any of the texts.
func holding(t *testing.T, dir string, texts ...string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, text := range texts {
			if bytes.Contains(data, []byte(text)) {
				rel, err := filepath.Rel(dir, path)
				if err != nil {
					return err
				}
				found = append(found, rel)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// The same agent script in two copies of the demo at once. With nothing in
// front of it the typed password reaches the inbox and the database goes.
// Under rehearse neither happens, four actions wait, and the password is
// in nothing rehearse wrote.
func TestSideBySide(t *testing.T) {
	needCurl(t)
	bin := buildRehearse(t)
	script, err := filepath.Abs(filepath.Join("..", "..", "demo", "side-by-side.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "side by side")
	base := twoWorlds(t)
	page := "http://" + closedPort(t)
	// characters a shell, an address and a page would each trip on
	const typed = `judge's <b>pass</b> word 100%!`
	// as curl writes it into an address: escapes in lower case
	encoded := regexp.MustCompile(`%[0-9A-F]{2}`).ReplaceAllStringFunc(url.QueryEscape(typed), strings.ToLower)
	if encoded == typed || strings.Contains(encoded, "<") {
		t.Fatalf("the encoded form is %q", encoded)
	}
	env := cleanEnv(t, "REHEARSE="+bin, "REHEARSAL_SIDE_DIR="+dir, fmt.Sprintf("REHEARSAL_SIDE_PORT=%d", base),
		"REHEARSAL_SIDE_OPTIONS=--no-open --port "+page[strings.LastIndexByte(page, ':')+1:])
	page = strings.Replace(page, "127.0.0.1", "localhost", 1)

	out := &output{}
	run := exec.Command("sh", script)
	run.Stdin = strings.NewReader("short\n" + typed + "\n")
	run.Stdout, run.Stderr, run.Env = out, out, env
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- run.Wait() }()
	reset := func() (string, time.Duration) {
		t.Helper()
		began := time.Now()
		cmd := exec.Command("sh", script, "--reset")
		cmd.Env = env
		text, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("reset: %v\n%s", err, text)
		}
		return string(text), time.Since(began)
	}
	t.Cleanup(func() {
		if _, err := os.Stat(dir); err == nil {
			reset()
		}
	})

	getJSON := func(address string, into any) bool {
		res, err := http.Get(address)
		if err != nil {
			return false
		}
		body, err := io.ReadAll(res.Body)
		if cerr := res.Body.Close(); err != nil || cerr != nil || res.StatusCode != http.StatusOK {
			return false
		}
		return json.Unmarshal(body, into) == nil
	}

	// world B's session comes to rest with its actions held
	var held heldView
	for deadline := time.Now().Add(120 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if getJSON(page+"/api/held", &held) && held.Phase == ledger.PhaseDeciding {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("the script ended (%v) before anything was held:\n%s", err, out)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("world B never waited for a decision:\n%s", out)
		}
	}
	if !strings.Contains(out.String(), "It needs at least 8 characters.") {
		t.Errorf("a password of 5 characters was not turned down:\n%s", out)
	}
	if len(held.Actions) != 4 {
		t.Fatalf("%d actions held in world B, want 4: %+v", len(held.Actions), held.Actions)
	}
	kinds := map[string]int{}
	for _, a := range held.Actions {
		kinds[string(a.Kind)+" "+a.Method]++
		if a.Kind == events.ActionHTTP && a.Method != http.MethodDelete && (len(a.Carries) != 1 || a.Carries[0].Kind != events.KindSecret) {
			t.Errorf("held %s %s%s carries %+v, want the typed password as a secret", a.Method, a.Host, a.Path, a.Carries)
		}
	}
	if kinds["http POST"] != 1 || kinds["http GET"] != 1 || kinds["http DELETE"] != 1 || kinds["git-push "] != 1 {
		t.Errorf("held %v, want the post, the get, the delete and the push", kinds)
	}

	// the page's view of the two worlds, from either cloud host
	for _, port := range []int{base, base + 10} {
		var s struct {
			Without, With struct {
				Inbox      []string
				Production bool
			}
		}
		if !getJSON(fmt.Sprintf("http://localhost:%d/compare/state", port), &s) {
			t.Fatalf("no state from the cloud host on port %d", port)
		}
		if len(s.Without.Inbox) != 2 || !strings.Contains(s.Without.Inbox[0], "PAYMENTS_API_KEY="+typed) || !strings.Contains(s.Without.Inbox[1], encoded) || s.Without.Production {
			t.Errorf("without Rehearsal: inbox %q, database there %v; want the password received twice and the database gone", s.Without.Inbox, s.Without.Production)
		}
		if len(s.With.Inbox) != 0 || !s.With.Production {
			t.Errorf("with Rehearsal: inbox %q, database there %v; want nothing received and the database there", s.With.Inbox, s.With.Production)
		}
		res, err := http.Get(fmt.Sprintf("http://localhost:%d/compare", port))
		if err != nil || res.StatusCode != http.StatusOK {
			t.Errorf("the page on port %d: %v", port, err)
		}
		if err == nil {
			if err := res.Body.Close(); err != nil {
				t.Error(err)
			}
		}
	}

	// refuse everything; the script then ends by itself
	req, err := http.NewRequest(http.MethodPost, page+"/api/apply", strings.NewReader(`{"decision":"discard"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", page)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusAccepted {
		t.Fatalf("discard: %v", err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the script ended with %v:\n%s", err, out)
		}
	case <-time.After(120 * time.Second):
		t.Fatalf("the script did not end after the decision:\n%s", out)
	}
	if text := out.String(); !strings.Contains(text, "The agent was told 4 actions worked, including deleting database production. None has happened.") {
		t.Errorf("world B's summary does not open with what the script was told:\n%s", text)
	}

	// Nothing rehearse wrote for world B holds the password, plain or as
	// it went into an address: not the log, the ledger, the report or
	// anything else in its state folder, and not the export.
	state := filepath.Join(dir, "with", "state")
	if found := holding(t, state, typed, encoded); len(found) != 0 {
		t.Errorf("world B's state holds the typed password in %v", found)
	}
	for _, name := range []string{"events.db", "rehearsal-ledger.json", "rehearsal-report.md"} {
		if matches, err := filepath.Glob(filepath.Join(state, "rehearsal", "sessions", "*", name)); err != nil || len(matches) != 1 {
			t.Errorf("world B's session has no %s (%v)", name, err)
		}
	}
	export := exec.Command(bin, "export", "--format", "jsonl")
	export.Dir = filepath.Join(dir, "with", "project")
	export.Env = append(env, "XDG_STATE_HOME="+state)
	record, err := export.CombinedOutput()
	if err != nil {
		t.Fatalf("export: %v\n%s", err, record)
	}
	if bytes.Contains(record, []byte(typed)) || bytes.Contains(record, []byte(encoded)) || !bytes.Contains(record, []byte("Value of PAYMENTS_API_KEY from .env")) {
		t.Errorf("the export holds the typed password, or does not name it by its label:\n%s", record)
	}
	// On disk it is in the two .env files and nowhere else that can be
	// read as text. (Each project's git objects hold .env too, packed.)
	if found := holding(t, dir, typed); len(found) != 2 || found[0] != filepath.Join("with", "project", ".env") || found[1] != filepath.Join("without", "project", ".env") {
		t.Errorf("the typed password is in %v, want only the two .env files", found)
	}

	text, took := reset()
	if took > 10*time.Second {
		t.Errorf("reset took %v, want under 10 seconds", took)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("after reset %s is still there (%v): %s", dir, err, text)
	}
	if found := holding(t, filepath.Dir(dir), typed, encoded); len(found) != 0 {
		t.Errorf("after reset the typed password is still in %v", found)
	}
	for _, port := range []int{base, base + 11} {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
			_ = c.Close()
			t.Errorf("after reset something still listens on port %d", port)
		}
	}
	if again, _ := reset(); !strings.Contains(again, "Nothing to put back") {
		t.Errorf("a second reset said: %s", again)
	}
}
