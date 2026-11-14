// Package proxy runs the intercepting proxy and records what passes
// through it into the event log.
package proxy

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	mitm "github.com/lqqyt2423/go-mitmproxy/proxy"

	"rehearsal/handlers"
	"rehearsal/internal/events"
	"rehearsal/internal/hold"
	"rehearsal/internal/ident"
	"rehearsal/internal/observe"
	"rehearsal/internal/redact"
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
	*memory

	mu       sync.Mutex
	recorded map[string]bool // flows whose request event is already written
	err      error           // first failed write to the log
}

// memory is what a session has learned from its traffic so far. The
// proxies of one session share it.
type memory struct {
	salt    string
	outside *outside

	seen sync.Mutex
	// provider host -> hash of the first credential used there
	credentials map[string][32]byte
	// set when a login refresh has just succeeded: the next credential
	// seen is the same account under a new token
	refreshed bool
	// tool calls and results already written to the log, by call ID
	calls   map[string]bool
	results map[string]bool
	// what the model said in its latest reply, kept in memory only
	finalText string
	// hosts the agent program talks to for itself; set before the proxy starts
	own map[string]bool
}

// the most of a tool call's command kept in the log
const maxCallText = 300

// the most of a streamed reply kept in memory to read token counts from
const maxReplyCopy = 8 << 20

// Holding makes a Recorder hold requests instead of sending them.
type Holding struct {
	Holder   *hold.Holder
	Handlers *handlers.Set
	// Writes holds every request that changes something. Without it only
	// requests carrying a watched value are held, which is how the proxy
	// for read-only commands is set up.
	Writes bool
	// hosts whose writes go straight through, such as the agent's own
	// telemetry, which it may misbehave without
	Allow []string
	// planted values, which are not secrets even when shaped like one
	Canaries []string
	// ProviderDoor holds a request to the model provider that uses a
	// different credential from the first one seen, or uploads a file
	ProviderDoor bool
}

var (
	writeMethods = []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	// headers a client uses to prove who it is to the server it is talking to
	credentialHeaders = []string{"Authorization", "Proxy-Authorization", "Cookie", "X-Api-Key", "Api-Key", "X-Auth-Token", "Private-Token"}
	// where the model providers take file uploads
	uploadPaths = []string{"/v1/files", "/v1/uploads"}
)

func NewRecorder(log *events.Log, matcher *ident.Matcher, providerHosts []string) *Recorder {
	providers := map[string]bool{}
	for _, h := range providerHosts {
		providers[strings.ToLower(h)] = true
	}
	salt := rand.Text()
	return &Recorder{
		log: log, matcher: matcher, providers: providers, recorded: map[string]bool{},
		memory: &memory{
			salt: salt, outside: newOutside(salt), credentials: map[string][32]byte{},
			calls: map[string]bool{}, results: map[string]bool{},
		},
	}
}

// OwnHosts names the hosts the agent program talks to for itself and not
// at the model's asking, such as its telemetry and its update check. What
// they send back is still read for addresses and hidden text, but is
// marked as not being something the agent fetched. It must be called
// before the proxy starts.
func (r *Recorder) OwnHosts(hosts []string) {
	r.own = map[string]bool{}
	for _, h := range hosts {
		r.own[strings.ToLower(h)] = true
	}
}

// FinalText is what the model said in its latest reply that could be
// read, or "" if none could.
func (r *Recorder) FinalText() string {
	r.seen.Lock()
	defer r.seen.Unlock()
	return r.finalText
}

