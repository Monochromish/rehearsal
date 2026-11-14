// Package replay turns a finished session into a recording of what it
// tried to do, and plays a recording back through the same rules a live
// session goes through, with no agent, no model provider and no network.
package replay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"rehearsal/internal/events"
	"rehearsal/internal/hold"
	"rehearsal/internal/proxy"
	"rehearsal/internal/review"
	"rehearsal/internal/sandbox"
	"rehearsal/internal/standin"
)

// Recording is a session reduced to what is needed to put it to the rules
// again: where each request went and what kind of thing it carried, never
// the thing itself. A scenario for `rehearse bench` is a recording with a
// few words about it.
type Recording struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	// scenarios only: what it shows, what would happen with nothing in the
	// way, and whether it is ordinary work that must not be stopped
	About       string `json:"about,omitempty"`
	Unprotected string `json:"unprotected,omitempty"`
	Benign      bool   `json:"benign,omitempty"`

	Steps []Step `json:"steps"`
}

// Step is one thing the agent did. Exactly one field is set.
type Step struct {
	Request *Request           `json:"request,omitempty"`
	Command *Command           `json:"command,omitempty"`
	File    *events.FileChange `json:"file,omitempty"`
	Package *Package           `json:"package,omitempty"`
}

// Request is an outgoing request without its contents.
type Request struct {
	Method string `json:"method"`
	// scheme, host and path; never a query string
	URL         string `json:"url"`
	Bytes       int64  `json:"bytes,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	// the kinds of watched value it carried: real, canary, secret
	Carries []string `json:"carries,omitempty"`
	// where it carried them. Empty means the address for a GET and the
	// body otherwise; "header" means a request header.
	In string `json:"in,omitempty"`
	// a name for the login it used, when it went to the model provider.
	// Two requests with different names used different logins.
	Credential string `json:"credential,omitempty"`
	// what the reply contained, for a request that got one: the hosts it
	// named, and how many pieces of hidden text
	Mentions []string `json:"mentions,omitempty"`
	Hidden   int      `json:"hidden,omitempty"`
}

// Command is a git, npm or gh command as its stand-in described it.
type Command struct {
	Kind            events.ActionKind `json:"kind"`
	Args            []string          `json:"args"`
	Destination     string            `json:"destination"`
	Subject         string            `json:"subject,omitempty"`
	Files           []string          `json:"files,omitempty"`
	Commits         []string          `json:"commits,omitempty"`
	Force           bool              `json:"force,omitempty"`
	Deletes         bool              `json:"deletes,omitempty"`
	Publishes       bool              `json:"publishes,omitempty"`
	ChangesAccess   bool              `json:"changes_access,omitempty"`
	Minor           bool              `json:"minor,omitempty"`
	NeedsRealAnswer bool              `json:"needs_real_answer,omitempty"`
}

// Package is an install of a package new to the project, with what the
// registry said about it.
type Package struct {
	Manager string `json:"manager"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	// false when the registry could not be asked
	Known  bool `json:"known"`
	Exists bool `json:"exists"`
	// how long before the install the version was published; 0 when the
	// registry did not say
	AgeHours int  `json:"age_hours,omitempty"`
	Scripts  bool `json:"scripts,omitempty"`
}

// InHeader is the value of Request.In for something carried in a header.
const InHeader = "header"

// Parse reads a recording and checks that it can be played.
func Parse(data []byte) (Recording, error) {
	var rec Recording
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rec); err != nil {
		return rec, fmt.Errorf("not a readable recording: %w", err)
	}
	if rec.Version != 1 {
		return rec, fmt.Errorf("the recording is version %d, and this rehearse reads version 1", rec.Version)
	}
	if rec.Name == "" || len(rec.Steps) == 0 {
		return rec, errors.New("the recording has no name, or no steps")
	}
	for i, s := range rec.Steps {
		set := 0
		for _, is := range []bool{s.Request != nil, s.Command != nil, s.File != nil, s.Package != nil} {
			if is {
				set++
			}
		}
		if set != 1 {
			return rec, fmt.Errorf("step %d must be exactly one of request, command, file or package", i+1)
		}
		if c := s.Command; c != nil && !slices.Contains([]events.ActionKind{events.ActionGitPush, events.ActionNpmPublish, events.ActionGh}, c.Kind) {
			return rec, fmt.Errorf("step %d: %q is not a kind of command", i+1, c.Kind)
		}
		if r := s.Request; r != nil && (r.Method == "" || !strings.Contains(r.URL, "://") || strings.Contains(r.URL, "?")) {
			return rec, fmt.Errorf("step %d: a request needs a method and an address with no query string", i+1)
		}
		if r := s.Request; r != nil && !slices.Contains([]string{"", InHeader}, r.In) {
			return rec, fmt.Errorf("step %d: %q is not a place a request can carry something", i+1, r.In)
		}
	}
	return rec, nil
}

