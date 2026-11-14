// Package proxy runs the intercepting proxy and records what passes
// through it into the event log.
package proxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	mitm "github.com/lqqyt2423/go-mitmproxy/proxy"

	"rehearsal/handlers"
	"rehearsal/internal/events"
	"rehearsal/internal/hold"
	"rehearsal/internal/ident"
)

// Recorder writes one event per request and one per identifier found in
// it. Header values, query strings and bodies are searched, never stored.
//
// A request that cannot be recorded is refused, and so is every request
// after it: nothing may leave without appearing in the ledger.
type Recorder struct {
	mitm.BaseAddon
	log       *events.Log
	matcher   *ident.Matcher
	providers map[string]bool
	holding   *Holding

	mu       sync.Mutex
	recorded map[string]bool // flows whose request event is already written
	err      error           // first failed write to the log
}

// Holding makes a Recorder hold requests that change something instead of
// sending them.
type Holding struct {
	Holder   *hold.Holder
	Handlers *handlers.Set
	// hosts whose writes go straight through, such as the agent's own
	// telemetry, which it may misbehave without
	Allow []string
}

var writeMethods = []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

func NewRecorder(log *events.Log, matcher *ident.Matcher, providerHosts []string) *Recorder {
	providers := map[string]bool{}
	for _, h := range providerHosts {
		providers[strings.ToLower(h)] = true
	}
	return &Recorder{log: log, matcher: matcher, providers: providers, recorded: map[string]bool{}}
}

// HoldWrites must be called before the proxy starts.
func (r *Recorder) HoldWrites(h Holding) {
	for i, host := range h.Allow {
		h.Allow[i] = strings.ToLower(host)
	}
	r.holding = &h
}