// Sibling makes a second recorder for the same session: same log, same
// watched values, and the same memory of what the session has seen.
func (r *Recorder) Sibling() *Recorder {
	return &Recorder{log: r.log, matcher: r.matcher, providers: r.providers, recorded: map[string]bool{}, memory: r.memory}
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
		if err := r.record(f, r.scan(f, nil), true, nil); err != nil {
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
	provider := r.providers[strings.ToLower(f.Request.URL.Hostname())]
	why, held := r.holds(f.Request, seen)
	// A policy may hold or refuse what the built-in rules let through, and
	// let through some of what they hold. It has no say over ordinary
	// traffic to the model provider.
	if held || (r.holding != nil && !provider && r.holding.Holder.HasRules()) {
		if r.hold(f, seen, why, held) {
			return
		}
	}
	if provider {
		r.readConversation(f.Request.URL.Path, body)
	}
	if err := r.record(f, seen, true, body); err != nil {
		refuse(f, err)
		return
	}
	// only a request that goes on upstream reaches StreamRequestModifier
	r.mu.Lock()
	r.recorded[f.Id.String()] = true
	r.mu.Unlock()
}

// readConversation writes down the tool calls and results in a request
// to the provider that have not been written down already. Each request
// carries the whole conversation so far, so most of it has been.
func (r *Recorder) readConversation(path string, body []byte) {
	format := observe.For(path)
	if format == nil {
		return
	}
	calls, results := format.Calls(body)
	r.seen.Lock()
	var newCalls []observe.Call
	var newResults []observe.Result
	for _, c := range calls {
		if !r.calls[c.ID] {
			r.calls[c.ID] = true
			newCalls = append(newCalls, c)
		}
	}
	for _, res := range results {
		if !r.results[res.CallID] {
			r.results[res.CallID] = true
			newResults = append(newResults, res)
		}
	}
	r.seen.Unlock()
	for _, c := range newCalls {
		text := redact.Text(c.Text)
		if runes := []rune(text); len(runes) > maxCallText {
			text = string(runes[:maxCallText]) + "..."
		}
		r.append(events.TypeAgentAction, events.AgentAction{ID: c.ID, Tool: c.Tool, Kind: string(c.Kind), Text: text})
	}
	for _, res := range newResults {
		r.append(events.TypeAgentResult, events.AgentResult{ID: res.CallID, Failed: res.Failed})
	}
}

// readReply takes token counts and the model's words from a reply by the
// provider. It works on a copy, after the reply has gone to the agent.
func (r *Recorder) readReply(f *mitm.Flow, reply []byte, streamed bool) {
	format := observe.For(f.Request.URL.Path)
	if format == nil || f.Response.StatusCode/100 != 2 {
		return
	}
	request, err := f.Request.DecodedBody()
	if err != nil {
		request = nil // the model's name is then not known for this reply
	}
	if u := format.Usage(reply, streamed); u.Known() {
		r.append(events.TypeModelUsage, events.ModelUsage{
			RequestID: f.Id.String(), Model: observe.Model(request),
			Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite,
		})
	}
	if text := format.FinalText(reply, streamed); text != "" {
		r.seen.Lock()
		r.finalText = text
		r.seen.Unlock()
	}
}

// StreamResponseModifier lets a streamed reply from the provider through
// untouched while keeping a copy to read token counts from once it ends.
func (r *Recorder) StreamResponseModifier(f *mitm.Flow, in io.Reader) io.Reader {
	if !f.Stream || f.Response == nil || !r.providers[strings.ToLower(f.Request.URL.Hostname())] || observe.For(f.Request.URL.Path) == nil {
		return in
	}
	return &replyCopy{in: in, done: func(raw []byte) {
		// the copy is as it came over the wire, so possibly compressed
		reply, err := (&mitm.Response{Header: f.Response.Header, Body: raw}).DecodedBody()
		if err != nil {
			return // unreadable: its token counts stay "not available"
		}
		r.readReply(f, reply, true)
	}}
}

// replyCopy passes a stream through and keeps what it has seen, up to a
// limit. When the stream ends it hands the copy over, once.
type replyCopy struct {
	in   io.Reader
	buf  []byte
	over bool
	done func([]byte)
}

func (c *replyCopy) Read(p []byte) (int, error) {
	n, err := c.in.Read(p)
	if !c.over {
		if len(c.buf)+n > maxReplyCopy {
			c.over, c.buf = true, nil // too long to read counts from
		} else {
			c.buf = append(c.buf, p[:n]...)
		}
	}
	if err != nil && c.done != nil {
		if !c.over {
			// in its own goroutine, so the end of the stream reaches the
			// agent without waiting for the copy to be read
			go c.done(c.buf)
		}
		c.done = nil
	}
	return n, err
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
	seen := r.scan(f, nil)
	if _, held := r.holds(f.Request, seen); held {
		return failingReader{fmt.Errorf("rehearse cannot hold a request body over %d MB, so the request was not sent", maxBodyInMemory>>20)}
	}
	if err := r.record(f, seen, false, nil); err != nil {
		return failingReader{err}
	}
	return in
}

// The status is recorded when the headers arrive, since a streamed
// response may not finish for minutes.
func (r *Recorder) Responseheaders(f *mitm.Flow) {
	r.append(events.TypeResponse, events.Response{RequestID: f.Id.String(), Status: f.Response.StatusCode})
	if r.providers[strings.ToLower(f.Request.URL.Hostname())] && strings.HasSuffix(f.Request.URL.Path, "/oauth/token") && f.Response.StatusCode/100 == 2 {
		r.seen.Lock()
		r.refreshed = true
		r.seen.Unlock()
	}
}

// Response sees a reply that was read whole (a streamed one is not). A
// reply from anywhere but the model provider is outside content: what it
// says may steer the agent, so the addresses in it are remembered.
func (r *Recorder) Response(f *mitm.Flow) {
	if f.Response.StatusCode/100 != 2 {
		return
	}
	body, err := f.Response.DecodedBody()
	if err != nil || len(body) == 0 {
		return // nothing readable in it
	}
	if r.providers[strings.ToLower(f.Request.URL.Hostname())] {
		r.readReply(f, body, false)
		return
	}
	ev := r.outside.read(displayHost(f.Request.URL), body, time.Now())
	ev.RequestID = f.Id.String()
	ev.Background = r.own[strings.ToLower(f.Request.URL.Hostname())]
	r.append(events.TypeOutsideContent, ev)
}

func (r *Recorder) RequestError(f *mitm.Flow, _ error) {
	r.append(events.TypeResponse, events.Response{RequestID: f.Id.String(), Failed: true})
}

// held says why a request is being kept back.
type held struct {
	door       bool
	provenance *events.Provenance
	after      []string
}

// holds decides whether a request is kept back. Any request carrying a
// watched value to a server other than the model provider is, whatever
// its method, and so is one to an address that first appeared in content
// read from another server. Requests that change something are too, when
// the recorder is set to hold writes.
func (r *Recorder) holds(req *mitm.Request, seen []events.IdentifierSeen) (held, bool) {
	if r.holding == nil {
		return held{}, false
	}
	host := strings.ToLower(req.URL.Hostname())
	if r.providers[host] {
		door := r.holding.ProviderDoor && r.unfamiliar(req)
		return held{door: door}, door
	}
	from, after := r.outside.contact(displayHost(req.URL), time.Now())
	why := held{provenance: from, after: after}
	write := r.holding.Writes && slices.Contains(writeMethods, req.Method) && !slices.Contains(r.holding.Allow, host)
	return why, write || len(seen) > 0 || from != nil
}

// unfamiliar reports whether a request to the model provider comes in a
// way not seen before: with a different credential from the first one
// used on that host, or as a file upload.
func (r *Recorder) unfamiliar(req *mitm.Request) bool {
	if req.Method == http.MethodPost && slices.ContainsFunc(uploadPaths, func(p string) bool { return strings.HasPrefix(req.URL.Path, p) }) {
		return true
	}
	credential := ""
	for _, name := range credentialHeaders {
		if v := req.Header.Get(name); v != "" && name != "Cookie" {
			credential = name + ": " + v
			break
		}
	}
	if credential == "" {
		return false
	}
	sum := sha256.Sum256([]byte(r.salt + credential))
	host := strings.ToLower(req.URL.Hostname())
	r.seen.Lock()
	defer r.seen.Unlock()
	first, known := r.credentials[host]
	if !known || r.refreshed {
		r.credentials[host] = sum
		r.refreshed = false
		return false
	}
	return first != sum
}

// shownPath is an address's path as it may be stored and displayed:
// watched values replaced by their names, and anything else that looks
// like a credential hidden.
func (r *Recorder) shownPath(path string) string {
	return redact.Text(r.matcher.Mask(path), r.holding.Canaries...)
}

// shownQuery is what is kept of a held request's query string: the name
// of each parameter, the name of any watched value in place of the value,
// and nothing of any other value. A query string can hold a credential
// under any name, so no value is stored as it was sent.
func (r *Recorder) shownQuery(raw string) string {
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, "&")
	for i, part := range parts {
		name, value, named := strings.Cut(part, "=")
		if !named {
			name, value = "", name
		}
		if found := r.matcher.Names([]byte(value)); len(found) > 0 {
			value = "[" + strings.Join(found, "][") + "]"
		} else if value != "" {
			value = redact.Hidden
		}
		if named {
			value = r.matcher.Mask(name) + "=" + value
		}
		parts[i] = value
	}
	return strings.Join(parts, "&")
}

