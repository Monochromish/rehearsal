package investigate

import (
	"fmt"
	"slices"
	"strings"

	"rehearsal/internal/events"
	"rehearsal/internal/ident"
	"rehearsal/internal/review"
)

// The three kinds of statement. They are never mixed: a reader can skip
// everything a model wrote and lose nothing factual.
const (
	Observed = "Observed"
	Derived  = "Derived"
	ByModel  = "Written by a model"
)

// Ref points at one entry of one record, and at the part of its raw line
// that supports what is being said.
type Ref struct {
	Session string `json:"session"`
	Label   string `json:"label"`
	Seq     int64  `json:"seq"`
	Hash    string `json:"hash"`
	// text that appears in the entry's data exactly as written here
	Mark string `json:"mark,omitempty"`
}

// ref points at e, marking the first of the candidates that its raw data
// really contains.
func ref(e *Entry, marks ...string) Ref {
	r := Ref{Session: e.Session, Label: e.Label, Seq: e.Seq, Hash: e.Hash}
	for _, m := range marks {
		if m != "" && strings.Contains(e.Data, m) {
			r.Mark = m
			break
		}
	}
	return r
}

// Cite is how a Ref is written in text: [S1#12].
func (r Ref) Cite() string { return fmt.Sprintf("[%s#%d]", r.Label, r.Seq) }

// Statement is one thing said about the record.
type Statement struct {
	// Observed or Derived
	Kind string `json:"kind"`
	Text string `json:"text"`
	// for a derived statement, the rule that gave it
	Rule string `json:"rule,omitempty"`
	Refs []Ref  `json:"refs"`
}

// Step is one link of a finding's chain: an entry, or a rule that joins
// the entries either side of it.
type Step struct {
	Label string `json:"label"`
	Rule  string `json:"rule,omitempty"`
	Refs  []Ref  `json:"refs"`
}

// HowStep is one stage of the pipeline, as it bore on a finding.
type HowStep struct {
	Stage string `json:"stage"`
	What  string `json:"what"`
	Refs  []Ref  `json:"refs"`
}

// Extract is a stored excerpt of something the agent read.
type Extract struct {
	Ref    Ref    `json:"ref"`
	Source string `json:"source"`
	Text   string `json:"text"`
	// the part of Text that names the address or gives the instruction
	Marked string `json:"marked,omitempty"`
	// how the text was hidden from a reader, when the record says
	Hidden string `json:"hidden,omitempty"`
}

// Proof shows that an encoded text was a watched value without showing
// the value.
type Proof struct {
	Encoding string `json:"encoding"`
	Encoded  string `json:"encoded"`
	Value    string `json:"value"`
	// the hash stored for the value when the session began, and the hash
	// of what decoding the text gave; either may be empty
	StoredHash  string `json:"stored_hash,omitempty"`
	DecodedHash string `json:"decoded_hash,omitempty"`
	Equal       bool   `json:"equal"`
	Text        string `json:"text"`
	Refs        []Ref  `json:"refs"`
}

// Link joins two entities by the entries that connect them.
type Link struct {
	From Entity `json:"from"`
	To   Entity `json:"to"`
	Via  string `json:"via"`
	Refs []Ref  `json:"refs"`
}

// Finding is one thing the rules report, with everything it rests on.
type Finding struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// the finding in ten words or fewer, one line that supports it, and
	// the finding as a full sentence
	Headline string `json:"headline"`
	Support  string `json:"support"`
	Title    string `json:"title"`
	// the one entry that most directly shows it
	Key      Ref      `json:"key"`
	Severity int      `json:"severity"`
	Sessions []string `json:"sessions"`

	Statements []Statement `json:"statements"`
	Chain      []Step      `json:"chain"`
	Extracts   []Extract   `json:"extracts,omitempty"`
	Proof      *Proof      `json:"proof,omitempty"`
	How        []HowStep   `json:"how"`
	Links      []Link      `json:"links,omitempty"`

	// every entry the finding rests on, in order
	Refs []Ref `json:"refs"`
	// those of them that cannot be relied on, and what that means for
	// this finding; empty when all of them hold
	Unverified     []Ref  `json:"unverified"`
	UnverifiedNote string `json:"unverified_note,omitempty"`
	// set when the record itself did not verify, whichever entries this
	// finding rests on
	RecordNote string `json:"record_note,omitempty"`

	// the account a model wrote of it, when there is one
	Narrative *Narrative `json:"narrative,omitempty"`
}

// Finding kinds, in the order the overview lists them.
const (
	KindSentAfterReading = "sent-after-reading"
	KindEncoded          = "encoded-value"
	KindDestructive      = "destructive-request"
	KindHighRisk         = "high-risk-action"
	KindValueSent        = "value-sent"
	KindNewServer        = "new-server-after-reading"
	KindClaim            = "contradicted-claim"
	KindInstall          = "stopped-install"
	KindSameDestination  = "same-destination"
)

// Kind names a kind of finding.
type Kind struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

// Kinds lists every kind of finding the rules can report.
var Kinds = []Kind{
	{KindSentAfterReading, "Data sent after reading outside content"},
	{KindEncoded, "A watched value sent in encoded form"},
	{KindDestructive, "A destructive request"},
	{KindHighRisk, "An action the rules rate as high risk"},
	{KindValueSent, "A watched value sent outside"},
	{KindNewServer, "A new server contacted after outside content was read"},
	{KindClaim, "A claim the record contradicts"},
	{KindInstall, "An install that was stopped"},
	{KindSameDestination, "The same destination in several sessions"},
}

// Rule is a named rule, in words.
type Rule struct {
	Name string `json:"name"`
	Says string `json:"says"`
}

