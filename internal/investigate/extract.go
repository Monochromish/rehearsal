package investigate

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"rehearsal/internal/events"
)

// at is a decoded entry together with the raw line it came from.
type at[T any] struct {
	e *Entry
	v T
}

// view is one record with its entries decoded by type. It holds nothing
// that is not in an entry.
type view struct {
	index     int
	rec       *Record
	started   *at[events.SessionStarted]
	ids       map[string]events.Identifier
	providers map[string]bool
	requests  map[string]at[events.Request]
	order     []at[events.Request]
	seen      []at[events.IdentifierSeen]
	outside   []at[events.OutsideContent]
	held      []at[events.ActionHeld]
	decision  map[string]*Entry
	packages  []at[events.PackageChecked]
	actions   []at[events.AgentAction]
	results   map[string]at[events.AgentResult]
	notes     []at[events.ReportNotes]
	commits   []at[events.CommitsCreated]
	sandboxed bool
	sends     []send
}

// send is one watched value found in something on its way out.
type send struct {
	v    *view
	host string
	// "A POST request", "A git push"
	what    string
	request *Entry
	carrier *Entry
	held    *at[events.ActionHeld]
	id      events.Identifier
	where   events.Where
	form    events.Form
	encoded string
	hash    string
	// one of several requests in the same finding
	one bool
}

// first is the entry that shows the send happening.
func (s send) first() *Entry {
	if s.request != nil {
		return s.request
	}
	return s.carrier
}

func decode[T any](e *Entry) (at[T], bool) {
	var v T
	if err := json.Unmarshal([]byte(e.Data), &v); err != nil {
		return at[T]{}, false
	}
	return at[T]{e: e, v: v}, true
}

// extract decodes a record's entries. An entry that cannot be decoded is
// left out of the rules and still shown in the timeline.
func extract(index int, rec *Record) *view {
	v := &view{
		index: index, rec: rec,
		ids: map[string]events.Identifier{}, providers: map[string]bool{},
		requests: map[string]at[events.Request]{}, decision: map[string]*Entry{},
		results: map[string]at[events.AgentResult]{},
	}
	for i := range rec.Entries {
		e := &rec.Entries[i]
		switch events.Type(e.Type) {
		case events.TypeSessionStarted:
			if a, ok := decode[events.SessionStarted](e); ok && v.started == nil {
				v.started = &a
				for _, id := range a.v.Identifiers {
					v.ids[id.ID] = id
				}
				for _, h := range a.v.ProviderHosts {
					v.providers[strings.ToLower(h)] = true
				}
			}
		case events.TypeRequest:
			if a, ok := decode[events.Request](e); ok {
				v.requests[a.v.ID] = a
				v.order = append(v.order, a)
			}
		case events.TypeIdentifierSeen:
			if a, ok := decode[events.IdentifierSeen](e); ok {
				v.seen = append(v.seen, a)
			}
		case events.TypeOutsideContent:
			if a, ok := decode[events.OutsideContent](e); ok && !a.v.Background {
				v.outside = append(v.outside, a)
			}
		case events.TypeActionHeld:
			if a, ok := decode[events.ActionHeld](e); ok {
				v.held = append(v.held, a)
			}
		case events.TypeActionApproved:
			if a, ok := decode[events.ActionApproved](e); ok {
				v.decision[a.v.ActionID] = e
			}
		case events.TypeActionRefused:
			if a, ok := decode[events.ActionRefused](e); ok {
				v.decision[a.v.ActionID] = e
			}
		case events.TypePackageChecked:
			if a, ok := decode[events.PackageChecked](e); ok {
				v.packages = append(v.packages, a)
			}
		case events.TypeAgentAction:
			if a, ok := decode[events.AgentAction](e); ok {
				v.actions = append(v.actions, a)
			}
		case events.TypeAgentResult:
			if a, ok := decode[events.AgentResult](e); ok {
				v.results[a.v.ID] = a
			}
		case events.TypeReportNotes:
			if a, ok := decode[events.ReportNotes](e); ok {
				v.notes = append(v.notes, a)
			}
		case events.TypeCommitsCreated:
			if a, ok := decode[events.CommitsCreated](e); ok {
				v.commits = append(v.commits, a)
			}
		case events.TypeSandboxStarted:
			v.sandboxed = true
		}
	}
	v.sends = v.findSends()
	return v
}