// hold puts the request to the session's policy. builtIn says whether the
// built-in rules hold it. It reports whether the request was answered
// here: kept back with a stand-in answer, or refused. If a request that
// should be kept cannot be, it is refused, never sent.
func (r *Recorder) hold(f *mitm.Flow, seen []events.IdentifierSeen, why held, builtIn bool) (answered bool) {
	if err := r.Err(); err != nil {
		refuse(f, err)
		return true
	}
	req := f.Request
	answer := handlers.Answer{Status: http.StatusOK}
	if slices.Contains(writeMethods, req.Method) {
		answer = r.holding.Handlers.Answer(req.Method, req.URL.Hostname(), req.URL.Path)
	} else {
		// a read: the agent asked for something and is getting nothing
		answer.NeedsRealAnswer = true
	}
	ids := []string{}
	for _, s := range seen {
		if !slices.Contains(ids, s.IdentifierID) {
			ids = append(ids, s.IdentifierID)
		}
	}
	host := displayHost(req.URL)
	out, err := r.holding.Holder.Consider(events.ActionHeld{
		Kind:            events.ActionHTTP,
		RequestID:       f.Id.String(),
		Method:          req.Method,
		Host:            host,
		Scheme:          req.URL.Scheme,
		Path:            r.shownPath(req.URL.Path),
		Query:           r.shownQuery(req.URL.RawQuery),
		RequestBytes:    int64(len(req.Body)),
		ContentType:     req.Header.Get("Content-Type"),
		Destination:     host,
		IdentifierIDs:   ids,
		Deletes:         req.Method == http.MethodDelete,
		ChangesAccess:   changesAccess(req.URL.Path),
		NeedsRealAnswer: answer.NeedsRealAnswer || why.door,
		ProviderDoor:    why.door,
		Provenance:      why.provenance,
		OutsideSources:  why.after,
	}, hold.Payload{
		Method: req.Method,
		URL:    req.URL.String(),
		Header: req.Header.Clone(),
		Body:   req.Body,
		Seen:   seen,
	}, builtIn)
	if err != nil {
		refuse(f, err)
		return true
	}
	switch out.Action {
	case hold.Kept:
		header := http.Header{}
		if answer.ContentType != "" {
			header.Set("Content-Type", answer.ContentType)
		}
		f.Response = &mitm.Response{StatusCode: answer.Status, Header: header, Body: answer.Body}
		return true
	case hold.Refused:
		f.Response = &mitm.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
			Body:       []byte("Rehearsal: this request is refused by the policy for this project and was not sent. Rule: " + out.Rule + "\n"),
		}
		return true
	}
	return false // let through
}