// Rules are the linking rules a derived statement can name.
var Rules = []Rule{
	{"address-from-content", "A host is named in a reply that was read, no earlier entry is a request to that host, and a later entry is."},
	{"first-contact-after-reading", "The first entry that is a request to a host comes after an entry that records outside content being read."},
	{"decoded-hash-equals-stored-hash", "The salted hash of what decoding an encoded text gave equals the salted hash stored for a watched value when the session began."},
	{"decodes-to-listed-value", "Decoding an encoded text gives a value that the record lists in full, because it was planted or is not a secret."},
	{"value-from-file", "A watched value is labelled as coming from a file, and an earlier entry shows the agent reading that file."},
	{"claim-against-result", "A claim's verdict names a command; the entry for that command and the entry for its result are set beside the claim."},
	{"no-hold-recorded", "No entry holds the request that carried the value, so nothing in the record stopped it."},
	{"built-in-rating", "The rules that decide what is held rate the held action as high risk, from the fields of its entry alone."},
	{"grouped-by-destination", "Requests that carry the same watched value to the same host are counted together."},
	{"same-destination", "The same host receives a watched value, or is named in outside content, in more than one session."},
}

type builder struct {
	views    []*view
	mentions map[string]mention
	contact  map[string]point
	findings []Finding
	// the sends a finding has already reported, by the entry that says
	// the value was found and which value it was
	reported map[sent]bool
	// the requests and commands those sends were found in
	leaked map[*Entry]bool
}

type sent struct {
	carrier *Entry
	id      string
}

func (b *builder) report(s send) {
	b.reported[sent{s.carrier, s.id.ID}] = true
	b.leaked[s.first()] = true
}

// leaks counts the requests and commands that a finding reports as
// carrying a watched value.
func (b *builder) leaks() int {
	return len(b.leaked)
}

// point is a place in the case: a session and an entry in it.
type point struct {
	view int
	e    *Entry
}

func (p point) before(view int, e *Entry) bool {
	return p.view < view || (p.view == view && p.e.Seq < e.Seq)
}

// mention is the first time outside content named a host.
type mention struct {
	point
	source  string
	content events.OutsideContent
	excerpt events.Mention
}

func newBuilder(views []*view) *builder {
	b := &builder{views: views, mentions: map[string]mention{}, contact: map[string]point{}, reported: map[sent]bool{}, leaked: map[*Entry]bool{}}
	for _, v := range views {
		for _, o := range v.outside {
			named := func(host string, m events.Mention) {
				if _, known := b.mentions[host]; !known && host != o.v.Host {
					b.mentions[host] = mention{point: point{v.index, o.e}, source: o.v.Host, content: o.v, excerpt: m}
				}
			}
			for _, m := range o.v.Mentions {
				named(m.Host, m)
			}
			for _, h := range o.v.Hosts {
				named(h, events.Mention{Host: h})
			}
		}
		first := func(host string, e *Entry) {
			if p, known := b.contact[host]; host != "" && (!known || !p.before(v.index, e)) {
				b.contact[host] = point{v.index, e}
			}
		}
		for _, r := range v.order {
			first(r.v.Host, r.e)
		}
		for _, h := range v.held {
			first(h.v.Host, h.e)
		}
	}
	return b
}

func whereWords(w events.Where) string {
	switch w {
	case events.WhereAddress:
		return "its address"
	case events.WhereHeader:
		return "a header"
	case events.WhereBody:
		return "its body"
	case "":
		return "what it sent"
	}
	return "its " + string(w)
}

var encodings = map[events.Form]string{
	events.FormBase64: "base64",
	events.FormHex:    "hexadecimal",
	events.FormURL:    "URL encoding (percent-encoding)",
}

func (s send) carried() string {
	text := fmt.Sprintf("%s to %s carried %q in %s", s.what, s.host, s.id.Label, whereWords(s.where))
	if enc, ok := encodings[s.form]; ok {
		text += ", written in " + enc
	}
	return text + "."
}

// carriedRef points at the entry that says the value was found, marking
// the encoded text when there is one and otherwise which value it was.
func (s send) carriedRef() Ref {
	return ref(s.carrier, jsonText(s.encoded), jsonPair("identifier_id", s.id.ID), jsonText(s.id.ID))
}

// outcome says what the record shows became of a send.
func (b *builder) outcome(s send) (Statement, Step) {
	st, step := b.outcomeOf(s)
	// said of one request among several: name the entry it is in
	if s.one {
		st.Text = strings.Replace(strings.Replace(st.Text, "It was held", "The one in "+ref(s.first()).Cite()+" was held", 1),
			"No entry in this record holds that request.", "No entry in this record holds the one in "+ref(s.first()).Cite()+".", 1)
	}
	return st, step
}

func (b *builder) outcomeOf(s send) (Statement, Step) {
	if s.held == nil {
		st := Statement{Kind: Derived, Rule: "no-hold-recorded", Refs: []Ref{ref(s.first())},
			Text: "No entry in this record holds that request. Nothing recorded here stopped it."}
		return st, Step{Label: "Not held", Rule: "no-hold-recorded", Refs: st.Refs}
	}
	held := ref(s.held.e, jsonPair("id", s.held.v.ID))
	decided, ok := s.v.decision[s.held.v.ID]
	switch {
	case !ok:
		return Statement{Kind: Observed, Refs: []Ref{held}, Text: "It was held before it left. No decision on it is recorded."},
			Step{Label: "Held, not decided", Refs: []Ref{held}}
	case events.Type(decided.Type) == events.TypeActionRefused:
		refs := []Ref{held, ref(decided, jsonPair("action_id", s.held.v.ID))}
		return Statement{Kind: Observed, Refs: refs, Text: "It was held before it left, and then refused."},
			Step{Label: "Held, then refused", Refs: refs}
	}
	refs := []Ref{held, ref(decided, jsonPair("action_id", s.held.v.ID))}
	return Statement{Kind: Observed, Refs: refs, Text: "It was held before it left, and then allowed."},
		Step{Label: "Held, then allowed", Refs: refs}
}

func entity(kind, name string) Entity { return Entity{Kind: kind, Name: name, Refs: []Ref{}} }