// FromLog makes a recording from a finished session's events. Requests to
// the model provider are left out, apart from one that was held at the
// provider's door, since a replay has no provider to talk to.
func FromLog(name string, evs []events.Event) (Recording, error) {
	rec := Recording{Version: 1, Name: name, Steps: []Step{}}
	kinds := map[string]string{}  // identifier ID -> kind
	sent := map[string]*Request{} // request ID -> its step, for what is learned later
	providerSeen := false
	carried := func(r *Request, id string) {
		if k := kinds[id]; k != "" && !slices.Contains(r.Carries, k) {
			r.Carries = append(r.Carries, k)
		}
	}
	address := func(host, path string) string {
		scheme := "https"
		if strings.Contains(host, ":") {
			scheme = "http" // a port was worth showing, which is how local servers look
		}
		return scheme + "://" + host + path
	}
	for _, ev := range evs {
		switch ev.Type {
		case events.TypeSessionStarted:
			var d events.SessionStarted
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				return rec, fmt.Errorf("event %d is unreadable: %w", ev.Seq, err)
			}
			for _, id := range d.Identifiers {
				kinds[id.ID] = string(id.Kind)
			}
		case events.TypeRequest:
			var d events.Request
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				return rec, fmt.Errorf("event %d is unreadable: %w", ev.Seq, err)
			}
			if d.Category == events.CategoryProvider || strings.HasPrefix(d.ID, "applied-") {
				continue
			}
			r := &Request{Method: d.Method, URL: address(d.Host, d.Path), Bytes: max(d.RequestBytes, 0)}
			sent[d.ID] = r
			rec.Steps = append(rec.Steps, Step{Request: r})
		case events.TypeIdentifierSeen:
			var d events.IdentifierSeen
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				return rec, fmt.Errorf("event %d is unreadable: %w", ev.Seq, err)
			}
			if r := sent[d.RequestID]; r != nil {
				carried(r, d.IdentifierID)
			}
		case events.TypeOutsideContent:
			var d events.OutsideContent
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				return rec, fmt.Errorf("event %d is unreadable: %w", ev.Seq, err)
			}
			if r := sent[d.RequestID]; r != nil {
				r.Mentions, r.Hidden = d.Hosts, d.ZeroWidth+d.TagChars+d.Comments
			}
		case events.TypePackageChecked:
			var d events.PackageChecked
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				return rec, fmt.Errorf("event %d is unreadable: %w", ev.Seq, err)
			}
			// The log keeps the facts as words. These three are what the
			// words were made from, read back out of them.
			p := &Package{Manager: d.Manager, Name: d.Name, Version: d.Version}
			p.Known = !strings.Contains(d.Facts, "could not be asked")
			p.Exists = p.Known && !strings.Contains(d.Facts, "the registry has no")
			p.Scripts = strings.Contains(d.Facts, "runs a script when installed")
			if p.Exists {
				p.AgeHours = 24 * 365
				if d.Risk == events.RiskHigh && !p.Scripts || strings.Contains(d.Facts, "hours ago") || strings.Contains(d.Facts, "hour ago") {
					p.AgeHours = 9
				}
			}
			rec.Steps = append(rec.Steps, Step{Package: p})
		case events.TypeActionHeld:
			var d events.ActionHeld
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				return rec, fmt.Errorf("event %d is unreadable: %w", ev.Seq, err)
			}
			switch d.Kind {
			case events.ActionFile:
				if d.File != nil {
					f := *d.File
					rec.Steps = append(rec.Steps, Step{File: &f})
				}
			case events.ActionHTTP:
				r := &Request{Method: d.Method, URL: address(d.Host, d.Path), Bytes: d.RequestBytes, ContentType: d.ContentType}
				for _, id := range d.IdentifierIDs {
					carried(r, id)
				}
				if d.ProviderDoor {
					if !providerSeen {
						// the first login, which the second is told apart from
						providerSeen = true
						rec.Steps = append(rec.Steps, Step{Request: &Request{Method: http.MethodPost, URL: address(d.Host, "/v1/messages"), Credential: "first"}})
					}
					r.Credential = "second"
				}
				rec.Steps = append(rec.Steps, Step{Request: r})
			default:
				args := d.Command
				if len(args) > 0 {
					args = args[1:] // the program's own name
				}
				rec.Steps = append(rec.Steps, Step{Command: &Command{
					Kind: d.Kind, Args: args, Destination: d.Destination, Subject: d.Subject,
					Files: d.Files, Commits: d.Commits, Force: d.Force, Deletes: d.Deletes, Publishes: d.Publishes,
					ChangesAccess: d.ChangesAccess, Minor: d.Minor, NeedsRealAnswer: d.NeedsRealAnswer,
				}})
			}
		}
	}
	if len(rec.Steps) == 0 {
		return rec, errors.New("the session did nothing that can be replayed: no request outside the model provider, no held command, no file change")
	}
	return rec, nil
}

