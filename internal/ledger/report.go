package ledger

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"rehearsal/internal/events"
)

// Report is what was observed about a session beyond where its requests
// went. Every part comes from events. A part with nothing behind it says
// so; it is never filled in with a guess or a zero.
type Report struct {
	BeforeInput BeforeInput `json:"before_input"`
	Models      []ModelUse  `json:"models"`
	Tokens      Tokens      `json:"tokens"`
	// false when no request was in a format whose tool calls can be read,
	// and then Commands says nothing about what the agent did
	CommandsKnown bool      `json:"commands_known"`
	Commands      []Command `json:"commands"`
	// things worth a second look, each a plain sentence
	Flags []string `json:"flags"`
	// written by the local model, and absent when it gave no usable answer
	Notes *events.ReportNotes `json:"notes"`
}

// BeforeInput counts what was sent before the user's first input reached
// the agent: requests the agent made of its own accord at start-up.
type BeforeInput struct {
	// false when input did not pass through rehearse, so nothing can be said
	Watched bool `json:"watched"`
	// false when no input was passed on at all; then every request counts
	InputSeen bool        `json:"input_seen"`
	Requests  int         `json:"requests"`
	Hosts     []HostCount `json:"hosts"`
}

type HostCount struct {
	Host     string `json:"host"`
	Requests int    `json:"requests"`
}

type ModelUse struct {
	Name     string `json:"name"`
	Requests int    `json:"requests"`
}

// Tokens are totals over the replies that stated them. A nil total means
// no reply stated that count.
type Tokens struct {
	Input      *int `json:"input"`
	Output     *int `json:"output"`
	CacheRead  *int `json:"cache_read"`
	CacheWrite *int `json:"cache_write"`
	// what share of the input was served from cache, in percent
	CacheShare *int `json:"cache_share"`
}

// Command is a shell command the agent ran, by its own account.
type Command struct {
	Text string `json:"text"`
	// nil when the agent never reported how it turned out
	Failed *bool `json:"failed"`
}

// reportBuilder gathers the report while Build walks the events.
type reportBuilder struct {
	watched    bool
	inputSeen  bool
	before     map[string]int
	beforeKeys []string
	models     map[string]int
	modelOrder []string
	tokens     Tokens
	known      bool
	actions    []events.AgentAction
	failed     map[string]bool
	notes      *events.ReportNotes
}

func newReportBuilder() *reportBuilder {
	return &reportBuilder{before: map[string]int{}, models: map[string]int{}, failed: map[string]bool{}}
}

func (b *reportBuilder) request(d events.Request) {
	if !b.inputSeen {
		if b.before[d.Host] == 0 {
			b.beforeKeys = append(b.beforeKeys, d.Host)
		}
		b.before[d.Host]++
	}
	if d.Model != "" {
		if b.models[d.Model] == 0 {
			b.modelOrder = append(b.modelOrder, d.Model)
		}
		b.models[d.Model]++
	}
	b.known = b.known || d.Conversation
}

func (b *reportBuilder) usage(d events.ModelUsage) {
	add := func(total **int, n int) {
		if n < 0 {
			return // not stated in this reply
		}
		if *total == nil {
			*total = new(int)
		}
		**total += n
	}
	add(&b.tokens.Input, d.Input)
	add(&b.tokens.Output, d.Output)
	add(&b.tokens.CacheRead, d.CacheRead)
	add(&b.tokens.CacheWrite, d.CacheWrite)
}

var (
	noVerify    = regexp.MustCompile(`\bgit\b.*\b(?:commit|push)\b.*--no-verify\b`)
	destructive = regexp.MustCompile(`(?i)\brm\s+-[a-z]*r[a-z]*f|\brm\s+-[a-z]*f[a-z]*r|\bgit\s+reset\s+--hard\b|\bgit\s+clean\s+-[a-z]*f|\bgit\s+push\b.*(?:--force\b|\s-f\b)|\bdrop\s+(?:table|database)\b|\bmkfs\b|\bdd\s+.*\bof=`)
	testCommand = regexp.MustCompile(`\b(?:go\s+test|npm\s+(?:run\s+)?test|pnpm\s+test|yarn\s+test|pytest|jest|vitest|cargo\s+test|make\s+test)\b`)
	testFile    = regexp.MustCompile(`(?i)(?:_test\.go|\.test\.[a-z]+|\.spec\.[a-z]+|(?:^|/)tests?/|(?:^|/)test_[^/]+\.py)$|(?:^|/)tests?/`)
)