func (v *view) sessionEntity() Entity {
	return entity(EntitySession, v.rec.Custody.Label+" ("+v.rec.Custody.Session+")")
}

// marked is the address of host inside an excerpt, as it is written there.
func marked(excerpt, host string) string {
	for _, scheme := range []string{"https://", "http://", ""} {
		if strings.Contains(excerpt, scheme+host) {
			return scheme + host
		}
	}
	return ""
}

// sentAfterReading reports each host that was first named in outside
// content and then sent a watched value.
func (b *builder) sentAfterReading() map[string]bool {
	covered := map[string]bool{}
	for _, v := range b.views {
		var hosts []string
		groups := map[string][]send{}
		for _, s := range v.sends {
			m, ok := b.mentions[s.host]
			first, contacted := b.contact[s.host]
			if !ok || !m.before(v.index, s.first()) || (contacted && first.before(m.view, m.e)) {
				continue
			}
			if _, seen := groups[s.host]; !seen {
				hosts = append(hosts, s.host)
			}
			groups[s.host] = append(groups[s.host], s)
		}
		for _, host := range hosts {
			m, sends := b.mentions[host], groups[host]
			covered[v.rec.Custody.Label+"\x00"+host] = true
			var labels []string
			for _, s := range sends {
				if !slices.Contains(labels, s.id.Label) {
					labels = append(labels, s.id.Label)
				}
			}
			f := Finding{
				Kind: KindSentAfterReading, Severity: 3,
				Title: fmt.Sprintf("%s sent to %s, a host first named in content read from %s", strings.Join(labels, " and "), host, m.source),
			}
			read := ref(m.e, jsonPair("host", m.source))
			f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{read},
				Text: fmt.Sprintf("A reply of %d bytes from %s was read.", m.content.Bytes, m.source)})
			f.Chain = append(f.Chain, Step{Label: "Reply read from " + m.source, Refs: []Ref{read}})

			named := ref(m.e, jsonText(marked(m.excerpt.Excerpt, host)), jsonText(host))
			text := fmt.Sprintf("That reply named %s", host)
			if m.excerpt.Hidden != "" {
				text += ", inside " + m.excerpt.Hidden
			}
			f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{named}, Text: text + "."})
			f.Chain = append(f.Chain, Step{Label: "It named " + host, Refs: []Ref{named}})
			if m.excerpt.Excerpt != "" {
				f.Extracts = append(f.Extracts, Extract{Ref: named, Source: m.source, Text: m.excerpt.Excerpt, Marked: marked(m.excerpt.Excerpt, host), Hidden: m.excerpt.Hidden})
			}
			if hidden := hiddenWords(m.content); hidden != "" {
				at := ref(m.e, `"comments":`, `"zero_width":`, `"tag_chars":`)
				f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{at},
					Text: "The reply held text that a reader of it would not see: " + hidden + "."})
				if m.content.HiddenExcerpt != "" && m.content.HiddenExcerpt != m.excerpt.Excerpt {
					f.Extracts = append(f.Extracts, Extract{
						Ref: ref(m.e, jsonText(m.content.HiddenExcerpt)), Source: m.source, Text: m.content.HiddenExcerpt,
						Marked: m.content.HiddenExcerpt, Hidden: "an HTML comment, which a reader of the page does not see",
					})
				}
			}

			first := b.contact[host]
			contact := ref(first.e, jsonPair("host", host))
			link := Statement{Kind: Derived, Rule: "address-from-content", Refs: []Ref{named, contact},
				Text: fmt.Sprintf("%s is named in %s. No entry before that is a request to it. The first request to it is %s, which comes after.", host, named.Cite(), contact.Cite())}
			f.Statements = append(f.Statements, link)
			f.Chain = append(f.Chain, Step{Label: "Address taken from the content", Rule: link.Rule, Refs: link.Refs},
				Step{Label: "Request to " + host, Refs: []Ref{contact}})

			var carried []Ref
			for _, s := range sends {
				refs := []Ref{s.carriedRef()}
				if s.request != nil && s.request != s.carrier {
					refs = append([]Ref{ref(s.request, jsonPair("host", host))}, refs...)
				}
				f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: refs, Text: s.carried()})
				f.Chain = append(f.Chain, Step{Label: fmt.Sprintf("Carried %q", s.id.Label), Refs: []Ref{s.carriedRef()}})
				carried = append(carried, s.carriedRef())
				b.report(s)
				f.Links = append(f.Links, Link{From: entity(EntitySecret, s.id.Label), To: entity(EntityServer, host), Via: "carried in a request", Refs: []Ref{s.carriedRef()}})
				if file, ok := b.fromFile(s); ok {
					f.Statements = append(f.Statements, file.statements...)
					f.Links = append(f.Links, file.link)
				}
			}
			if h := sends[0].held; h != nil && h.v.Provenance != nil {
				f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{ref(h.e, `"provenance":`)},
					Text: fmt.Sprintf("When it held the request, the recorder noted that this address first appeared in content from %s, %d seconds earlier.", h.v.Provenance.SourceHost, h.v.Provenance.SecondsBefore)})
			}
			for _, s := range requests(sends) {
				st, step := b.outcome(s)
				f.Statements = append(f.Statements, st)
				f.Chain = append(f.Chain, step)
			}

			f.Links = append(f.Links,
				Link{From: entity(EntityPage, m.source), To: entity(EntityServer, host), Via: "named in the content", Refs: []Ref{named}},
				Link{From: v.sessionEntity(), To: entity(EntityServer, host), Via: "sent a request", Refs: []Ref{contact}})
			if m.view != v.index {
				f.Links = append(f.Links, Link{From: b.views[m.view].sessionEntity(), To: entity(EntityPage, m.source), Via: "read", Refs: []Ref{read}})
			}
			f.How = []HowStep{
				{Stage: "Extraction", Refs: append([]Ref{read, contact}, carried...),
					What: "Took the reply that was read, the host it named, the request to that host and the watched value found in it, each from one entry."},
				{Stage: "Linking", Refs: link.Refs,
					What: "Rule address-from-content joined the reply to the later request to the host it named."},
				{Stage: "Finding", Refs: carried,
					What: "Rule " + KindSentAfterReading + " reports a request that carries a watched value to a host first named in outside content."},
			}
			f.Headline, f.Support, f.Key = shortValue(sends)+" sent to "+host, "The address came from content read from "+m.source, sends[0].carriedRef()
			b.findings = append(b.findings, f)
		}
	}
	return covered
}