// changesAccess reports whether a write to this path changes who can see
// or change a repository. The paths are those of the GitHub REST API for
// collaborators, branch protection and rule sets.
func changesAccess(path string) bool {
	for _, part := range []string{"/collaborators", "/protection", "/rulesets"} {
		if strings.Contains(path, part) {
			return true
		}
	}
	return false
}

// scan finds what the request carries: the watched identifiers and
// secrets, in its address, headers and body, and anything in a well-known
// credential format. A credential in a header that exists to carry one is
// the client logging in to that server, not a leak, so the format check
// skips those headers. A watched value is reported wherever it is.
func (r *Recorder) scan(f *mitm.Flow, body []byte) []events.IdentifierSeen {
	req := f.Request
	host := displayHost(req.URL)
	var seen []events.IdentifierSeen
	add := func(id string, where events.Where, form events.Form) {
		dup := slices.ContainsFunc(seen, func(s events.IdentifierSeen) bool { return s.IdentifierID == id && s.Where == where })
		if !dup {
			seen = append(seen, events.IdentifierSeen{RequestID: f.Id.String(), Host: host, IdentifierID: id, Where: where, Form: form})
		}
	}
	var canaries []string
	if r.holding != nil {
		canaries = r.holding.Canaries
	}
	look := func(where events.Where, text []byte, formats bool) {
		for _, m := range r.matcher.Find(text) {
			add(m.IdentifierID, where, m.Form)
		}
		if formats && !r.providers[strings.ToLower(req.URL.Hostname())] {
			for _, id := range ident.FindFormats(text, canaries...) {
				add(id, where, events.FormPlain)
			}
		}
	}
	look(events.WhereAddress, []byte(req.URL.String()), true)
	for name, values := range req.Header {
		for _, v := range values {
			look(events.WhereHeader, []byte(v), !slices.Contains(credentialHeaders, http.CanonicalHeaderKey(name)))
		}
	}
	look(events.WhereBody, body, true)
	return seen
}

// record writes the request event and what was found in the request. body
// is the request's body when it was read, nil when it was too large.
func (r *Recorder) record(f *mitm.Flow, seen []events.IdentifierSeen, bodyChecked bool, body []byte) error {
	if err := r.Err(); err != nil {
		return err
	}
	req := f.Request
	size := int64(len(req.Body))
	if !bodyChecked {
		size = req.Raw().ContentLength // -1 when the client did not say
	}
	ev := events.Request{
		ID:           f.Id.String(),
		Host:         displayHost(req.URL),
		Method:       req.Method,
		Path:         req.URL.Path,
		RequestBytes: size,
		Category:     r.category(req.URL.Hostname()),
		BodyChecked:  bodyChecked,
	}
	if ev.Category == events.CategoryProvider {
		ev.Model = observe.Model(body)
		ev.Conversation = observe.For(req.URL.Path) != nil
	}
	r.append(events.TypeRequest, ev)
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
