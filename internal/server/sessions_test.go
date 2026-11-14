package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"rehearsal/internal/changes"
	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
)

// recorded makes a session folder with a short log in it.
func recorded(t *testing.T, sessions, name, workDir string, more func(log *events.Log)) *events.Log {
	t.Helper()
	dir := filepath.Join(sessions, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	log, err := events.Open(filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, WorkDir: workDir}); err != nil {
		t.Fatal(err)
	}
	if more != nil {
		more(log)
	}
	return log
}

func get(t *testing.T, address string) (int, string) {
	t.Helper()
	res, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	if cerr := res.Body.Close(); err != nil || cerr != nil {
		t.Fatal(err, cerr)
	}
	return res.StatusCode, string(body)
}

func getInto(t *testing.T, address string, into any) {
	t.Helper()
	status, body := get(t, address)
	if status != http.StatusOK {
		t.Fatalf("%s answered %d: %s", address, status, body)
	}
	if err := json.Unmarshal([]byte(body), into); err != nil {
		t.Fatalf("%s: %v\n%s", address, err, body)
	}
}

func TestSessionsAreListedAndReadOnly(t *testing.T) {
	// the signing key is looked for here, and must not be the developer's
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	sessions := t.TempDir()
	const older, newer = "20260101-090000-aaaaaa", "20260102-090000-bbbbbb"
	const diff = "diff --git a/main.go b/main.go\nnew file mode 100644\n--- /dev/null\n+++ b/main.go\n@@ -0,0 +1 @@\n+package main\n"
	old := recorded(t, sessions, older, "/home/ada/shop", func(log *events.Log) {
		if _, err := log.Append(events.TypeChangesCaptured, events.ChangesCaptured{InRepo: true, Files: 1, Added: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := log.Append(events.TypeSessionEnded, events.SessionEnded{}); err != nil {
			t.Fatal(err)
		}
	})
	if err := os.WriteFile(filepath.Join(sessions, older, changes.File), []byte(diff), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // so the two sessions start at different times
	live := recorded(t, sessions, newer, "/home/ada/site", nil)
	defer func() {
		if err := live.Close(); err != nil {
			t.Error(err)
		}
	}()
	// a folder that is not a session is passed over
	if err := os.MkdirAll(filepath.Join(sessions, "not a session"), 0o700); err != nil {
		t.Fatal(err)
	}

	s, err := Start(live, fstest.MapFS{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	s.ServeSessions(sessions, newer)
	s.Describe(Info{Version: "9.9.9", Reviewer: "a-model"})

	var info Info
	getInto(t, s.URL+"/api/info", &info)
	if info.Version != "9.9.9" || info.Reviewer != "a-model" {
		t.Errorf("info = %+v", info)
	}

	var list []SessionInfo
	getInto(t, s.URL+"/api/sessions", &list)
	if len(list) != 2 || list[0].Name != newer || !list[0].Current || list[0].Project != "site" ||
		list[1].Name != older || list[1].Current || list[1].Project != "shop" || list[1].Command != "agent" {
		t.Fatalf("sessions = %+v, want the live one first, then the older one", list)
	}

	var l ledger.Ledger
	getInto(t, s.URL+"/api/sessions/"+older+"/ledger", &l)
	if l.WorkDir != "/home/ada/shop" || l.Phase != ledger.PhaseFinished || l.Changes == nil || l.Changes.Files != 1 {
		t.Errorf("the older session's ledger = %+v", l)
	}
	getInto(t, s.URL+"/api/sessions/"+newer+"/ledger", &l)
	if l.WorkDir != "/home/ada/site" {
		t.Errorf("the live session's ledger by name = %+v", l)
	}

	var review changes.Review
	getInto(t, s.URL+"/api/sessions/"+older+"/changes", &review)
	if !review.Captured || len(review.Parts) != 1 || review.Parts[0].Path != "main.go" || review.Parts[0].Level != changes.Read {
		t.Fatalf("the older session's changes = %+v", review)
	}
	if status, text := get(t, s.URL+"/api/sessions/"+older+"/change?id="+review.Parts[0].ID); status != http.StatusOK || !strings.Contains(text, "+package main") {
		t.Errorf("the part's text: %d %q", status, text)
	}

	var entries []Entry
	getInto(t, s.URL+"/api/sessions/"+older+"/entries", &entries)
	if len(entries) != 3 || entries[0].Seq != 1 || entries[0].Type != events.TypeSessionStarted || entries[1].Prev != entries[0].Hash || len(entries[2].Hash) != 64 {
		t.Errorf("entries = %+v", entries)
	}

	var check RecordCheck
	getInto(t, s.URL+"/api/sessions/"+older+"/record", &check)
	if check.Verified || check.Entries != 3 || check.Problem == "" || check.BadEntry != 0 {
		t.Errorf("record of a session that was never signed = %+v, want its chain intact and the lack of a signature said", check)
	}

	// (".." never gets this far: the router resolves it to another address first)
	for _, bad := range []string{"not%20a%20session", "20260101-090000-zzzzzz", ".%2F" + older, "..%2F" + older, older + "%2F..%2F..%2Fetc"} {
		if status, body := get(t, s.URL+"/api/sessions/"+bad+"/ledger"); status != http.StatusNotFound {
			t.Errorf("%s answered %d (%s), want 404", bad, status, body)
		}
	}
}

// With no folder of sessions to answer for, the list is empty and nothing
// can be asked for by name.
func TestAServerWithNoSessionsFolderAnswersOnlyForItsOwn(t *testing.T) {
	live := recorded(t, t.TempDir(), "20260102-090000-bbbbbb", "/home/ada/site", nil)
	defer func() {
		if err := live.Close(); err != nil {
			t.Error(err)
		}
	}()
	s, err := Start(live, fstest.MapFS{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	var list []SessionInfo
	getInto(t, s.URL+"/api/sessions", &list)
	if len(list) != 0 {
		t.Errorf("sessions = %+v, want none", list)
	}
	if status, _ := get(t, s.URL+"/api/sessions/20260102-090000-bbbbbb/ledger"); status != http.StatusNotFound {
		t.Errorf("a session by name answered %d, want 404", status)
	}
}