func hiddenWords(o events.OutsideContent) string {
	var parts []string
	count := func(n int, one, many string) {
		switch {
		case n == 1:
			parts = append(parts, "1 "+one)
		case n > 1:
			parts = append(parts, fmt.Sprintf("%d %s", n, many))
		}
	}
	count(o.Comments, "HTML comment", "HTML comments")
	count(o.ZeroWidth, "zero-width character", "zero-width characters")
	count(o.TagChars, "invisible tag character", "invisible tag characters")
	return strings.Join(parts, ", ")
}

type fileLink struct {
	statements []Statement
	link       Link
}

// fromFile looks for the agent reading the file a value is labelled as
// coming from, before the value was sent.
func (b *builder) fromFile(s send) (fileLink, bool) {
	_, file, ok := strings.Cut(s.id.Label, " from ")
	if !ok || s.id.Kind != events.KindSecret {
		return fileLink{}, false
	}
	for _, a := range s.v.actions {
		if a.e.Seq >= s.first().Seq || !(a.v.Text == file || strings.HasSuffix(a.v.Text, "/"+file) || strings.Contains(a.v.Text, " "+file)) {
			continue
		}
		read := ref(a.e, jsonText(file))
		return fileLink{
			statements: []Statement{
				{Kind: Observed, Refs: []Ref{read}, Text: fmt.Sprintf("The agent told the model it used %s (%s).", file, a.v.Text)},
				{Kind: Derived, Rule: "value-from-file", Refs: []Ref{read, s.carriedRef()},
					Text: fmt.Sprintf("%q is a value from %s. The agent used that file in %s, before the value was found leaving in %s.", s.id.Label, file, read.Cite(), s.carriedRef().Cite())},
			},
			link: Link{From: entity(EntityFile, file), To: entity(EntitySecret, s.id.Label), Via: "read before the value was sent", Refs: []Ref{read}},
		}, true
	}
	return fileLink{}, false
}

// encoded reports each watched value that left in an encoded form, with
// the evidence that the encoded text was that value.
func (b *builder) encoded() {
	for _, v := range b.views {
		for _, s := range v.sends {
			enc, ok := encodings[s.form]
			if !ok {
				continue
			}
			f := Finding{
				Kind: KindEncoded, Severity: 3,
				Title: fmt.Sprintf("%s sent to %s written in %s", s.id.Label, s.host, enc),
			}
			carried := s.carriedRef()
			b.report(s)
			f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{carried}, Text: s.carried()})
			f.Chain = append(f.Chain, Step{Label: "Request to " + s.host, Refs: []Ref{ref(s.first(), jsonPair("host", s.host))}},
				Step{Label: "Encoded text found in " + whereWords(s.where), Refs: []Ref{carried}})

			p := &Proof{Encoding: enc, Encoded: s.encoded, Value: s.id.Label, DecodedHash: s.hash, StoredHash: s.id.Hash, Refs: []Ref{carried}}
			rule := ""
			switch {
			case v.started != nil && s.id.Hash != "" && s.hash != "":
				stored := ref(v.started.e, s.id.Hash)
				p.Refs = append(p.Refs, stored)
				p.Equal = s.hash == s.id.Hash
				f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{stored},
					Text: fmt.Sprintf("When the session began, the salted hash stored for %q was %s.", s.id.Label, s.id.Hash)})
				f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{ref(s.carrier, s.hash)},
					Text: fmt.Sprintf("The recorder decoded the text as %s and stored the salted hash of what that gave: %s.", enc, s.hash)})
				if p.Equal {
					rule = "decoded-hash-equals-stored-hash"
					p.Text = fmt.Sprintf("Decoding the text as %s gives a value whose salted hash equals the hash stored for %q. The two hashes are the same, so the encoded text was that value. The value itself is not in the record.", enc, s.id.Label)
				} else {
					p.Text = fmt.Sprintf("The two hashes differ, so the record does not show that the decoded text was %q.", s.id.Label)
				}
			case v.started != nil && s.id.Value != "" && ident.DecodesTo(s.encoded, s.form, s.id.Value):
				listed := ref(v.started.e, jsonText(s.id.Value))
				p.Refs = append(p.Refs, listed)
				p.Equal = true
				rule = "decodes-to-listed-value"
				p.Text = fmt.Sprintf("Decoding the text as %s gives the value the record lists for %q in %s. That value is kept in the record because it was planted or is not a secret, so the decoding can be repeated by hand.", enc, s.id.Label, listed.Cite())
			case s.id.Value != "":
				p.Text = fmt.Sprintf("The record keeps only the first characters of the encoded text, because it is a value of the user's that could be decoded from it. The recorder matched it as %s when the request was made; the match cannot be repeated from the record.", enc)
			default:
				p.Text = "The record holds no hash to set beside this text. The recorder matched it when the request was made; the match cannot be repeated from the record."
			}
			f.Proof = p
			if rule != "" {
				f.Statements = append(f.Statements, Statement{Kind: Derived, Rule: rule, Refs: p.Refs, Text: p.Text})
				f.Chain = append(f.Chain, Step{Label: "Decoded and compared", Rule: rule, Refs: p.Refs})
			}
			if file, ok := b.fromFile(s); ok {
				f.Statements = append(f.Statements, file.statements...)
				f.Links = append(f.Links, file.link)
			}
			st, step := b.outcome(s)
			f.Statements = append(f.Statements, st)
			f.Chain = append(f.Chain, step)
			f.Links = append(f.Links,
				Link{From: entity(EntitySecret, s.id.Label), To: entity(EntityServer, s.host), Via: "carried, encoded", Refs: []Ref{carried}},
				Link{From: v.sessionEntity(), To: entity(EntityServer, s.host), Via: "sent a request", Refs: []Ref{ref(s.first())}})
			f.How = []HowStep{
				{Stage: "Extraction", Refs: []Ref{carried}, What: "Took the encoded text, its form and the value it was matched to from one entry."},
				{Stage: "Linking", Refs: p.Refs, What: "Set the hash of the decoded text beside the hash stored for the value, or decoded the text and compared it with the value the record lists."},
				{Stage: "Finding", Refs: []Ref{carried}, What: "Rule " + KindEncoded + " reports a watched value found in a request in any form other than plain text."},
			}
			f.Headline, f.Key = shortValue([]send{s})+" sent to "+s.host+", encoded", carried
			f.Support = "Written in " + enc + ". The record does not prove which value it was."
			if p.Equal {
				f.Support = "Written in " + enc + ". The proof is below."
			}
			b.findings = append(b.findings, f)
		}
	}
}

