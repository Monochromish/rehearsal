package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"rehearsal/internal/changes"
	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
	"rehearsal/internal/record"
	"rehearsal/internal/redact"
)

// Info is what the server says about the program it is part of.
type Info struct {
	Version string `json:"version"`
	// the local model that describes held actions, or "" when there is none
	Reviewer string `json:"reviewer"`
}

// SessionInfo is one recorded session, as a list of sessions shows it.
type SessionInfo struct {
	Name string `json:"name"`
	// the folder the agent ran in, and that folder's own name
	WorkDir   string    `json:"work_dir"`
	Project   string    `json:"project"`
	Command   string    `json:"command"`
	StartedAt time.Time `json:"started_at"`
	// the first words of what the person asked the agent, when the record
	// has them; a list can name the session by them
	Title string `json:"title,omitempty"`
	// the session this server is running for
	Current bool `json:"current"`
	Replay  bool `json:"replay,omitempty"`
}

// RecordCheck is whether a session's record holds up: its chain of hashes
// and, once the session has ended, the signature on its last entry.
type RecordCheck struct {
	Verified  bool   `json:"verified"`
	Entries   int64  `json:"entries"`
	FinalHash string `json:"final_hash"`
	// when the record was signed; absent for one that is not
	SignedAt *time.Time `json:"signed_at,omitempty"`
	// why it does not verify, and the first entry that does not match
	Problem  string `json:"problem,omitempty"`
	BadEntry int64  `json:"bad_entry,omitempty"`
}

// Entry is one line of a session's log as it was written and hashed.
type Entry struct {
	Seq  int64           `json:"seq"`
	Time string          `json:"time"`
	Type events.Type     `json:"type"`
	Data json.RawMessage `json:"data"`
	Prev string          `json:"prev"`
	Hash string          `json:"hash"`
}

// Describe sets what /api/info answers.
func (s *Server) Describe(info Info) {
	s.mu.Lock()
	s.info = info
	s.mu.Unlock()
}

// ServeSessions lets the server answer for every session recorded under
// dir, read-only, as well as for the live one, whose folder name is
// current.
func (s *Server) ServeSessions(dir, current string) {
	s.mu.Lock()
	s.sessionsDir, s.current = dir, current
	s.mu.Unlock()
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	info := s.info
	s.mu.Unlock()
	writeJSON(w, info)
}

var sessionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// opened is a session's log and folder, and how to let go of them.
type opened struct {
	dir   string
	log   *events.Log
	close func() error
}

// open finds a session by name. The live session is answered from the log
// already open; any other is opened for reading and must be closed.
func (s *Server) open(name string) (opened, error) {
	s.mu.Lock()
	dir, current := s.sessionsDir, s.current
	s.mu.Unlock()
	if dir == "" {
		return opened{}, errors.New("this server answers for its own session only")
	}
	if !sessionName.MatchString(name) {
		return opened{}, fmt.Errorf("%q is not the name of a session", name)
	}
	folder := filepath.Join(dir, name)
	if name == current {
		return opened{dir: folder, log: s.log, close: func() error { return nil }}, nil
	}
	log, err := events.OpenExisting(filepath.Join(folder, "events.db"))
	if errors.Is(err, fs.ErrNotExist) {
		return opened{}, fmt.Errorf("there is no session called %q", name)
	}
	if err != nil {
		return opened{}, err
	}
	return opened{dir: folder, log: log, close: log.Close}, nil
}

