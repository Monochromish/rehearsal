package server

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"rehearsal/internal/investigate"
	"rehearsal/internal/record"
)

// UseInvestigator sets the local model that writes an account of each
// finding and answers questions. Without one, the findings stand alone.
func (s *Server) UseInvestigator(model investigate.Asker) {
	s.mu.Lock()
	s.asker = model
	s.mu.Unlock()
}

// unsignedYet is how a record with no signature beside it is described.
const unsignedYet = "does not hold: there is no signed checkpoint"

// investigation applies the rules to one recorded session, or to every
// session when name is empty.
func (s *Server) investigation(name string) (*investigate.Case, investigate.Asker, error) {
	s.mu.Lock()
	dir, asker, version, current := s.sessionsDir, s.asker, s.info.Version, s.current
	s.mu.Unlock()
	if dir == "" {
		return nil, nil, fmt.Errorf("this server answers for its own session only")
	}
	path := dir
	if name != "" {
		if !sessionName.MatchString(name) {
			return nil, nil, fmt.Errorf("%q is not the name of a session", name)
		}
		path = filepath.Join(dir, name)
	}
	// with no key of our own there is nothing to check a signature
	// against, and each record's custody says so
	var trusted []byte
	if keyDir, err := record.KeyDir(); err == nil {
		if key, err := record.OwnPublicKey(keyDir); err == nil {
			trusted = key
		}
	}
	recs, err := investigate.Open(path, trusted)
	if err != nil {
		return nil, nil, err
	}
	// The session this server is for is still open, and a record is signed
	// when its session ends. Until then there is no signature to have
	// failed, and its custody says that and not that one does not hold.
	for i := range recs {
		if c := &recs[i].Custody; c.Session == current && !c.Verified && strings.HasPrefix(c.Signature, unsignedYet) {
			c.Signature = "not checked yet: this session is still open, and its record is signed when it ends"
		}
	}
	return investigate.Investigate(recs, version), asker, nil
}

// handleInvestigate answers with the whole case: custody, findings, the
// raw entries they rest on, timelines, the overview and the links. With
// narrate=1 the local model's accounts are added, which takes a while.
func (s *Server) handleInvestigate(w http.ResponseWriter, r *http.Request) {
	c, asker, err := s.investigation(r.URL.Query().Get("session"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("narrate") == "1" && asker != nil {
		c.Narrate(r.Context(), asker)
	}
	writeJSON(w, c)
}

// handleAsk answers a question from the entries of one session or of all.
func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	// a page on another site cannot send JSON here without asking first
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		http.Error(w, "the question must be sent as application/json", http.StatusUnsupportedMediaType)
		return
	}
	var body struct {
		Session  string `json:"session"`
		Question string `json:"question"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || body.Question == "" {
		http.Error(w, "the request needs a question", http.StatusBadRequest)
		return
	}
	c, asker, err := s.investigation(body.Session)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if asker == nil {
		// a nil model inside the interface would not compare equal to nil
		writeJSON(w, c.Ask(r.Context(), nil, body.Question))
		return
	}
	writeJSON(w, c.Ask(r.Context(), asker, body.Question))
}