func (b *reportBuilder) build() Report {
	r := Report{
		BeforeInput:   BeforeInput{Watched: b.watched, InputSeen: b.inputSeen, Hosts: []HostCount{}},
		Models:        []ModelUse{},
		Tokens:        b.tokens,
		CommandsKnown: b.known,
		Commands:      []Command{},
		Flags:         []string{},
		Notes:         b.notes,
	}
	for _, host := range b.beforeKeys {
		r.BeforeInput.Requests += b.before[host]
		r.BeforeInput.Hosts = append(r.BeforeInput.Hosts, HostCount{host, b.before[host]})
	}
	for _, name := range b.modelOrder {
		r.Models = append(r.Models, ModelUse{name, b.models[name]})
	}
	slices.SortStableFunc(r.Models, func(x, y ModelUse) int { return y.Requests - x.Requests })

	if t := b.tokens; t.Input != nil && t.CacheRead != nil {
		all := *t.Input + *t.CacheRead
		if t.CacheWrite != nil {
			all += *t.CacheWrite
		}
		if all > 0 {
			share := *t.CacheRead * 100 / all
			r.Tokens.CacheShare = &share
		}
	}

	var failedTest, changedTest string
	for _, a := range b.actions {
		failed, reported := b.failed[a.ID]
		switch a.Kind {
		case "shell":
			c := Command{Text: a.Text}
			if reported {
				c.Failed = &failed
			}
			r.Commands = append(r.Commands, c)
			if noVerify.MatchString(a.Text) {
				r.Flags = append(r.Flags, "A commit or push skipped the project's own checks (--no-verify): "+a.Text)
			}
			if destructive.MatchString(a.Text) {
				r.Flags = append(r.Flags, "A command that deletes or overwrites was run: "+a.Text)
			}
			if reported && failed && failedTest == "" && testCommand.MatchString(a.Text) {
				failedTest = a.Text
			}
		case "edit":
			if changedTest == "" && testFile.MatchString(a.Text) {
				changedTest = a.Text
			}
		}
	}
	if failedTest != "" && changedTest != "" {
		r.Flags = append(r.Flags, fmt.Sprintf("A test file was changed (%s) in the same session as a test command that failed (%s).", changedTest, failedTest))
	}
	return r
}

// CommandLines are the observed shell commands, each with how it turned
// out, as numbered for the model and for the report.
func (l Ledger) CommandLines() []string {
	lines := make([]string, len(l.Report.Commands))
	for i, c := range l.Report.Commands {
		how := "result unknown"
		if c.Failed != nil {
			how = "ok"
			if *c.Failed {
				how = "failed"
			}
		}
		lines[i] = fmt.Sprintf("%s (%s)", c.Text, how)
	}
	return lines
}

