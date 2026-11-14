// Package ledger turns the event log into the summary of which servers
// were contacted, which identifiers each one received, and what became of
// each held action.
package ledger

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"rehearsal/internal/events"
)

// Phase is where a session stands.
type Phase string

const (
	PhaseRunning   Phase = "running"   // the agent is still working
	PhaseReviewing Phase = "reviewing" // held actions are being described
	PhaseDeciding  Phase = "deciding"  // waiting for the user
	PhaseApplying  Phase = "applying"
	PhaseFinished  Phase = "finished"
)

type Ledger struct {
	Command   []string   `json:"command"`
	WorkDir   string     `json:"work_dir"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	ExitCode  *int       `json:"exit_code"`
	Phase     Phase      `json:"phase"`
	// whether actions are being held this session
	Holding bool   `json:"holding"`
	Totals  Totals `json:"totals"`
	// every identifier we looked for, sent or not
	Watching []events.Identifier `json:"watching"`
	Servers  []Server            `json:"servers"`
	// held actions, in the order they were held
	Actions []Action `json:"actions"`
	// servers other than the model provider whose replies were read
	Outside []OutsideSource `json:"outside"`
	Report  Report          `json:"report"`
	// the version of rehearse that recorded the session
	ToolVersion string `json:"tool_version"`
	// commits made in the working folder's repository during the session;
	// nil until the agent has exited
	Commits *events.CommitsCreated `json:"commits"`
	// whether the event log's chain of hashes recomputes. Set by FromLog.
	Record Record `json:"record"`
	// how the agent was confined; nil when it ran without a sandbox
	Sandbox *events.SandboxStarted `json:"sandbox"`
	// writes to paths that decide what runs on this machine, as they were
	// noticed
	Sensitive []events.SensitiveWrite `json:"sensitive"`
	Policy    Policy                  `json:"policy"`
	// packages the agent asked to install that were new to the project
	Packages []events.PackageChecked `json:"packages"`
	// true when this is a recording being played back, not a live session
	Replay bool `json:"replay"`
}

// Policy is what the session's policy files did.
type Policy struct {
	// identifies the files in force; empty when there were none
	Hash string `json:"hash"`
	// actions a rule let through that would otherwise have been held
	Allowed []events.PolicyAllowed `json:"allowed"`
	// rules that were not allowed to apply
	Ignored []events.PolicyIgnored `json:"ignored"`
	// rules offered to the user, and whether each was added
	Offers []Offer `json:"offers"`
}

type Offer struct {
	events.RuleOffered
	Added bool `json:"added"`
}

// Record is the state of the log the ledger was built from.
type Record struct {
	Verified bool  `json:"verified"`
	Entries  int64 `json:"entries"`
	// the first entry that does not match, when not verified
	BadEntry int64  `json:"bad_entry"`
	Problem  string `json:"problem,omitempty"`
}

// FromLog builds the ledger from everything in the log, and notes whether
// the log's chain of hashes still holds.
func FromLog(log *events.Log) (Ledger, error) {
	evs, err := log.Since(0)
	if err != nil {
		return Ledger{}, err
	}
	l, err := Build(evs)
	if err != nil {
		return l, err
	}
	chain, err := log.Check()
	if err != nil {
		return l, err
	}
	l.Record = Record{Verified: chain.OK(), Entries: chain.Entries, BadEntry: chain.BadEntry, Problem: chain.Problem}
	return l, nil
}

// OutsideSource is one server the agent read content from.
type OutsideSource struct {
	Host  string `json:"host"`
	Reads int    `json:"reads"`
	// hidden characters and comments found in what it sent
	Hidden int `json:"hidden"`
	// a few words from the first hidden comment
	HiddenExcerpt string `json:"hidden_excerpt,omitempty"`
}

type Totals struct {
	Requests int `json:"requests"`
	Servers  int `json:"servers"`
	// distinct (server, identifier) pairs
	IdentifiersSent int `json:"identifiers_sent"`
	FlaggedServers  int `json:"flagged_servers"`
	Held            int `json:"held"`
}

type Server struct {
	Host     string          `json:"host"`
	Category events.Category `json:"category"`
	Requests int             `json:"requests"`
	// bytes of request bodies sent to it
	Bytes     int64     `json:"bytes"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// not a model provider, yet it received an identifier
	Flagged  bool       `json:"flagged"`
	Received []Received `json:"received"`
}