// with runs fn on a named session and reports a session that cannot be
// opened as not found.
func (s *Server) with(w http.ResponseWriter, r *http.Request, fn func(o opened) error) {
	o, err := s.open(r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	err = fn(o)
	if cerr := o.close(); err == nil {
		err = cerr
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleSessions lists the recorded sessions, newest first. One whose log
// cannot be read is left out: it has nothing to show.
func (s *Server) handleSessions(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	dir, current := s.sessionsDir, s.current
	s.mu.Unlock()
	out := []SessionInfo{}
	entries, err := os.ReadDir(dir)
	if err != nil && dir != "" && !errors.Is(err, fs.ErrNotExist) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !sessionName.MatchString(e.Name()) {
			continue
		}
		info, ok := s.describe(e.Name())
		if !ok {
			continue
		}
		info.Current = e.Name() == current
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b SessionInfo) int { return b.StartedAt.Compare(a.StartedAt) })
	writeJSON(w, out)
}

// describe reads how a session started. A finished session never changes,
// so what is read is kept.
func (s *Server) describe(name string) (SessionInfo, bool) {
	s.mu.Lock()
	known, ok := s.described[name]
	s.mu.Unlock()
	if ok {
		return known, true
	}
	o, err := s.open(name)
	if err != nil {
		return SessionInfo{}, false
	}
	l, err := ledger.FromLog(o.log)
	if cerr := o.close(); err != nil || cerr != nil || l.StartedAt.IsZero() {
		return SessionInfo{}, false
	}
	info := SessionInfo{Name: name, WorkDir: l.WorkDir, Project: filepath.Base(l.WorkDir), StartedAt: l.StartedAt, Replay: l.Replay, Title: l.Title}
	if len(l.Command) > 0 {
		info.Command = l.Command[0]
	}
	s.mu.Lock()
	if s.described == nil {
		s.described = map[string]SessionInfo{}
	}
	s.described[name] = info
	s.mu.Unlock()
	return info, true
}

func (s *Server) handleSessionLedger(w http.ResponseWriter, r *http.Request) {
	s.with(w, r, func(o opened) error {
		l, err := ledger.FromLog(o.log)
		if err != nil {
			return err
		}
		if home, herr := os.UserHomeDir(); herr == nil {
			l.Home = home
		}
		writeJSON(w, l)
		return nil
	})
}

func (s *Server) sessionReview(o opened) (changes.Review, error) {
	l, err := ledger.FromLog(o.log)
	if err != nil {
		return changes.Review{}, err
	}
	r, err := changes.Load(o.dir, l.Changes)
	r.Annotate(l.ChangeNotes, l.ChangesReading)
	return r, err
}

func (s *Server) handleSessionChanges(w http.ResponseWriter, r *http.Request) {
	s.with(w, r, func(o opened) error {
		review, err := s.sessionReview(o)
		if err != nil {
			return err
		}
		writeJSON(w, review)
		return nil
	})
}

func (s *Server) handleSessionChange(w http.ResponseWriter, r *http.Request) {
	s.with(w, r, func(o opened) error {
		review, err := s.sessionReview(o)
		if err != nil {
			return err
		}
		part, ok := review.Part(r.URL.Query().Get("id"))
		if !ok {
			http.Error(w, "this session has no such change", http.StatusNotFound)
			return nil
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, redact.Text(part.Text)) // fails only when the client has gone away
		return nil
	})
}

// handleSessionEntries returns a session's log, each entry as it was
// hashed, for showing the evidence behind what the page says.
func (s *Server) handleSessionEntries(w http.ResponseWriter, r *http.Request) {
	s.with(w, r, func(o opened) error {
		evs, err := o.log.Since(0)
		if err != nil {
			return err
		}
		out := make([]Entry, len(evs))
		for i, ev := range evs {
			out[i] = Entry{Seq: ev.Seq, Time: ev.Time.Format(time.RFC3339Nano), Type: ev.Type, Data: json.RawMessage(ev.Data), Prev: ev.Prev, Hash: ev.Hash}
		}
		writeJSON(w, out)
		return nil
	})
}

// handleSessionRecord checks a session's record: the chain, and the
// signature against this computer's own key.
func (s *Server) handleSessionRecord(w http.ResponseWriter, r *http.Request) {
	s.with(w, r, func(o opened) error {
		check, err := checkRecord(o)
		if err != nil {
			return err
		}
		s.mu.Lock()
		current := s.current
		s.mu.Unlock()
		// The session being served is still open and is signed when it
		// ends: its record lacks a signature, and has not failed a check.
		if r.PathValue("name") == current && !check.Verified && check.BadEntry == 0 && strings.HasPrefix(check.Problem, "there is no signed checkpoint") {
			check.Problem = fmt.Sprintf("Each of the %d entries so far matches its hash and follows the one before. This session is still open, and its record is signed when it ends.", check.Entries)
		}
		writeJSON(w, check)
		return nil
	})
}

func checkRecord(o opened) (RecordCheck, error) {
	keyDir, err := record.KeyDir()
	if err != nil {
		return RecordCheck{}, err
	}
	trusted, err := record.OwnPublicKey(keyDir)
	if err != nil {
		// with no key of our own there is nothing to check a signature against
		chain, cerr := o.log.Check()
		if cerr != nil {
			return RecordCheck{}, cerr
		}
		problem := chain.Problem
		if chain.OK() {
			problem = "the chain of hashes holds, but there is no signing key on this computer to check a signature against"
		}
		return RecordCheck{Entries: chain.Entries, FinalHash: chain.Head, Problem: problem, BadEntry: chain.BadEntry}, nil
	}
	v, err := record.Verify(o.log, o.dir, trusted)
	if err != nil {
		return RecordCheck{}, err
	}
	check := RecordCheck{Verified: v.OK(), Entries: v.Entries, FinalHash: v.FinalHash, Problem: strings.TrimSpace(v.Problem), BadEntry: v.BadEntry}
	if !v.SignedAt.IsZero() {
		at := v.SignedAt
		check.SignedAt = &at
	}
	return check, nil
}