// Markdown renders the session report. It names what was watched for by
// label, never by value.
func (l Ledger) Markdown() string {
	var b strings.Builder
	agent := "agent"
	if len(l.Command) > 0 {
		agent = l.Command[0]
	}
	fmt.Fprintf(&b, "# Rehearsal report: %s\n\n", agent)
	b.WriteString("## What left your computer\n\n| Server | Kind | Requests | Bytes sent | Received |\n|---|---|---|---|---|\n")
	for _, s := range l.Servers {
		kind := "other"
		if s.Category == events.CategoryProvider {
			kind = "model provider"
		}
		got := "none of your details"
		if len(s.Received) > 0 {
			labels := make([]string, len(s.Received))
			for i, r := range s.Received {
				labels[i] = r.Label
			}
			got = strings.Join(labels, "; ")
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %s |\n", s.Host, kind, s.Requests, s.Bytes, got)
	}
	if len(l.Servers) == 0 {
		b.WriteString("| (none) | | | | |\n")
	}

	// A part whose figures could not be read is left out. It is never
	// shown as zero, and a heading with "not available" under it only
	// takes up room.
	switch bi := l.Report.BeforeInput; {
	case !bi.Watched:
	case bi.Requests == 0:
		b.WriteString("\n## Before you pressed a key\n\nNo request was sent before your first input reached the agent.\n")
	default:
		b.WriteString("\n## Before you pressed a key\n\n")
		when := "before your first input reached the agent"
		if !bi.InputSeen {
			when = "and no input ever reached the agent, so this is every request"
		}
		fmt.Fprintf(&b, "%s sent %s:\n\n", plural(bi.Requests, "request was", "requests were"), when)
		for _, h := range bi.Hosts {
			fmt.Fprintf(&b, "- %s: %d\n", h.Host, h.Requests)
		}
	}

	if len(l.Report.Models) > 0 {
		b.WriteString("\n## Models that saw your session\n\n")
	}
	for i, m := range l.Report.Models {
		note := ""
		if i > 0 {
			note = " (not the main model of this session)"
		}
		fmt.Fprintf(&b, "- %s: %s%s\n", m.Name, plural(m.Requests, "request"), note)
	}

	t := l.Report.Tokens
	var counts []string
	for _, c := range []struct {
		name string
		n    *int
		unit string
	}{
		{"Input", t.Input, ""}, {"Output", t.Output, ""}, {"Read from cache", t.CacheRead, ""},
		{"Written to cache", t.CacheWrite, ""}, {"Share of input served from cache", t.CacheShare, "%"},
	} {
		if c.n != nil {
			counts = append(counts, fmt.Sprintf("- %s: %d%s\n", c.name, *c.n, c.unit))
		}
	}
	if len(counts) > 0 {
		b.WriteString("\n## Tokens\n\n" + strings.Join(counts, ""))
	}

	if l.Report.CommandsKnown {
		b.WriteString("\n## What the agent did\n\n")
		if len(l.Report.Commands) == 0 {
			b.WriteString("It ran no shell commands.\n")
		}
	}
	for _, c := range l.Report.Commands {
		how := "result not reported"
		if c.Failed != nil {
			how = "ok"
			if *c.Failed {
				how = "reported an error"
			}
		}
		fmt.Fprintf(&b, "- `%s` (%s)\n", strings.ReplaceAll(c.Text, "`", "'"), how)
	}
	if len(l.Report.Flags) > 0 {
		b.WriteString("\nWorth a second look:\n\n")
		for _, f := range l.Report.Flags {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}

	// Only a claim the commands support or contradict is shown. One that
	// nothing bears on tells the reader nothing.
	var claims []events.ReportClaim
	if n := l.Report.Notes; n != nil {
		for _, c := range n.Claims {
			if c.Verdict == "supported" || c.Verdict == "contradicted" {
				claims = append(claims, c)
			}
		}
	}
	if len(claims) > 0 {
		b.WriteString("\n## Claimed versus observed\n\nExperimental, written by an AI model on this computer. It was shown only the agent's final message and the commands above.\n\n")
		for _, c := range claims {
			fmt.Fprintf(&b, "- \"%s\": %s", c.Claim, c.Verdict)
			if c.Command != "" {
				fmt.Fprintf(&b, " (`%s`)", strings.ReplaceAll(c.Command, "`", "'"))
			}
			b.WriteString("\n")
		}
	}

	if len(l.Outside) > 0 {
		b.WriteString("\n## Outside content the agent read\n\n")
		for _, o := range l.Outside {
			fmt.Fprintf(&b, "- %s: %s", o.Host, plural(o.Reads, "reply", "replies"))
			if o.Hidden > 0 {
				fmt.Fprintf(&b, ", with %s a reader would not see", plural(o.Hidden, "hidden item", "hidden items"))
			}
			b.WriteString("\n")
		}
	}

	if len(l.Noted) > 0 {
		b.WriteString("\n## Links the agent followed\n\n")
		for _, n := range l.Noted {
			fmt.Fprintf(&b, "- %s, an address first seen in content from %s: %s that carried nothing of yours. Noted, not held.\n",
				n.Host, n.NamedBy, plural(n.Requests, "plain read", "plain reads"))
		}
	}

	if c := l.Changes; c != nil {
		b.WriteString("\n## What changed in the project\n\n")
		switch {
		case !c.InRepo:
			b.WriteString("The working folder is not in a git repository, so the session's changes to it were not recorded.\n")
		case c.Problem != "":
			fmt.Fprintf(&b, "The changes could not be recorded: %s\n", c.Problem)
		case c.Files == 0:
			b.WriteString("No file in the project changed.\n")
		default:
			fmt.Fprintf(&b, "%s changed: %s added and %s removed. The diff is `changes.diff`, beside this report.\n",
				plural(c.Files, "file"), plural(c.Added, "line"), plural(c.Removed, "line"))
			if c.Truncated {
				b.WriteString("It was too long to keep whole, and is cut short.\n")
			}
			if n := len(l.ChangeNotes); n > 0 {
				fmt.Fprintf(&b, "The local model wrote a sentence on what %s of the changes do. They are shown beside each part in the window. They are that model's words, not something that was observed.\n", plural(n, "part"))
			}
		}
	}

	b.WriteString("\n## Held actions\n\n")
	if len(l.Actions) == 0 {
		b.WriteString("Nothing was held.\n")
	} else {
		b.WriteString(strings.TrimPrefix(l.actionsText(), "\nHELD ACTIONS\n"))
	}

	if len(l.Packages) > 0 {
		b.WriteString("\n## Packages the agent asked to install\n\nOnly packages new to the project are listed. An install was stopped when any package in it was high risk.\n\n")
		for _, p := range l.Packages {
			outcome := "went ahead"
			if p.Blocked {
				outcome = "stopped"
			}
			fmt.Fprintf(&b, "- %s (%s, %s risk, %s)\n", p.Facts, p.Manager, p.Risk, outcome)
		}
	}

	if p := l.Policy; p.Hash != "" || len(p.Ignored) > 0 {
		b.WriteString("\n## Policy\n\n")
		if p.Hash == "" {
			b.WriteString("No policy file was in force.\n")
		} else {
			fmt.Fprintf(&b, "Policy in force: %s\n", p.Hash)
		}
		if len(p.Allowed) > 0 {
			fmt.Fprintf(&b, "\n%s let through by a policy rule and not held:\n\n", plural(len(p.Allowed), "action was", "actions were"))
			for _, a := range p.Allowed {
				fmt.Fprintf(&b, "- %s to %s (rule: `%s`)\n", a.Kind, a.Destination, a.Rule)
			}
		}
		if len(p.Ignored) > 0 {
			b.WriteString("\nPolicy rules that were ignored:\n\n")
			for _, ig := range p.Ignored {
				fmt.Fprintf(&b, "- `%s` (%s policy): %s\n", ig.Rule, ig.Source, ig.Why)
			}
		}
		for _, o := range p.Offers {
			if o.Added {
				fmt.Fprintf(&b, "\nA rule was added to the project's policy file because you accepted it: `%s`\n", o.Line)
			}
		}
	}

	// nothing to say when the ledger was not built from a log
	if r := l.Record; r.Verified && r.Unsigned {
		fmt.Fprintf(&b, "\n## Record\n\nThe log of this session had %d entries when this report was written, each carrying the hash of the one before, and the chain verified. The record is unsigned: no signing key could be found or made when the session started, so entries removed from the end could not be noticed.\n", r.Entries)
	} else if r.Verified {
		fmt.Fprintf(&b, "\n## Record\n\nThe log of this session had %d entries when this report was written, each carrying the hash of the one before, and the chain verified. `rehearse verify` checks it again, with its signature.\n", r.Entries)
	} else if r.BadEntry > 0 {
		fmt.Fprintf(&b, "\n## Record\n\nThe log of this session does not verify, starting at entry %d: %s.\n", r.BadEntry, r.Problem)
	}
	return b.String()
}