// destructive reports requests and commands that delete or overwrite.
func (b *builder) destructive() {
	for _, v := range b.views {
		heldRequests := map[string]bool{}
		for i := range v.held {
			h := &v.held[i]
			heldRequests[h.v.RequestID] = true
			if !h.v.Deletes && !h.v.Force {
				continue
			}
			what, mark := "deletes something at "+h.v.Destination, `"deletes":true`
			if !h.v.Deletes {
				what, mark = "overwrites history at "+h.v.Destination, `"force":true`
			}
			subject := strings.Join(h.v.Command, " ")
			if subject == "" {
				subject = h.v.Method + " " + h.v.Host + h.v.Path
			}
			at := ref(h.e, mark)
			f := Finding{Kind: KindDestructive, Severity: 3, Title: fmt.Sprintf("%s: %s", subject, what)}
			f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{at},
				Text: fmt.Sprintf("The agent tried %q, which the recorder marked as one that %s.", subject, what)})
			f.Chain = append(f.Chain, Step{Label: subject, Refs: []Ref{at}})
			st, step := b.outcome(send{v: v, held: h, carrier: h.e})
			f.Statements = append(f.Statements, st)
			f.Chain = append(f.Chain, step)
			// once allowed, the request is made for real and has an entry of its own
			if decided, ok := v.decision[h.v.ID]; ok && h.v.Kind == events.ActionHTTP {
				for _, r := range v.order {
					if r.e.Seq > decided.Seq && r.v.Method == h.v.Method && r.v.Host == h.v.Host && r.v.Path == h.v.Path && !heldRequests[r.v.ID] {
						made := ref(r.e, jsonPair("method", r.v.Method))
						f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{made}, Text: fmt.Sprintf("After that, a %s request was made to %s%s.", r.v.Method, r.v.Host, r.v.Path)})
						f.Chain = append(f.Chain, Step{Label: "Made after it was allowed", Refs: []Ref{made}})
						heldRequests[r.v.ID] = true
						break
					}
				}
			}
			f.Links = append(f.Links, Link{From: v.sessionEntity(), To: entity(EntityServer, h.v.Destination), Via: "destructive request", Refs: []Ref{at}})
			f.How = []HowStep{
				{Stage: "Extraction", Refs: []Ref{at}, What: "Took the held action and what the recorder marked it as from one entry."},
				{Stage: "Finding", Refs: []Ref{at}, What: "Rule " + KindDestructive + " reports a held action marked as deleting or as a forced push, and a DELETE request that was not held."},
			}
			f.Headline, f.Support, f.Key = "A forced push was attempted", subject, at
			if h.v.Deletes {
				f.Headline = "A delete was attempted at " + h.v.Destination
			}
			b.findings = append(b.findings, f)
		}
		for _, r := range v.order {
			if r.v.Method != "DELETE" || heldRequests[r.v.ID] || r.v.Category == events.CategoryProvider {
				continue
			}
			at := ref(r.e, `"method":"DELETE"`)
			f := Finding{Kind: KindDestructive, Severity: 3, Title: fmt.Sprintf("DELETE %s%s was sent", r.v.Host, r.v.Path)}
			f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{at},
				Text: fmt.Sprintf("A DELETE request was made to %s%s.", r.v.Host, r.v.Path)})
			f.Chain = append(f.Chain, Step{Label: "DELETE " + r.v.Host + r.v.Path, Refs: []Ref{at}})
			st, step := b.outcome(send{v: v, request: r.e, carrier: r.e})
			f.Statements = append(f.Statements, st)
			f.Chain = append(f.Chain, step)
			f.Links = append(f.Links, Link{From: v.sessionEntity(), To: entity(EntityServer, r.v.Host), Via: "destructive request", Refs: []Ref{at}})
			f.How = []HowStep{
				{Stage: "Extraction", Refs: []Ref{at}, What: "Took the request's method and address from one entry."},
				{Stage: "Finding", Refs: []Ref{at}, What: "Rule " + KindDestructive + " reports a held action marked as deleting or as a forced push, and a DELETE request that was not held."},
			}
			f.Headline, f.Support, f.Key = "A DELETE request was sent to "+r.v.Host, r.v.Host+r.v.Path, at
			b.findings = append(b.findings, f)
		}
	}
}