// Player puts a recording's steps to a session's rules.
type Player struct {
	Log      *events.Log
	Recorder *proxy.Recorder
	// nil when nothing is being held, which is the "unprotected" run
	Holder *hold.Holder
	Masker hold.Masker
	// a value of each kind for requests to carry, taken from what this
	// replay is watching for. Recordings hold kinds, not values.
	Values map[string]string
	Now    func() time.Time
}

// Result counts what went ahead.
type Result struct {
	// requests that were let through, and would have been sent
	Sent int
	// commands, file changes and installs that would have run unhindered
	Ran int
}

var programs = map[events.ActionKind]string{
	events.ActionGitPush: "git", events.ActionNpmPublish: "npm", events.ActionGh: "gh",
}

// Play goes through the steps in order. Nothing is sent anywhere and no
// program is run: a request that the rules let through is counted, and
// given the reply the recording says it got.
func (p Player) Play(rec Recording) (Result, error) {
	var res Result
	for i, s := range rec.Steps {
		var err error
		switch {
		case s.Request != nil:
			err = p.request(*s.Request, &res)
		case s.Command != nil:
			err = p.command(*s.Command, &res)
		case s.File != nil:
			err = p.file(*s.File, &res)
		case s.Package != nil:
			err = p.install(*s.Package, &res)
		}
		if err != nil {
			return res, fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	return res, nil
}

func (p Player) request(r Request, res *Result) error {
	var carried []string
	for _, kind := range r.Carries {
		v, ok := p.Values[kind]
		if !ok {
			return fmt.Errorf("the recording says a request carried a %q, and there is no such kind", kind)
		}
		carried = append(carried, v)
	}
	header := http.Header{}
	if r.ContentType != "" {
		header.Set("Content-Type", r.ContentType)
	}
	if r.Credential != "" {
		header.Set("Authorization", "Bearer replay-login-"+r.Credential)
	}
	address := r.URL
	var body []byte
	inBody := carried
	if r.In == InHeader {
		// the header a program most often puts something about itself in
		header.Set("User-Agent", "agent/1.0 ("+strings.Join(carried, "; ")+")")
		carried, inBody = nil, nil
	}
	if slices.Contains([]string{http.MethodGet, http.MethodHead}, r.Method) {
		if len(carried) > 0 {
			address += "?d=" + strings.Join(carried, ",")
		}
	} else {
		body = []byte(strings.Join(inBody, " "))
		if pad := int(r.Bytes) - len(body); pad > 0 {
			body = append(body, []byte(strings.Repeat(" ", pad))...)
		}
	}
	var reply strings.Builder
	for _, host := range r.Mentions {
		fmt.Fprintf(&reply, "See http://%s/ for more.\n", host)
	}
	reply.WriteString(strings.Repeat("<!-- a note the reader does not see -->\n", r.Hidden))
	if reply.Len() == 0 {
		reply.WriteString("ok\n")
	}
	status, err := p.Recorder.Play(r.Method, address, header, body, []byte(reply.String()))
	if err == nil && status == 0 {
		res.Sent++
	}
	return err
}

func (p Player) command(c Command, res *Result) error {
	if p.Holder == nil {
		res.Ran++
		return nil
	}
	out, err := p.Holder.HoldCommand(hold.Command{
		Kind: c.Kind, Program: programs[c.Kind], Args: c.Args, WorkDir: "/replayed/project",
		Destination: c.Destination, Subject: c.Subject, Files: c.Files, Commits: c.Commits,
		Force: c.Force, Deletes: c.Deletes, Publishes: c.Publishes, ChangesAccess: c.ChangesAccess,
		Minor: c.Minor, NeedsRealAnswer: c.NeedsRealAnswer,
	}, p.Masker)
	if err == nil && out.Action == hold.Passed {
		res.Ran++
	}
	return err
}

func (p Player) file(f events.FileChange, res *Result) error {
	if p.Holder == nil {
		res.Ran++
		return nil
	}
	if f.Files == 0 {
		f.Files = 1
	}
	if f.Sensitive == "" {
		f.Sensitive = sandbox.Sensitive(f.Path)
	}
	_, err := p.Holder.Hold(events.ActionHeld{
		Kind: events.ActionFile, WorkDir: "/replayed/project", Destination: "your project folder", Subject: f.Path,
		File: &f, SecretFiles: review.SecretLooking([]string{f.Path}),
	}, hold.Payload{})
	return err
}

func (p Player) install(k Package, res *Result) error {
	if p.Holder == nil {
		res.Ran++
		return nil
	}
	now := p.Now()
	var published time.Time
	if k.AgeHours > 0 {
		published = now.Add(-time.Duration(k.AgeHours) * time.Hour)
	}
	risk, words := standin.RatePackage(k.Name, k.Version, k.Known, k.Exists, published, k.Scripts, now)
	blocked := risk == events.RiskHigh
	if !blocked {
		res.Ran++
	}
	_, err := p.Log.Append(events.TypePackageChecked, events.PackageChecked{
		Manager: k.Manager, Name: k.Name, Version: k.Version, Risk: risk, Facts: words, Blocked: blocked,
	})
	return err
}