func (v *view) identifier(id string) events.Identifier {
	if found, ok := v.ids[id]; ok {
		return found
	}
	return events.Identifier{ID: id, Label: "the watched value " + id}
}

func (v *view) findSends() []send {
	var out []send
	for _, s := range v.seen {
		if v.providers[strings.ToLower(s.v.Host)] {
			continue
		}
		one := send{
			v: v, host: s.v.Host, what: "A request", carrier: s.e, id: v.identifier(s.v.IdentifierID),
			where: s.v.Where, form: s.v.Form, encoded: s.v.Encoded, hash: s.v.DecodedHash,
		}
		if req, ok := v.requests[s.v.RequestID]; ok {
			one.request, one.what = req.e, "A "+req.v.Method+" request"
		}
		out = append(out, one)
	}
	for i := range v.held {
		h := &v.held[i]
		host, what := h.v.Host, "A "+h.v.Method+" request"
		if h.v.Kind != events.ActionHTTP {
			host, what = h.v.Destination, "A "+strings.ReplaceAll(string(h.v.Kind), "-", " ")
		}
		if h.v.Kind == events.ActionFile || v.providers[strings.ToLower(host)] {
			continue
		}
		sightings := h.v.Sightings
		if len(sightings) == 0 {
			// a record made before sightings were kept says only which values
			for _, id := range h.v.IdentifierIDs {
				sightings = append(sightings, events.Sighting{IdentifierID: id})
			}
		}
		for _, s := range sightings {
			same := slices.IndexFunc(out, func(o send) bool {
				seen, ok := decode[events.IdentifierSeen](o.carrier)
				return ok && h.v.RequestID != "" && seen.v.RequestID == h.v.RequestID && seen.v.IdentifierID == s.IdentifierID && seen.v.Where == s.Where
			})
			if same >= 0 {
				out[same].held = h
				continue
			}
			one := send{
				v: v, host: host, what: what, carrier: h.e, held: h, id: v.identifier(s.IdentifierID),
				where: s.Where, form: s.Form, encoded: s.Encoded, hash: s.DecodedHash,
			}
			if req, ok := v.requests[h.v.RequestID]; ok {
				one.request = req.e
			}
			out = append(out, one)
		}
	}
	slices.SortStableFunc(out, func(a, b send) int { return int(a.first().Seq - b.first().Seq) })
	return out
}

// Entity is something the entries name: a server, a file, a secret by
// its label, a command, a commit, a package, a page that was read.
type Entity struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// how many entries name it, and the first few of them
	Count int   `json:"count"`
	Refs  []Ref `json:"refs"`
}

const maxEntityRefs = 5

type entities struct {
	list  []Entity
	index map[string]int
}

func (s *entities) add(kind, name string, e *Entry, mark string) {
	if name == "" {
		return
	}
	if s.index == nil {
		s.index = map[string]int{}
	}
	key := kind + "\x00" + name
	i, ok := s.index[key]
	if !ok {
		i = len(s.list)
		s.index[key] = i
		s.list = append(s.list, Entity{Kind: kind, Name: name, Refs: []Ref{}})
	}
	s.list[i].Count++
	if len(s.list[i].Refs) < maxEntityRefs {
		s.list[i].Refs = append(s.list[i].Refs, ref(e, mark))
	}
}

