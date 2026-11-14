// Package proxy runs the intercepting proxy and records what passes
// through it into the event log.
package proxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	mitm "github.com/lqqyt2423/go-mitmproxy/proxy"

	"rehearsal/internal/events"
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

	mu       sync.Mutex
	recorded map[string]bool // flows whose request event is already written
	err      error           // first failed write to the log
}

func NewRecorder(log *events.Log, matcher *ident.Matcher, providerHosts []string) *Recorder {
	providers := map[string]bool{}
	for _, h := range providerHosts {
		providers[strings.ToLower(h)] = true
	}
	return &Recorder{log: log, matcher: matcher, providers: providers, recorded: map[string]bool{}}
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
		if err := r.record(f, nil, true); err != nil {
			refuse(f, err)
		}
	}
}

func (r *Recorder) Request(f *mitm.Flow) {
	body, err := f.Request.DecodedBody()
	if err != nil {
		body = f.Request.Body // unknown compression: search the raw bytes
	}
	if err := r.record(f, body, true); err != nil {
		refuse(f, err)
		return
	}
	r.mu.Lock()
	r.recorded[f.Id.String()] = true
	r.mu.Unlock()
}

// StreamRequestModifier runs for every request, after Request. A request
// that skipped Request had a body too large to hold in memory, so it is
// recorded here with its body unchecked.
func (r *Recorder) StreamRequestModifier(f *mitm.Flow, in io.Reader) io.Reader {
	id := f.Id.String()
	r.mu.Lock()
	done := r.recorded[id]
	delete(r.recorded, id)
	r.mu.Unlock()
	if done {
		return in
	}
	if err := r.record(f, nil, false); err != nil {
		return failingReader{err} // the upstream request fails instead of going out unrecorded
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

func (r *Recorder) record(f *mitm.Flow, body []byte, bodyChecked bool) error {
	if err := r.Err(); err != nil {
		return err
	}
	req := f.Request
	host := displayHost(req.URL)
	size := int64(len(req.Body))
	if !bodyChecked {
		size = req.Raw().ContentLength // -1 when the client did not say
	}
	r.append(events.TypeRequest, events.Request{
		ID:           f.Id.String(),
		Host:         host,
		Method:       req.Method,
		Path:         req.URL.Path,
		RequestBytes: size,
		Category:     r.category(req.URL.Hostname()),
		BodyChecked:  bodyChecked,
	})

	seen := map[string]bool{}
	report := func(where events.Where, text []byte) {
		for _, m := range r.matcher.Find(text) {
			key := m.IdentifierID + "/" + string(where)
			if seen[key] {
				continue
			}
			seen[key] = true
			r.append(events.TypeIdentifierSeen, events.IdentifierSeen{
				RequestID: f.Id.String(), Host: host, IdentifierID: m.IdentifierID, Where: where, Form: m.Form,
			})
		}
	}
	report(events.WhereAddress, []byte(req.URL.String()))
	for _, values := range req.Header {
		for _, v := range values {
			report(events.WhereHeader, []byte(v))
		}
	}
	report(events.WhereBody, body)
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
