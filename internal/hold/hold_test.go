package hold

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"rehearsal/internal/events"
)

func TestStoreKeepsPayloadsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "held")
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Payload{
		Method: http.MethodPost,
		URL:    "https://api.example.test/upload?token=abc",
		Header: http.Header{"Authorization": {"Bearer secret"}},
		Body:   []byte{0, 1, 2, 'x'},
	}
	if err := store.Save("a1", want); err != nil {
		t.Fatal(err)
	}

	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("held folder mode = %v (err %v), want 0700", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(filepath.Join(dir, "a1.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("payload file mode = %v (err %v), want 0600", info.Mode().Perm(), err)
	}

	got, err := store.Load("a1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load = %+v, want %+v", got, want)
	}

	if _, err := store.Load("a2"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load of an unknown action: err = %v, want not-exist", err)
	}
	if err := store.RemoveAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("held folder still exists after RemoveAll (stat err %v)", err)
	}
}

func TestStoreTightensAnExistingFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "held")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("existing folder mode = %v (err %v), want 0700", info.Mode().Perm(), err)
	}
}

func TestHolder(t *testing.T) {
	log, err := events.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(filepath.Join(t.TempDir(), "held"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewHolder(log, store)

	first, err := h.Hold(events.ActionHeld{Kind: events.ActionGitPush}, Payload{Program: "git", Args: []string{"push"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.Hold(events.ActionHeld{Kind: events.ActionHTTP}, Payload{Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	if first != "a1" || second != "a2" || h.Count() != 2 {
		t.Errorf("ids = %q, %q and count %d, want a1, a2 and 2", first, second, h.Count())
	}
	evs, err := log.Since(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Type != events.TypeActionHeld {
		t.Fatalf("log has %d events, want 2 action_held", len(evs))
	}

	// an action whose event cannot be written must leave no payload behind
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Hold(events.ActionHeld{Kind: events.ActionHTTP}, Payload{Method: "DELETE"}); err == nil {
		t.Fatal("Hold succeeded with a closed log")
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("held folder has %d files, want the 2 from before the failure", len(entries))
	}

	h.Close()
	if _, err := h.Hold(events.ActionHeld{Kind: events.ActionHTTP}, Payload{}); err == nil || !strings.Contains(err.Error(), "session is over") {
		t.Errorf("Hold after Close: err = %v, want the session-is-over error", err)
	}
}

func TestStoreNeverOverwrites(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "held"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("a1", Payload{Method: "POST"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("a1", Payload{Method: "DELETE"}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second Save of a1: err = %v, want already-exists", err)
	}
	if got, err := store.Load("a1"); err != nil || got.Method != "POST" {
		t.Errorf("a1 after the refused overwrite = %+v (err %v), want the original POST", got, err)
	}
}