// Entity kinds.
const (
	EntityServer  = "server"
	EntityPage    = "page read"
	EntitySecret  = "watched value"
	EntityFile    = "file"
	EntityCommand = "command"
	EntityCommit  = "commit"
	EntityPackage = "package"
	EntitySession = "session"
)

// collect lists what the views name, in the order it is first named.
func collect(views []*view) []Entity {
	var set entities
	for _, v := range views {
		if v.started != nil {
			set.add(EntitySession, v.rec.Custody.Label+" ("+v.rec.Custody.Session+")", v.started.e, "")
		}
		for _, r := range v.order {
			set.add(EntityServer, r.v.Host, r.e, jsonPair("host", r.v.Host))
		}
		for _, o := range v.outside {
			set.add(EntityPage, o.v.Host, o.e, jsonPair("host", o.v.Host))
		}
		for _, s := range v.sends {
			set.add(EntitySecret, s.id.Label, s.carrier, jsonPair("identifier_id", s.id.ID))
		}
		for _, h := range v.held {
			switch {
			case h.v.File != nil:
				set.add(EntityFile, h.v.File.Path, h.e, jsonPair("path", h.v.File.Path))
			case len(h.v.Command) > 0:
				set.add(EntityCommand, strings.Join(h.v.Command, " "), h.e, "")
			case h.v.Host != "":
				set.add(EntityServer, h.v.Host, h.e, jsonPair("host", h.v.Host))
			}
		}
		for _, a := range v.actions {
			// "shell" ran a command and "edit" changed a file. Anything
			// else is a file when its text is a path and nothing more.
			kind := EntityCommand
			if a.v.Kind == "edit" || (a.v.Kind != "shell" && !strings.Contains(a.v.Text, " ")) {
				kind = EntityFile
			}
			set.add(kind, a.v.Text, a.e, jsonPair("text", a.v.Text))
		}
		for _, c := range v.commits {
			for _, commit := range c.v.Commits {
				set.add(EntityCommit, commit, c.e, jsonText(commit))
			}
		}
		for _, p := range v.packages {
			set.add(EntityPackage, p.v.Name, p.e, jsonPair("name", p.v.Name))
		}
	}
	return set.list
}

// jsonText is s as it is written inside a stored entry, without the
// quotes round it.
func jsonText(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(b[1 : len(b)-1])
}

// jsonPair is a name and a text value as they are written in an entry.
func jsonPair(name, value string) string {
	return fmt.Sprintf(`"%s":"%s"`, name, jsonText(value))
}

// label says in a few words what an entry is, for a timeline.
func label(e *Entry) string {
	short := func(s string) string {
		if len(s) > 70 {
			return s[:70] + "…"
		}
		return s
	}
	switch events.Type(e.Type) {
	case events.TypeRequest:
		if a, ok := decode[events.Request](e); ok {
			return short(a.v.Method + " " + a.v.Host + a.v.Path)
		}
	case events.TypeResponse:
		if a, ok := decode[events.Response](e); ok {
			if a.v.Failed {
				return "no reply"
			}
			return fmt.Sprintf("reply, status %d", a.v.Status)
		}
	case events.TypeIdentifierSeen:
		if a, ok := decode[events.IdentifierSeen](e); ok {
			return short("a watched value in a request to " + a.v.Host)
		}
	case events.TypeOutsideContent:
		if a, ok := decode[events.OutsideContent](e); ok {
			return short("a reply read from " + a.v.Host)
		}
	case events.TypeActionHeld:
		if a, ok := decode[events.ActionHeld](e); ok {
			return short("held: " + string(a.v.Kind) + " to " + a.v.Destination)
		}
	case events.TypeAgentAction:
		if a, ok := decode[events.AgentAction](e); ok {
			return short("the agent: " + a.v.Text)
		}
	case events.TypePackageChecked:
		if a, ok := decode[events.PackageChecked](e); ok {
			return short("package " + a.v.Name + " checked")
		}
	}
	return strings.ReplaceAll(e.Type, "_", " ")
}