type Received struct {
	events.Identifier
	Requests int            `json:"requests"`
	Where    []events.Where `json:"where"`
}

type Decision string

const (
	Undecided Decision = ""
	Approved  Decision = "approved"
	Refused   Decision = "refused"
)

type Outcome string

const (
	NoOutcome Outcome = ""
	Applied   Outcome = "applied"
	Failed    Outcome = "failed"
	// approved, but an earlier action failed first
	NotApplied Outcome = "not_applied"
)

type Action struct {
	events.ActionHeld
	Carries  []events.Identifier    `json:"carries"`
	Review   *events.ActionReviewed `json:"review"`
	Decision Decision               `json:"decision"`
	Outcome  Outcome                `json:"outcome"`
	// what the real program or server said, or why the action failed
	Detail string `json:"detail"`
}

func Build(evs []events.Event) (Ledger, error) {
	l := Ledger{
		Phase: PhaseRunning, Watching: []events.Identifier{}, Servers: []Server{}, Actions: []Action{},
		Outside: []OutsideSource{}, Sensitive: []events.SensitiveWrite{},
		Policy:   Policy{Allowed: []events.PolicyAllowed{}, Ignored: []events.PolicyIgnored{}, Offers: []Offer{}},
		Packages: []events.PackageChecked{},
	}
	byID := map[string]events.Identifier{}
	servers := map[string]*Server{}
	seenIn := map[string]map[string]bool{} // host+identifier -> request IDs
	actions := map[string]int{}            // action ID -> index in l.Actions
	report := newReportBuilder()
	action := func(ev events.Event, id string) (*Action, error) {
		i, ok := actions[id]
		if !ok {
			return nil, fmt.Errorf("event %d (%s) names an action that was never held: %q", ev.Seq, ev.Type, id)
		}
		return &l.Actions[i], nil
	}

	for _, ev := range evs {
		switch ev.Type {
		case events.TypeSessionStarted:
			var d events.SessionStarted
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			l.Command, l.WorkDir, l.StartedAt, l.Watching, l.Holding = d.Command, d.WorkDir, ev.Time, d.Identifiers, d.Holding
			l.ToolVersion, l.Policy.Hash, l.Replay = d.ToolVersion, d.PolicyHash, d.Replay
			report.watched = d.InputWatched
			for _, id := range d.Identifiers {
				byID[id.ID] = id
			}
		case events.TypeRequest:
			var d events.Request
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			s := servers[d.Host]
			if s == nil {
				s = &Server{Host: d.Host, Category: d.Category, FirstSeen: ev.Time, Received: []Received{}}
				servers[d.Host] = s
			}
			s.Requests++
			s.LastSeen = ev.Time
			if d.RequestBytes > 0 {
				s.Bytes += d.RequestBytes
			}
			l.Totals.Requests++
			report.request(d)
		case events.TypeFirstInput:
			report.inputSeen = true
		case events.TypePackageChecked:
			var d events.PackageChecked
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			l.Packages = append(l.Packages, d)
		case events.TypePolicyAllowed:
			var d events.PolicyAllowed
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			l.Policy.Allowed = append(l.Policy.Allowed, d)
		case events.TypePolicyIgnored:
			var d events.PolicyIgnored
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			l.Policy.Ignored = append(l.Policy.Ignored, d)
		case events.TypeRuleOffered:
			var d events.RuleOffered
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			l.Policy.Offers = append(l.Policy.Offers, Offer{RuleOffered: d})
		case events.TypeRuleAdded:
			var d events.RuleAdded
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			for i := range l.Policy.Offers {
				if l.Policy.Offers[i].ID == d.ID {
					l.Policy.Offers[i].Added = true
				}
			}
		case events.TypeSandboxStarted:
			var d events.SandboxStarted
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			l.Sandbox = &d
		case events.TypeSensitiveWrite:
			var d events.SensitiveWrite
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			l.Sensitive = append(l.Sensitive, d)
		case events.TypeCommitsCreated:
			var d events.CommitsCreated
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			l.Commits = &d
		case events.TypeModelUsage:
			var d events.ModelUsage
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			report.usage(d)
		case events.TypeAgentAction:
			var d events.AgentAction
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			report.actions = append(report.actions, d)
		case events.TypeAgentResult:
			var d events.AgentResult
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			report.failed[d.ID] = d.Failed
		case events.TypeReportNotes:
			var d events.ReportNotes
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			report.notes = &d
		case events.TypeIdentifierSeen:
			var d events.IdentifierSeen
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			s, id := servers[d.Host], byID[d.IdentifierID]
			if s == nil || id.ID == "" {
				return l, fmt.Errorf("event %d names an unknown server or identifier (%s, %s)", ev.Seq, d.Host, d.IdentifierID)
			}
			r := findReceived(s, id)
			key := d.Host + "\x00" + d.IdentifierID
			if seenIn[key] == nil {
				seenIn[key] = map[string]bool{}
			}
			if !seenIn[key][d.RequestID] {
				seenIn[key][d.RequestID] = true
				r.Requests++
			}
			if !containsWhere(r.Where, d.Where) {
				r.Where = append(r.Where, d.Where)
			}
		case events.TypeSessionEnded:
			var d events.SessionEnded
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			t, code := ev.Time, d.ExitCode
			l.EndedAt, l.ExitCode = &t, &code

		case events.TypeOutsideContent:
			var d events.OutsideContent
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			i := slices.IndexFunc(l.Outside, func(o OutsideSource) bool { return o.Host == d.Host })
			if i < 0 {
				l.Outside = append(l.Outside, OutsideSource{Host: d.Host})
				i = len(l.Outside) - 1
			}
			l.Outside[i].Reads++
			l.Outside[i].Hidden += d.ZeroWidth + d.TagChars + d.Comments
			if l.Outside[i].HiddenExcerpt == "" {
				l.Outside[i].HiddenExcerpt = d.HiddenExcerpt
			}

		case events.TypeActionHeld:
			var d events.ActionHeld
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			a := Action{ActionHeld: d, Carries: []events.Identifier{}}
			for _, id := range d.IdentifierIDs {
				if known, ok := byID[id]; ok {
					a.Carries = append(a.Carries, known)
				}
			}
			actions[d.ID] = len(l.Actions)
			l.Actions = append(l.Actions, a)
		case events.TypeActionReviewed:
			var d events.ActionReviewed
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			a, err := action(ev, d.ActionID)
			if err != nil {
				return l, err
			}
			a.Review = &d
		case events.TypeActionApproved, events.TypeActionRefused:
			var d events.ActionRefused // an approval carries only the action's ID
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			a, err := action(ev, d.ActionID)
			if err != nil {
				return l, err
			}
			a.Decision = Approved
			if ev.Type == events.TypeActionRefused {
				a.Decision = Refused
				if d.Policy != "" {
					a.Detail = "Refused by the policy for this project. Rule: " + d.Policy
				}
			}
		case events.TypeActionApplied:
			var d events.ActionApplied
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			a, err := action(ev, d.ActionID)
			if err != nil {
				return l, err
			}
			a.Outcome, a.Detail = Applied, d.Detail
		case events.TypeActionFailed:
			var d events.ActionFailed
			if err := decode(ev, &d); err != nil {
				return l, err
			}
			a, err := action(ev, d.ActionID)
			if err != nil {
				return l, err
			}
			a.Outcome, a.Detail = Failed, strings.TrimSpace(d.Reason+"\n"+d.Detail)
		}
	}

	for _, s := range servers {
		s.Flagged = s.Category != events.CategoryProvider && len(s.Received) > 0
		if s.Flagged {
			l.Totals.FlaggedServers++
		}
		l.Totals.IdentifiersSent += len(s.Received)
		l.Servers = append(l.Servers, *s)
	}
	l.Totals.Servers = len(l.Servers)
	sort.Slice(l.Servers, func(i, j int) bool {
		a, b := l.Servers[i], l.Servers[j]
		if a.Flagged != b.Flagged {
			return a.Flagged
		}
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return a.Host < b.Host
	})

	l.Totals.Held = len(l.Actions)
	l.Report = report.build()
	l.Phase = phase(l)
	if l.Phase == PhaseFinished {
		for i := range l.Actions {
			if a := &l.Actions[i]; a.Decision == Approved && a.Outcome == NoOutcome {
				a.Outcome = NotApplied
			}
		}
	}
	return l, nil
}