// newServers reports a destination first contacted after outside content
// had been read, when a fuller chain to it was not already reported.
func (b *builder) newServers(covered map[string]bool) {
	for _, v := range b.views {
		for i := range v.held {
			h := &v.held[i]
			if len(h.v.OutsideSources) == 0 || h.v.Host == "" || covered[v.rec.Custody.Label+"\x00"+h.v.Host] {
				continue
			}
			at := ref(h.e, `"outside_sources":`)
			f := Finding{Kind: KindNewServer, Severity: 2,
				Title: fmt.Sprintf("%s was first contacted after content from %s had been read", h.v.Host, strings.Join(h.v.OutsideSources, ", "))}
			f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{at},
				Text: fmt.Sprintf("When it held a %s request to %s, the recorder noted that the host had not been contacted before content from %s was read.", h.v.Method, h.v.Host, strings.Join(h.v.OutsideSources, ", "))})
			var read *Entry
			for _, o := range v.outside {
				if o.e.Seq < h.e.Seq && slices.Contains(h.v.OutsideSources, o.v.Host) {
					read = o.e
					break
				}
			}
			if read != nil {
				from := ref(read, jsonPair("host", h.v.OutsideSources[0]))
				f.Chain = append(f.Chain, Step{Label: "Reply read from " + strings.Join(h.v.OutsideSources, ", "), Refs: []Ref{from}})
				st := Statement{Kind: Derived, Rule: "first-contact-after-reading", Refs: []Ref{from, at},
					Text: fmt.Sprintf("%s records the content being read. No entry before it is a request to %s; %s is the first.", from.Cite(), h.v.Host, at.Cite())}
				f.Statements = append(f.Statements, st)
				f.Chain = append(f.Chain, Step{Label: "First contact comes after", Rule: st.Rule, Refs: st.Refs})
			}
			f.Chain = append(f.Chain, Step{Label: h.v.Method + " to " + h.v.Host, Refs: []Ref{at}})
			st, step := b.outcome(send{v: v, held: h, carrier: h.e})
			f.Statements = append(f.Statements, st)
			f.Chain = append(f.Chain, step)
			f.Links = append(f.Links, Link{From: v.sessionEntity(), To: entity(EntityServer, h.v.Host), Via: "first contacted after reading", Refs: []Ref{at}})
			for _, source := range h.v.OutsideSources {
				f.Links = append(f.Links, Link{From: entity(EntityPage, source), To: entity(EntityServer, h.v.Host), Via: "read before first contact", Refs: []Ref{at}})
			}
			f.How = []HowStep{
				{Stage: "Extraction", Refs: []Ref{at}, What: "Took the held request and the sources the recorder listed for it from one entry."},
				{Stage: "Linking", Refs: f.Chain[0].Refs, What: "Rule first-contact-after-reading looked for the entry that records the content being read, and for any earlier request to the host."},
				{Stage: "Finding", Refs: []Ref{at}, What: "Rule " + KindNewServer + " reports a held request whose destination was first contacted after outside content was read."},
			}
			f.Headline, f.Support, f.Key = h.v.Host+" was first contacted after outside content", "Content from "+strings.Join(h.v.OutsideSources, ", ")+" had been read before it", at
			b.findings = append(b.findings, f)
		}
	}
}

// claims reports what the agent said it did that a verdict in the record
// contradicts, beside the command and result the verdict names.
func (b *builder) claims() {
	for _, v := range b.views {
		for _, n := range v.notes {
			for _, c := range n.v.Claims {
				if c.Verdict != "contradicted" {
					continue
				}
				said := ref(n.e, jsonText(c.Claim))
				f := Finding{Kind: KindClaim, Severity: 2, Title: fmt.Sprintf("The agent's claim %q is contradicted", c.Claim)}
				f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{said},
					Text: fmt.Sprintf("The record holds a verdict that a local model gave when the session ended: the agent's claim %q is contradicted by the command %q. The verdict is that model's. It is quoted here, not checked.", c.Claim, c.Command)})
				f.Chain = append(f.Chain, Step{Label: "Claim: " + c.Claim, Refs: []Ref{said}})
				how := []HowStep{{Stage: "Extraction", Refs: []Ref{said}, What: "Took the claim, its verdict and the command the verdict names from one entry."}}
				for i := len(v.actions) - 1; i >= 0; i-- {
					a := v.actions[i]
					if c.Command == "" || a.v.Text != c.Command {
						continue
					}
					ran := ref(a.e, jsonText(c.Command))
					f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{ran}, Text: fmt.Sprintf("The agent told the model it ran %q.", c.Command)})
					f.Chain = append(f.Chain, Step{Label: "Command: " + c.Command, Refs: []Ref{ran}})
					refs := []Ref{said, ran}
					text := fmt.Sprintf("The command the verdict names is %s. The record holds no result for it, so the entries neither support nor contradict the verdict.", ran.Cite())
					if r, ok := v.results[a.v.ID]; ok {
						result := ref(r.e, `"failed":true`, `"failed":false`)
						refs = append(refs, result)
						if r.v.Failed {
							f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{result}, Text: "The agent reported that command as failed."})
							text = fmt.Sprintf("The command the verdict names is %s and its result is %s, which says it failed. The entries agree with the verdict.", ran.Cite(), result.Cite())
						} else {
							f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{result}, Text: "The agent did not report that command as failed."})
							text = fmt.Sprintf("The command the verdict names is %s and its result is %s, which does not say it failed. The entries alone do not show the contradiction; it rests on the model's reading.", ran.Cite(), result.Cite())
						}
						f.Chain = append(f.Chain, Step{Label: "Its result", Refs: []Ref{result}})
					}
					st := Statement{Kind: Derived, Rule: "claim-against-result", Refs: refs, Text: text}
					f.Statements = append(f.Statements, st)
					f.Chain = append(f.Chain, Step{Label: "Claim set against the result", Rule: st.Rule, Refs: refs})
					how = append(how, HowStep{Stage: "Linking", Refs: refs, What: "Rule claim-against-result found the entry for the command the verdict names, and the entry for its result."})
					break
				}
				f.How = append(how, HowStep{Stage: "Finding", Refs: []Ref{said}, What: "Rule " + KindClaim + " reports each claim whose recorded verdict is \"contradicted\"."})
				f.Headline, f.Support, f.Key = "A claim by the agent is contradicted", fmt.Sprintf("%q", c.Claim), said
				b.findings = append(b.findings, f)
			}
		}
	}
}

