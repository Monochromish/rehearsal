package investigate

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Case is everything the rules found in one session or several, with the
// raw entries each finding rests on. The same records always give the
// same Case, byte for byte once encoded: nothing here depends on when it
// was built or on a model.
type Case struct {
	ToolVersion string `json:"tool_version"`
	RuleSet     string `json:"rule_set"`
	// one for each record read, in the order S1, S2, ...
	Records  []Custody `json:"records"`
	Findings []Finding `json:"findings"`
	Kinds    []Kind    `json:"kinds"`
	Rules    []Rule    `json:"rules"`
	// what the records cannot show
	Limits   []string          `json:"limits"`
	Entities []Entity          `json:"entities"`
	Links    []Link            `json:"links"`
	Timeline []SessionTimeline `json:"timeline"`
	Overview []OverviewRow     `json:"overview"`
	// every entry a finding cites, as stored
	Entries []Entry `json:"entries"`

	// an account of the whole case written by a model, when there is one
	Account *Narrative `json:"account,omitempty"`

	// a note in place of the model's writing, when it gave none
	ModelNote string `json:"model_note,omitempty"`

	records []Record
	leaks   int
}

// Leaks is how many requests and commands the findings report as carrying
// a watched value.
func (c *Case) Leaks() int { return c.leaks }

// SessionTimeline is one session's entries in order.
type SessionTimeline struct {
	Session string         `json:"session"`
	Label   string         `json:"label"`
	Items   []TimelineItem `json:"items"`
}

// TimelineItem is one entry on a timeline, and the findings that rest on it.
type TimelineItem struct {
	Seq      int64    `json:"seq"`
	Time     string   `json:"time"`
	Type     string   `json:"type"`
	Label    string   `json:"label"`
	Bad      bool     `json:"bad,omitempty"`
	Findings []string `json:"findings"`
}

// OverviewRow is one session's findings by kind, in the order of Kinds.
type OverviewRow struct {
	Session string     `json:"session"`
	Label   string     `json:"label"`
	Cells   [][]string `json:"cells"`
}

// Investigate applies the rules to the records.
func Investigate(recs []Record, toolVersion string) *Case {
	c := &Case{ToolVersion: toolVersion, RuleSet: RuleSet, Kinds: Kinds, Rules: Rules, records: recs}
	views := make([]*view, len(recs))
	for i := range recs {
		views[i] = extract(i, &recs[i])
		c.Records = append(c.Records, recs[i].Custody)
	}
	b := newBuilder(views)
	covered := b.sentAfterReading()
	b.encoded()
	b.destructive()
	b.newServers(covered)
	b.claims()
	b.installs()
	b.valueSent()
	b.highRisk()
	b.sameDestination()
	c.leaks = b.leaks()

	c.Findings = b.findings
	for i := range c.Findings {
		c.finish(&c.Findings[i])
	}
	slices.SortStableFunc(c.Findings, func(x, y Finding) int {
		return cmp.Or(cmp.Compare(y.Severity, x.Severity), c.compare(x.Refs[0], y.Refs[0]))
	})
	cited := map[string]bool{}
	on := map[string][]string{}
	for i := range c.Findings {
		f := &c.Findings[i]
		f.ID = fmt.Sprintf("F%d", i+1)
		for _, r := range f.Refs {
			on[r.Cite()] = append(on[r.Cite()], f.ID)
			cited[r.Cite()] = true
		}
	}
	c.Entries = []Entry{}
	for _, rec := range recs {
		tl := SessionTimeline{Session: rec.Custody.Session, Label: rec.Custody.Label, Items: []TimelineItem{}}
		row := OverviewRow{Session: rec.Custody.Session, Label: rec.Custody.Label}
		for i := range rec.Entries {
			e := &rec.Entries[i]
			key := ref(e).Cite()
			findings := on[key]
			if findings == nil {
				findings = []string{}
			}
			tl.Items = append(tl.Items, TimelineItem{Seq: e.Seq, Time: e.Time, Type: e.Type, Label: label(e), Bad: e.Problem != "", Findings: findings})
			if cited[key] {
				c.Entries = append(c.Entries, *e)
			}
		}
		for _, k := range Kinds {
			cell := []string{}
			for _, f := range c.Findings {
				if f.Kind == k.Kind && slices.Contains(f.Sessions, rec.Custody.Label) {
					cell = append(cell, f.ID)
				}
			}
			row.Cells = append(row.Cells, cell)
		}
		c.Timeline = append(c.Timeline, tl)
		c.Overview = append(c.Overview, row)
	}
	c.Entities = collect(views)
	if c.Entities == nil {
		c.Entities = []Entity{}
	}
	c.Links = c.links()
	c.Limits = limits(views)
	return c
}

func (c *Case) session(label string) int {
	return slices.IndexFunc(c.records, func(r Record) bool { return r.Custody.Label == label })
}

func (c *Case) compare(x, y Ref) int {
	return cmp.Or(cmp.Compare(c.session(x.Label), c.session(y.Label)), cmp.Compare(x.Seq, y.Seq))
}

// Entry returns the stored entry a reference points at.
func (c *Case) Entry(r Ref) (Entry, bool) {
	i := c.session(r.Label)
	if i < 0 {
		return Entry{}, false
	}
	for _, e := range c.records[i].Entries {
		if e.Seq == r.Seq {
			return e, true
		}
	}
	return Entry{}, false
}