// phase works out where the session stands. Applying stops at the first
// failure, so one failed action means it is over.
func phase(l Ledger) Phase {
	if l.EndedAt == nil {
		return PhaseRunning
	}
	waiting, failed := false, false
	for _, a := range l.Actions {
		switch {
		case a.Review == nil:
			return PhaseReviewing
		case a.Decision == Undecided:
			return PhaseDeciding
		case a.Outcome == Failed:
			failed = true
		case a.Decision == Approved && a.Outcome == NoOutcome:
			waiting = true
		}
	}
	if waiting && !failed {
		return PhaseApplying
	}
	return PhaseFinished
}

func decode(ev events.Event, into any) error {
	if err := json.Unmarshal(ev.Data, into); err != nil {
		return fmt.Errorf("event %d (%s) is unreadable: %w", ev.Seq, ev.Type, err)
	}
	return nil
}

func findReceived(s *Server, id events.Identifier) *Received {
	for i := range s.Received {
		if s.Received[i].ID == id.ID {
			return &s.Received[i]
		}
	}
	s.Received = append(s.Received, Received{Identifier: id, Where: []events.Where{}})
	return &s.Received[len(s.Received)-1]
}

func containsWhere(list []events.Where, w events.Where) bool {
	for _, x := range list {
		if x == w {
			return true
		}
	}
	return false
}

