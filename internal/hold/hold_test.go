package hold

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"rehearsal/internal/events"
	"rehearsal/internal/ident"
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

// A username in the path of a local remote is where the remote is, not
// something the push sends. The same name in a commit message is sent.
func TestALocalPathIsNotSomethingSent(t *testing.T) {
	user := events.Identifier{ID: "user", Kind: events.KindReal, Label: "Your computer username", Value: "adal"}
	m := Masker{Matcher: ident.NewMatcher([]events.Identifier{user})}
	tests := []struct {
		name string
		c    Command
		want []string
	}{
		{"a remote that is a folder on this computer",
			Command{Args: []string{"push", "origin", "main"}, Destination: "/home/adal/remote.git", Remote: "origin"}, []string{}},
		{"the same folder given on the command line",
			Command{Args: []string{"push", "/home/adal/remote.git", "main"}, Destination: "/home/adal/remote.git"}, []string{}},
		{"a file address", Command{Args: []string{"push", "origin"}, Destination: "file:///home/adal/remote.git", Remote: "origin"}, []string{}},
		{"a remote on another machine", Command{Args: []string{"push", "origin"}, Destination: "https://git.example.test/adal/shop.git", Remote: "origin"}, []string{"user"}},
		{"a commit message", Command{Args: []string{"push", "origin"}, Destination: "/home/adal/remote.git", Commits: []string{"Fix adal's typo"}}, []string{"user"}},
		{"a file name", Command{Args: []string{"push", "origin"}, Destination: "/srv/remote.git", Files: []string{"notes/adal.md"}}, []string{"user"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, err := events.Open(filepath.Join(t.TempDir(), "events.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := log.Close(); err != nil {
					t.Error(err)
				}
			}()
			store, err := NewStore(filepath.Join(t.TempDir(), "held"))
			if err != nil {
				t.Fatal(err)
			}
			tt.c.Kind, tt.c.Program, tt.c.WorkDir = events.ActionGitPush, "git", "/home/adal/project"
			if _, err := NewHolder(log, store).HoldCommand(tt.c, m); err != nil {
				t.Fatal(err)
			}
			evs, err := log.Since(0)
			if err != nil {
				t.Fatal(err)
			}
			var held events.ActionHeld
			if err := json.Unmarshal(evs[len(evs)-1].Data, &held); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(held.IdentifierIDs, tt.want) {
				t.Errorf("carries %v, want %v", held.IdentifierIDs, tt.want)
			}
			if held.Remote != tt.c.Remote {
				t.Errorf("remote = %q, want %q", held.Remote, tt.c.Remote)
			}
		})
	}
}

// A secret in the first line of a commit is recorded as found there, and
// its name is stored in its place. A planted value and a detail of the
// user's own are not secrets and stay as they were written.
func TestASecretInACommitSubjectIsNamedNotStored(t *testing.T) {
	const secret = "tok-4f9a7c21e8b05d36"
	ids := []events.Identifier{
		{ID: "user", Kind: events.KindReal, Label: "Your computer username", Value: "adal"},
		{ID: "canary-word", Kind: events.KindCanary, Label: "Planted word", Value: "plantedword77"},
		{ID: "env-1", Kind: events.KindSecret, Label: "Value of API_KEY from .env", Name: "API_KEY", Hash: ident.SaltedHash("salt", secret)},
	}
	m := Masker{Matcher: ident.NewMatcher(ids, ident.Secret{ID: "env-1", Value: secret, Salt: "salt"}), Keep: []string{"plantedword77"}}
	log, err := events.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := log.Close(); err != nil {
			t.Error(err)
		}
	}()
	store, err := NewStore(filepath.Join(t.TempDir(), "held"))
	if err != nil {
		t.Fatal(err)
	}
	c := Command{
		Kind: events.ActionGitPush, Program: "git", WorkDir: "/home/adal/project", Args: []string{"push", "origin", "main"},
		Destination: "git.example.test:shop.git",
		Commits:     []string{"Set the deploy key to " + secret, "Fix adal's typo", "Note plantedword77 in the readme"},
	}
	if _, err := NewHolder(log, store).HoldCommand(c, m); err != nil {
		t.Fatal(err)
	}
	evs, err := log.Since(0)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(evs[len(evs)-1].Data)
	if strings.Contains(raw, secret) {
		t.Fatalf("the secret is in the log: %s", raw)
	}
	var held events.ActionHeld
	if err := json.Unmarshal(evs[len(evs)-1].Data, &held); err != nil {
		t.Fatal(err)
	}
	want := []string{"Set the deploy key to [API_KEY]", "Fix adal's typo", "Note plantedword77 in the readme"}
	if !reflect.DeepEqual(held.Commits, want) {
		t.Errorf("commits = %q, want %q", held.Commits, want)
	}
	found := map[string]events.Where{}
	for _, s := range held.Sightings {
		found[s.IdentifierID] = s.Where
		if s.Form != events.FormPlain {
			t.Errorf("%s found as %s", s.IdentifierID, s.Form)
		}
	}
	if len(found) != 3 || found["env-1"] != events.WhereCommit || found["user"] != events.WhereCommit || found["canary-word"] != events.WhereCommit {
		t.Errorf("sightings = %+v, want all three in a commit message", held.Sightings)
	}
}