// installs reports each install the registry check stopped.
func (b *builder) installs() {
	for _, v := range b.views {
		for _, p := range v.packages {
			if !p.v.Blocked {
				continue
			}
			at := ref(p.e, `"blocked":true`)
			name := p.v.Name
			if p.v.Version != "" {
				name += "@" + p.v.Version
			}
			f := Finding{Kind: KindInstall, Severity: 2, Title: fmt.Sprintf("The install of %s was stopped", name)}
			f.Statements = append(f.Statements,
				Statement{Kind: Observed, Refs: []Ref{at}, Text: fmt.Sprintf("The agent asked %s to install %s, and the install was stopped.", p.v.Manager, name)},
				Statement{Kind: Observed, Refs: []Ref{ref(p.e, jsonText(p.v.Facts))}, Text: fmt.Sprintf("What the registry said about it, as recorded: %s.", p.v.Facts)})
			f.Chain = append(f.Chain, Step{Label: p.v.Manager + " install " + name, Refs: []Ref{ref(p.e, jsonPair("name", p.v.Name))}}, Step{Label: "Stopped", Refs: []Ref{at}})
			f.Links = append(f.Links, Link{From: v.sessionEntity(), To: entity(EntityPackage, p.v.Name), Via: "install stopped", Refs: []Ref{at}})
			f.How = []HowStep{
				{Stage: "Extraction", Refs: []Ref{at}, What: "Took the package, what the registry said and whether the install was stopped from one entry."},
				{Stage: "Finding", Refs: []Ref{at}, What: "Rule " + KindInstall + " reports each package check that stopped an install."},
			}
			f.Headline, f.Support, f.Key = "An install of "+name+" was stopped", upperFirst(p.v.Facts), at
			b.findings = append(b.findings, f)
		}
	}
}