// Text is the plain-text ledger printed in the terminal.
func (l Ledger) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nRehearsal ledger: %s to %s\n",
		plural(l.Totals.Requests, "request"), plural(l.Totals.Servers, "server"))
	groups := []struct {
		title string
		cat   events.Category
	}{
		{"MODEL PROVIDER", events.CategoryProvider},
		{"EVERYTHING ELSE", events.CategoryOther},
	}
	for _, g := range groups {
		fmt.Fprintf(&b, "\n%s\n", g.title)
		n := 0
		for _, s := range l.Servers {
			if s.Category != g.cat {
				continue
			}
			n++
			mark := " "
			if s.Flagged {
				mark = "!"
			}
			fmt.Fprintf(&b, " %s %-44s %s\n", mark, s.Host, plural(s.Requests, "request"))
			for _, r := range s.Received {
				kind := "yours"
				if r.Kind == events.KindCanary {
					kind = "planted"
				}
				fmt.Fprintf(&b, "       received: %s (%s) in %s\n", r.Label, kind, plural(r.Requests, "request"))
			}
		}
		if n == 0 {
			b.WriteString("   (none)\n")
		}
	}
	if l.Totals.FlaggedServers > 0 {
		fmt.Fprintf(&b, "\n! %s outside the model provider received your details.\n", plural(l.Totals.FlaggedServers, "server"))
	}
	if len(l.Actions) > 0 {
		b.WriteString(l.actionsText())
	}
	return b.String()
}

func (l Ledger) actionsText() string {
	var b strings.Builder
	counts := map[string]int{}
	b.WriteString("\nHELD ACTIONS\n")
	for _, a := range l.Actions {
		state := "waiting"
		switch {
		case a.Outcome == Applied:
			state = "applied"
		case a.Outcome == Failed:
			state = "FAILED"
		case a.Outcome == NotApplied:
			state = "not applied"
		case a.Decision == Refused:
			state = "refused"
		}
		counts[state]++
		exact := strings.Join(a.Command, " ")
		if a.Kind == events.ActionHTTP {
			exact = fmt.Sprintf("%s %s%s", a.Method, a.Host, a.Path)
		}
		// the explanation never appears without the exact text beside it
		if a.Review != nil {
			fmt.Fprintf(&b, "   %-12s %s\n                %s\n", state, a.Review.Summary, exact)
		} else {
			fmt.Fprintf(&b, "   %-12s %s\n", state, exact)
		}
		if a.Outcome == Failed {
			for _, line := range strings.Split(a.Detail, "\n") {
				fmt.Fprintf(&b, "                %s\n", line)
			}
		}
	}
	fmt.Fprintf(&b, "\n%s held: %d applied, %d refused, %d failed", plural(len(l.Actions), "action"), counts["applied"], counts["refused"], counts["FAILED"])
	if n := counts["not applied"]; n > 0 {
		fmt.Fprintf(&b, ", %d not applied because an earlier one failed", n)
	}
	if n := counts["waiting"]; n > 0 {
		fmt.Fprintf(&b, ", %d undecided", n)
	}
	b.WriteString("\n")
	return b.String()
}

// plural counts things in words. The plural is one with an s added unless
// many says otherwise.
func plural(n int, one string, many ...string) string {
	switch {
	case n == 1:
		return "1 " + one
	case len(many) > 0:
		return fmt.Sprintf("%d %s", n, many[0])
	}
	return fmt.Sprintf("%d %ss", n, one)
}