// finish gathers the entries a finding rests on, and says so on the
// finding when any of them, or the record they come from, does not hold.
func (c *Case) finish(f *Finding) {
	var refs []Ref
	add := func(list []Ref) {
		for _, r := range list {
			if !slices.ContainsFunc(refs, func(o Ref) bool { return o.Label == r.Label && o.Seq == r.Seq }) {
				refs = append(refs, r)
			}
		}
	}
	for _, s := range f.Statements {
		add(s.Refs)
	}
	for _, s := range f.Chain {
		add(s.Refs)
	}
	for _, x := range f.Extracts {
		add([]Ref{x.Ref})
	}
	if f.Proof != nil {
		add(f.Proof.Refs)
	}
	for _, h := range f.How {
		add(h.Refs)
	}
	for _, l := range f.Links {
		add(l.Refs)
	}
	slices.SortStableFunc(refs, c.compare)
	f.Refs = refs
	f.Unverified = []Ref{}
	f.Sessions = []string{}
	var problems, unsigned []string
	for _, r := range refs {
		if !slices.Contains(f.Sessions, r.Label) {
			f.Sessions = append(f.Sessions, r.Label)
			if cu := c.records[c.session(r.Label)].Custody; !cu.Verified {
				unsigned = append(unsigned, cu.Label)
			}
		}
		if e, ok := c.Entry(r); ok && e.Problem != "" {
			f.Unverified = append(f.Unverified, r)
			problems = append(problems, r.Cite())
		}
	}
	if len(problems) > 0 {
		f.UnverifiedNote = fmt.Sprintf("Unverified: this finding rests on %s, which cannot be relied on. See the entry for what is wrong with it.", strings.Join(problems, ", "))
	}
	if len(unsigned) > 0 {
		f.RecordNote = fmt.Sprintf("From an unverified record (%s). See Custody for what did not hold.", strings.Join(unsigned, ", "))
	}
	var read []string
	for _, label := range f.Sessions {
		cu := c.records[c.session(label)].Custody
		read = append(read, fmt.Sprintf("%s (%d entries, %d of which fail their check)", cu.Label, cu.Entries, len(cu.BadEntries)))
	}
	intake := HowStep{Stage: "Intake", Refs: f.Unverified,
		What: fmt.Sprintf("Read %s and recomputed every hash. Of the %d entries this finding rests on, %d fail.", strings.Join(read, " and "), len(refs), len(f.Unverified))}
	f.How = append([]HowStep{intake}, f.How...)
}

// links joins the links of every finding into one list for the case.
func (c *Case) links() []Link {
	out := []Link{}
	for _, f := range c.Findings {
		for _, l := range f.Links {
			i := slices.IndexFunc(out, func(o Link) bool {
				return o.From.Kind == l.From.Kind && o.From.Name == l.From.Name && o.To.Kind == l.To.Kind && o.To.Name == l.To.Name && o.Via == l.Via
			})
			if i < 0 {
				out = append(out, Link{From: l.From, To: l.To, Via: l.Via, Refs: slices.Clone(l.Refs)})
				continue
			}
			for _, r := range l.Refs {
				if !slices.ContainsFunc(out[i].Refs, func(o Ref) bool { return o.Label == r.Label && o.Seq == r.Seq }) {
					out[i].Refs = append(out[i].Refs, r)
				}
			}
		}
	}
	return out
}

// limits lists what the records cannot show. The first part is the same
// for every case; the rest depends on how each session was recorded.
func limits(views []*view) []string {
	out := []string{
		"Replies are not kept. For a reply from a server other than the model provider, the record holds its size, the hosts it names, counts of hidden text, and a short excerpt around each newly named address.",
		"Request headers and bodies are not kept. The record holds where a watched value was found and in what form and, for an encoded value, the encoded text, cut short unless the value was planted.",
		"Query strings are not stored. The names of parameters are; their values are not.",
		"Only values that were being watched for can be seen leaving: the user's details, planted values, entries of .env files and well-known credential formats. A secret outside those, or one split up, compressed or encrypted before it was sent, leaves no entry.",
		"The encodings looked for are plain text, URL encoding, base64 and hexadecimal.",
		"What the agent read from files is known only from what it told the model it did. A program the agent ran can read a file without any entry being written.",
		"A verdict on something the agent claimed was written by a local model when the session ended. It is quoted, and set beside the command it names, but not re-derived.",
		"Times are those of the computer that made the record.",
	}
	for _, v := range views {
		label := v.rec.Custody.Label
		switch {
		case v.started == nil:
			out = append(out, label+" has no entry for the start of the session, so which values were being watched for is not known.")
		case v.started.v.Replay:
			out = append(out, label+" is a replay of a recording: no agent ran, nothing was sent, and the watched values in it are stand-ins.")
		case !v.sandboxed:
			out = append(out, label+" ran without the sandbox, so changes the agent made to files were not held or recorded as they happened.")
		}
		if v.started != nil && !v.started.v.Holding {
			out = append(out, label+" ran with holding switched off: requests were recorded and not stopped.")
		}
	}
	return out
}
