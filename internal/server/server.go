// Package server serves the page and the local API it reads from:
// GET /api/ledger and GET /api/events.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
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
}

// Start serves on 127.0.0.1. Port 0 picks any free port.
func Start(log *events.Log, ui fs.FS, port int) (*Server, error) {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("listen for the page on port %d: %w", port, err)
	}
	s := &Server{
		URL:    fmt.Sprintf("http://localhost:%d", l.Addr().(*net.TCPAddr).Port),
		log:    log,
		done:   make(chan struct{}),
		served: make(chan error, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ledger", s.handleLedger)
	mux.HandleFunc("GET /api/events", s.handleEvents)
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

func (s *Server) handleLedger(w http.ResponseWriter, _ *http.Request) {
	evs, err := s.log.Since(0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	l, err := ledger.Build(evs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := json.Marshal(l)
	if err != nil {
		http.Error(w, fmt.Sprintf("encode the ledger: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data) // fails only when the browser has gone away
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