// sameDestination reports a host that receives a watched value, or is
// named in outside content, in more than one session.
func (b *builder) sameDestination() {
	type seenIn struct {
		view int
		ref  Ref
		text string
	}
	var hosts []string
	by := map[string][]seenIn{}
	add := func(host string, s seenIn) {
		if host == "" {
			return
		}
		list, known := by[host]
		if !known {
			hosts = append(hosts, host)
		}
		if !slices.ContainsFunc(list, func(o seenIn) bool { return o.view == s.view }) {
			by[host] = append(list, s)
		}
	}
	for _, v := range b.views {
		for _, o := range v.outside {
			for _, m := range o.v.Mentions {
				at := ref(o.e, jsonText(marked(m.Excerpt, m.Host)), jsonText(m.Host))
				add(m.Host, seenIn{v.index, at, fmt.Sprintf("In %s, a reply read from %s named %s.", v.rec.Custody.Label, o.v.Host, m.Host)})
			}
		}
		for _, s := range v.sends {
			add(s.host, seenIn{v.index, s.carriedRef(), fmt.Sprintf("In %s: %s", v.rec.Custody.Label, lowerFirst(s.carried()))})
		}
	}
	for _, host := range hosts {
		list := by[host]
		if len(list) < 2 {
			continue
		}
		f := Finding{Kind: KindSameDestination, Severity: 1, Title: fmt.Sprintf("%s appears in %d sessions", host, len(list))}
		var refs []Ref
		for _, s := range list {
			f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: []Ref{s.ref}, Text: s.text})
			f.Chain = append(f.Chain, Step{Label: b.views[s.view].rec.Custody.Label + ": " + host, Refs: []Ref{s.ref}})
			f.Links = append(f.Links, Link{From: b.views[s.view].sessionEntity(), To: entity(EntityServer, host), Via: "names or sends to", Refs: []Ref{s.ref}})
			refs = append(refs, s.ref)
		}
		st := Statement{Kind: Derived, Rule: "same-destination", Refs: refs,
			Text: fmt.Sprintf("%s is named in outside content or sent a watched value in %d separate sessions.", host, len(list))}
		f.Statements = append(f.Statements, st)
		f.Chain = append(f.Chain, Step{Label: "Same host", Rule: st.Rule, Refs: refs})
		f.How = []HowStep{
			{Stage: "Extraction", Refs: refs, What: "Took each host that was named in outside content or sent a watched value, session by session."},
			{Stage: "Linking", Refs: refs, What: "Rule same-destination compared those hosts across the sessions of the case."},
			{Stage: "Finding", Refs: refs, What: "Rule " + KindSameDestination + " reports a host that turns up in this way in two sessions or more."},
		}
		f.Headline, f.Support, f.Key = fmt.Sprintf("%s appears in %d sessions", host, len(list)), "Named in outside content, or sent a watched value", refs[0]
		b.findings = append(b.findings, f)
	}
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// shortValue names what a set of sends carried in a few words, by kind
// and not by label, for a headline: "A secret was", "2 watched values were".
func shortValue(sends []send) string {
	var ids []string
	for _, s := range sends {
		if !slices.Contains(ids, s.id.ID) {
			ids = append(ids, s.id.ID)
		}
	}
	if len(ids) > 1 {
		return fmt.Sprintf("%d watched values were", len(ids))
	}
	switch sends[0].id.Kind {
	case events.KindSecret:
		return "A secret was"
	case events.KindCanary:
		return "A planted value was"
	}
	return "A detail of yours was"
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

const maxSendsShown = 5

// requests returns one send for each separate request or command among
// sends, marked as one of several when there is more than one.
func requests(sends []send) []send {
	var out []send
	for _, s := range sends {
		if !slices.ContainsFunc(out, func(o send) bool { return o.first() == s.first() }) {
			out = append(out, s)
		}
	}
	for i := range out {
		out[i].one = len(out) > 1
	}
	return out
}

// valueSent reports each watched value sent to a server other than the
// model provider, where no fuller finding has already reported it.
func (b *builder) valueSent() {
	type key struct{ host, id string }
	for _, v := range b.views {
		var keys []key
		groups := map[key][]send{}
		for _, s := range v.sends {
			if b.reported[sent{s.carrier, s.id.ID}] {
				continue
			}
			k := key{s.host, s.id.ID}
			if _, known := groups[k]; !known {
				keys = append(keys, k)
			}
			groups[k] = append(groups[k], s)
		}
		for _, k := range keys {
			sends := groups[k]
			s := sends[0]
			f := Finding{Kind: KindValueSent, Severity: 2, Title: fmt.Sprintf("%s sent to %s", s.id.Label, s.host)}
			if s.id.Kind == events.KindReal {
				// a detail of the user's own, not a secret or a planted value
				f.Severity = 1
			}
			var all []Ref
			for i, one := range sends {
				b.report(one)
				all = append(all, one.carriedRef())
				if i >= maxSendsShown {
					continue
				}
				refs := []Ref{one.carriedRef()}
				if one.request != nil && one.request != one.carrier {
					refs = append([]Ref{ref(one.request, jsonPair("host", one.host))}, refs...)
				}
				f.Statements = append(f.Statements, Statement{Kind: Observed, Refs: refs, Text: one.carried()})
			}
			f.Chain = append(f.Chain, Step{Label: s.what + " to " + s.host, Refs: []Ref{ref(s.first(), jsonPair("host", s.host))}},
				Step{Label: fmt.Sprintf("Carried %q in %s", s.id.Label, whereWords(s.where)), Refs: []Ref{s.carriedRef()}})
			how := []HowStep{{Stage: "Extraction", Refs: all, What: "Took each watched value found in a request or command, and where it was going, from one entry each."}}
			if len(sends) > 1 {
				f.Title += fmt.Sprintf(", %d times", len(sends))
				st := Statement{Kind: Derived, Rule: "grouped-by-destination", Refs: all,
					Text: fmt.Sprintf("%d entries record %q going to %s. The first %d are set out above.", len(sends), s.id.Label, s.host, min(len(sends), maxSendsShown))}
				f.Statements = append(f.Statements, st)
				how = append(how, HowStep{Stage: "Linking", Refs: all, What: "Rule grouped-by-destination counted the entries with the same value and the same host together."})
			}
			if file, ok := b.fromFile(s); ok {
				f.Statements = append(f.Statements, file.statements...)
				f.Links = append(f.Links, file.link)
			}
			for _, one := range requests(sends[:min(len(sends), maxSendsShown)]) {
				st, step := b.outcome(one)
				f.Statements = append(f.Statements, st)
				f.Chain = append(f.Chain, step)
			}
			f.Links = append(f.Links,
				Link{From: entity(EntitySecret, s.id.Label), To: entity(EntityServer, s.host), Via: "carried in a request", Refs: []Ref{s.carriedRef()}},
				Link{From: v.sessionEntity(), To: entity(EntityServer, s.host), Via: "sent a request", Refs: []Ref{ref(s.first())}})
			f.How = append(how, HowStep{Stage: "Finding", Refs: all, What: "Rule " + KindValueSent + " reports a watched value found on its way to a server that is not the model provider, when no other rule has reported it."})
			f.Headline, f.Key = shortValue(sends)+" sent to "+s.host, s.carriedRef()
			f.Support = "Found in " + whereWords(s.where) + ". " + f.Chain[len(f.Chain)-1].Label + "."
			b.findings = append(b.findings, f)
		}
	}
}

// highRisk reports each held action the built-in rules rate as high risk
// that no other finding rests on.
func (b *builder) highRisk() {
	used := map[string]bool{}
	for _, f := range b.findings {
		for _, st := range f.Statements {
			for _, r := range st.Refs {
				used[r.Cite()] = true
			}
		}
	}
	for _, v := range b.views {
		for i := range v.held {
			h := &v.held[i]
			at := ref(h.e, jsonPair("id", h.v.ID))
			if used[at.Cite()] || review.Floor(h.v) != events.RiskHigh {
				continue
			}
			rated := review.ByRules(h.v)
			what := strings.Join(h.v.Command, " ")
			switch {
			case h.v.File != nil:
				what = "a change to " + h.v.File.Path
			case what == "":
				what = h.v.Method + " " + h.v.Host + h.v.Path
			}
			f := Finding{Kind: KindHighRisk, Severity: 2, Title: fmt.Sprintf("%s was attempted, and the rules rate it as high risk", what)}
			f.Statements = append(f.Statements,
				Statement{Kind: Observed, Refs: []Ref{at}, Text: fmt.Sprintf("The agent tried %s, going to %s.", what, h.v.Destination)},
				Statement{Kind: Derived, Rule: "built-in-rating", Refs: []Ref{at},
					Text: fmt.Sprintf("Applied to this entry, the rules that decide what is held rate it as high risk. In their words: %s %s", rated.Summary, rated.Concern)})
			f.Chain = append(f.Chain, Step{Label: what, Refs: []Ref{at}}, Step{Label: "Rated as high risk", Rule: "built-in-rating", Refs: []Ref{at}})
			st, step := b.outcome(send{v: v, held: h, carrier: h.e})
			f.Statements = append(f.Statements, st)
			f.Chain = append(f.Chain, step)
			f.Links = append(f.Links, Link{From: v.sessionEntity(), To: entity(EntityServer, h.v.Destination), Via: "high-risk action", Refs: []Ref{at}})
			f.How = []HowStep{
				{Stage: "Extraction", Refs: []Ref{at}, What: "Took the held action from one entry."},
				{Stage: "Linking", Refs: []Ref{at}, What: "Rule built-in-rating applied the rules that decide what is held to that entry."},
				{Stage: "Finding", Refs: []Ref{at}, What: "Rule " + KindHighRisk + " reports a held action rated as high risk that no other finding rests on."},
			}
			f.Headline, f.Support, f.Key = "A high-risk action was attempted", what, at
			b.findings = append(b.findings, f)
		}
	}
}
