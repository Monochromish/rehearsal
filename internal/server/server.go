// Package server serves the page and the local API behind it:
// GET /api/ledger, GET /api/events, GET /api/held and POST /api/apply.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"slices"
	"strconv"
	"time"

	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
)

type Server struct {
	URL    string
	log    *events.Log
	http   *http.Server
	done   chan struct{}
	served chan error

	origins   []string
	decisions chan Decision
}

// Decision is what the user chose on the page for the held actions.
type Decision struct {
	Discard bool
	// IDs of the actions to perform; every other held action is refused
	Approve []string
}

// Decisions delivers the user's decision. There is at most one.
func (s *Server) Decisions() <-chan Decision {
	return s.decisions
}

// Start serves on 127.0.0.1. Port 0 picks any free port.
func Start(log *events.Log, ui fs.FS, port int) (*Server, error) {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("listen for the page on port %d: %w", port, err)
	}
	port = l.Addr().(*net.TCPAddr).Port
	s := &Server{
		URL:       fmt.Sprintf("http://localhost:%d", port),
		log:       log,
		done:      make(chan struct{}),
		served:    make(chan error, 1),
		origins:   []string{fmt.Sprintf("http://localhost:%d", port), fmt.Sprintf("http://127.0.0.1:%d", port)},
		decisions: make(chan Decision, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ledger", s.handleLedger)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/held", s.handleHeld)
	mux.HandleFunc("POST /api/apply", s.handleApply)
	if _, err := fs.Stat(ui, "index.html"); err == nil {
		mux.Handle("GET /", http.FileServerFS(ui))
	} else {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "The page was not built into this binary. Build with `sh build.sh`, or read /api/ledger.", http.StatusNotFound)
		})
	}
	s.http = &http.Server{Handler: localOnly(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() { s.served <- s.http.Serve(l) }()
	return s, nil
}

// Shutdown ends open event streams and stops the server. It also reports
// a server that had already died during the session.
func (s *Server) Shutdown(ctx context.Context) error {
	close(s.done)
	err := s.http.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		// Browsers open connections ahead of need and may never use them.
		// Shutdown waits on those as if a request were coming, so once the
		// time allowed for real requests is up, they are closed.
		err = s.http.Close()
	}
	if served := <-s.served; !errors.Is(served, http.ErrServerClosed) {
		err = errors.Join(err, fmt.Errorf("page server stopped early: %w", served))
	}
	return err
}

// localOnly rejects requests that were not addressed to this machine by
// name, so a web page elsewhere cannot reach the API through a DNS trick.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host // no port in the header
		}
		if host != "localhost" && host != "127.0.0.1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ledger() (ledger.Ledger, error) {
	return ledger.FromLog(s.log)
}

func writeJSON(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, fmt.Sprintf("encode the reply: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data) // fails only when the browser has gone away
}

func (s *Server) handleLedger(w http.ResponseWriter, _ *http.Request) {
	l, err := s.ledger()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, l)
}

// held is the part of the ledger the decision screen needs.
type held struct {
	Phase   ledger.Phase    `json:"phase"`
	Actions []ledger.Action `json:"actions"`
}

func (s *Server) handleHeld(w http.ResponseWriter, _ *http.Request) {
	l, err := s.ledger()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, held{Phase: l.Phase, Actions: l.Actions})
}

// handleApply takes the user's decision. It makes real things happen, so
// it only accepts a JSON request that this page itself sent: a form or
// script on another site cannot set that content type or this origin
// without the browser asking us first, and we never say yes.
func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	if !slices.Contains(s.origins, r.Header.Get("Origin")) {
		http.Error(w, "this request did not come from the Rehearsal page", http.StatusForbidden)
		return
	}
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		http.Error(w, "the decision must be sent as application/json", http.StatusUnsupportedMediaType)
		return
	}
	var body struct {
		Decision string   `json:"decision"`
		Approve  []string `json:"approve"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "unreadable decision: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Decision != "apply" && body.Decision != "discard" {
		http.Error(w, `"decision" must be "apply" or "discard"`, http.StatusBadRequest)
		return
	}
	if body.Decision == "discard" && len(body.Approve) > 0 {
		http.Error(w, "a discard cannot approve anything", http.StatusBadRequest)
		return
	}

	l, err := s.ledger()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if l.Phase != ledger.PhaseDeciding {
		http.Error(w, fmt.Sprintf("nothing is waiting for a decision (the session is %s)", l.Phase), http.StatusConflict)
		return
	}
	for i, id := range body.Approve {
		known := slices.ContainsFunc(l.Actions, func(a ledger.Action) bool { return a.ID == id })
		if !known || slices.Contains(body.Approve[:i], id) {
			http.Error(w, fmt.Sprintf("%q is not a held action, or is listed twice", id), http.StatusBadRequest)
			return
		}
	}
	select {
	case s.decisions <- Decision{Discard: body.Decision == "discard", Approve: body.Approve}:
		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "a decision has already been made", http.StatusConflict)
	}
}

// handleEvents streams the log as server-sent events: everything after
// the client's Last-Event-ID first, then new events as they are appended.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	var last int64
	if h := r.Header.Get("Last-Event-ID"); h != "" {
		n, err := strconv.ParseInt(h, 10, 64)
		if err != nil {
			http.Error(w, "Last-Event-ID must be a number", http.StatusBadRequest)
			return
		}
		last = n
	}

	live, cancel := s.log.Subscribe() // before reading the past, so nothing is missed
	defer cancel()
	past, err := s.log.Since(last)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	// A failed write below means the browser has gone away, which ends the
	// stream; there is nobody left to tell.
	send := func(ev events.Event) error {
		if ev.Seq <= last {
			return nil
		}
		last = ev.Seq
		data, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.Seq, data)
		return err
	}
	for _, ev := range past {
		if send(ev) != nil {
			return
		}
	}
	flusher.Flush()

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case ev, open := <-live:
			if !open {
				return // fell behind; the browser reconnects and catches up
			}
			if send(ev) != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		}
	}
}