// Err is the first failure to write to the event log, if any.
func (r *Recorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// WebSocket upgrades never reach Request, so they are recorded here.
func (r *Recorder) Requestheaders(f *mitm.Flow) {
	h := f.Request.Header
	if strings.EqualFold(h.Get("Connection"), "Upgrade") && strings.EqualFold(h.Get("Upgrade"), "websocket") {
		if err := r.record(f, r.scan(f, nil), true); err != nil {
			refuse(f, err)
		}
	}
}

func (r *Recorder) Request(f *mitm.Flow) {
	body, err := f.Request.DecodedBody()
	if err != nil {
		body = f.Request.Body // unknown compression: search the raw bytes
	}
	seen := r.scan(f, body)
	if r.holds(f.Request) {
		r.hold(f, seen)
		return
	}
	if err := r.record(f, seen, true); err != nil {
		refuse(f, err)
		return
	}
	// only a request that goes on upstream reaches StreamRequestModifier
	r.mu.Lock()
	r.recorded[f.Id.String()] = true
	r.mu.Unlock()
}

// StreamRequestModifier runs for every request, after Request. A request
// that skipped Request had a body too large to hold in memory, so it is
// dealt with here, its body unchecked.
func (r *Recorder) StreamRequestModifier(f *mitm.Flow, in io.Reader) io.Reader {
	id := f.Id.String()
	r.mu.Lock()
	done := r.recorded[id]
	delete(r.recorded, id)
	r.mu.Unlock()
	if done {
		return in
	}
	// Returning a reader that fails makes the upstream request fail, which
	// is the only way left to stop it at this point.
	if r.holds(f.Request) {
		return failingReader{fmt.Errorf("rehearse cannot hold a request body over %d MB, so the request was not sent", maxBodyInMemory>>20)}
	}
	if err := r.record(f, r.scan(f, nil), false); err != nil {
		return failingReader{err}
	}
	return in
}

// The status is recorded when the headers arrive, since a streamed
// response may not finish for minutes.
func (r *Recorder) Responseheaders(f *mitm.Flow) {
	r.append(events.TypeResponse, events.Response{RequestID: f.Id.String(), Status: f.Response.StatusCode})
}

func (r *Recorder) RequestError(f *mitm.Flow, _ error) {
	r.append(events.TypeResponse, events.Response{RequestID: f.Id.String(), Failed: true})
}

func (r *Recorder) holds(req *mitm.Request) bool {
	if r.holding == nil || !slices.Contains(writeMethods, req.Method) {
		return false
	}
	host := strings.ToLower(req.URL.Hostname())
	return !r.providers[host] && !slices.Contains(r.holding.Allow, host)
}

// hold keeps the request back and gives the agent a stand-in answer. If
// the request cannot be kept it is refused, never sent.
func (r *Recorder) hold(f *mitm.Flow, seen []events.IdentifierSeen) {
	if err := r.Err(); err != nil {
		refuse(f, err)
		return
	}
	req := f.Request
	answer := r.holding.Handlers.Answer(req.Method, req.URL.Hostname(), req.URL.Path)
	ids := []string{}
	for _, s := range seen {
		if !slices.Contains(ids, s.IdentifierID) {
			ids = append(ids, s.IdentifierID)
		}
	}
	host := displayHost(req.URL)
	_, err := r.holding.Holder.Hold(events.ActionHeld{
		Kind:            events.ActionHTTP,
		RequestID:       f.Id.String(),
		Method:          req.Method,
		Host:            host,
		Path:            req.URL.Path,
		RequestBytes:    int64(len(req.Body)),
		ContentType:     req.Header.Get("Content-Type"),
		Destination:     host,
		IdentifierIDs:   ids,
		Deletes:         req.Method == http.MethodDelete,
		NeedsRealAnswer: answer.NeedsRealAnswer,
	}, hold.Payload{
		Method: req.Method,
		URL:    req.URL.String(),
		Header: req.Header.Clone(),
		Body:   req.Body,
		Seen:   seen,
	})
	if err != nil {
		refuse(f, err)
		return
	}
	header := http.Header{}
	if answer.ContentType != "" {
		header.Set("Content-Type", answer.ContentType)
	}
	f.Response = &mitm.Response{StatusCode: answer.Status, Header: header, Body: answer.Body}
}

// scan finds the watched identifiers in the request's address, headers
// and body.
func (r *Recorder) scan(f *mitm.Flow, body []byte) []events.IdentifierSeen {
	req := f.Request
	host := displayHost(req.URL)
	var seen []events.IdentifierSeen
	look := func(where events.Where, text []byte) {
		for _, m := range r.matcher.Find(text) {
			dup := slices.ContainsFunc(seen, func(s events.IdentifierSeen) bool {
				return s.IdentifierID == m.IdentifierID && s.Where == where
			})
			if !dup {
				seen = append(seen, events.IdentifierSeen{
					RequestID: f.Id.String(), Host: host, IdentifierID: m.IdentifierID, Where: where, Form: m.Form,
				})
			}
		}
	}
	look(events.WhereAddress, []byte(req.URL.String()))
	for _, values := range req.Header {
		for _, v := range values {
			look(events.WhereHeader, []byte(v))
		}
	}
	look(events.WhereBody, body)
	return seen
}

func (r *Recorder) record(f *mitm.Flow, seen []events.IdentifierSeen, bodyChecked bool) error {
	if err := r.Err(); err != nil {
		return err
	}
	req := f.Request
	size := int64(len(req.Body))
	if !bodyChecked {
		size = req.Raw().ContentLength // -1 when the client did not say
	}
	r.append(events.TypeRequest, events.Request{
		ID:           f.Id.String(),
		Host:         displayHost(req.URL),
		Method:       req.Method,
		Path:         req.URL.Path,
		RequestBytes: size,
		Category:     r.category(req.URL.Hostname()),
		BodyChecked:  bodyChecked,
	})
	for _, s := range seen {
		r.append(events.TypeIdentifierSeen, s)
	}
	return r.Err()
}

func (r *Recorder) append(t events.Type, payload any) {
	if _, err := r.log.Append(t, payload); err != nil {
		r.mu.Lock()
		if r.err == nil {
			r.err = err
		}
		r.mu.Unlock()
	}
}

func (r *Recorder) category(hostname string) events.Category {
	if r.providers[strings.ToLower(hostname)] {
		return events.CategoryProvider
	}
	return events.CategoryOther
}

func refuse(f *mitm.Flow, err error) {
	f.Response = &mitm.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
		Body:       fmt.Appendf(nil, "rehearse could not record this request, so it was not sent: %v\n", err),
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// displayHost keeps the port only when it is not the usual one.
func displayHost(u *url.URL) string {
	host, port := u.Hostname(), u.Port()
	if port == "" || (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		return host
	}
	return net.JoinHostPort(host, port)
}
