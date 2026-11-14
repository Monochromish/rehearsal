// Package hold keeps held actions: the event that describes each one, and
// the exact data needed to replay it later.
package hold

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"rehearsal/internal/events"
)

// Payload is everything needed to perform a held action for real. It can
// contain credentials, so it lives only in the session's private folder
// and is deleted once the session is applied or discarded.
type Payload struct {
	// stand-in commands
	Program    string   `json:"program,omitempty"`
	Args       []string `json:"args,omitempty"`
	WorkDir    string   `json:"work_dir,omitempty"`
	Stdin      []byte   `json:"stdin,omitempty"`
	GitGlobals []string `json:"git_globals,omitempty"` // options before "push", such as -C

	// held requests
	Method string      `json:"method,omitempty"`
	URL    string      `json:"url,omitempty"`
	Header http.Header `json:"header,omitempty"`
	Body   []byte      `json:"body,omitempty"`
	// identifiers found in the request, written to the ledger if it is sent
	Seen []events.IdentifierSeen `json:"seen,omitempty"`
}

type Store struct {
	dir string
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create the folder for held actions: %w", err)
	}
	// MkdirAll leaves an existing folder's permissions alone
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("make the folder for held actions private: %w", err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) Save(id string, p Payload) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode held action %s: %w", id, err)
	}
	// O_EXCL: an ID is never reused, so an existing file means a bug
	f, err := os.OpenFile(s.path(id), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("store held action %s: %w", id, err)
	}
	if _, err := f.Write(data); err != nil {
		return errors.Join(fmt.Errorf("store held action %s: %w", id, err), f.Close())
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("store held action %s: %w", id, err)
	}
	return nil
}

func (s *Store) Load(id string) (Payload, error) {
	var p Payload
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return p, fmt.Errorf("read held action %s: %w", id, err)
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("held action %s is unreadable: %w", id, err)
	}
	return p, nil
}

func (s *Store) Remove(id string) error {
	if err := os.Remove(s.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete held action %s: %w", id, err)
	}
	return nil
}

// RemoveAll deletes every stored payload and the folder itself.
func (s *Store) RemoveAll() error {
	if err := os.RemoveAll(s.dir); err != nil {
		return fmt.Errorf("delete the held actions: %w", err)
	}
	return nil
}
