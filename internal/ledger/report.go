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

func plainCount(n *int) string {
	if n == nil {
		return "not available"
	}
	return fmt.Sprint(*n)
}

// Summary is three plain sentences about the session, built from the
// counts alone.
func (l Ledger) Summary() string {
	agent := "The agent"
	if len(l.Command) > 0 {
		agent = l.Command[0]
	}
	first := fmt.Sprintf("%s sent %s to %s, and %s outside the model provider received details of yours.",
		agent, plural(l.Totals.Requests, "request"), plural(l.Totals.Servers, "server"), plural(l.Totals.FlaggedServers, "server"))
	second := "Nothing was held."
	if n := len(l.Actions); n > 0 {
		counts := map[Outcome]int{}
		refused := 0
		for _, a := range l.Actions {
			counts[a.Outcome]++
			if a.Decision == Refused {
				refused++
			}
		}
		second = fmt.Sprintf("%s held: %d allowed, %d refused, %d failed.", plural(n, "action was", "actions were"), counts[Applied], refused, counts[Failed])
	}
	third := "Which model it used is not available."
	if m := l.Report.Models; len(m) > 0 {
		third = fmt.Sprintf("It used the model %s.", m[0].Name)
		if len(m) > 1 {
			third = fmt.Sprintf("It mostly used the model %s, and also %s.", m[0].Name, plural(len(m)-1, "other"))
		}
	}
	if n := len(l.Report.Flags); n > 0 {
		third += fmt.Sprintf(" %s worth a second look.", plural(n, "thing is", "things are"))
	}
	return first + " " + second + " " + third
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

// Facts are the counts behind the report as plain lines, which is all
// the model is given to write its summary from.
func (l Ledger) Facts() string {
	var b strings.Builder
	fmt.Fprintf(&b, "requests sent: %d\nservers contacted: %d\nservers outside the model provider that received the user's details: %d\n",
		l.Totals.Requests, l.Totals.Servers, l.Totals.FlaggedServers)
	counts := map[string]int{}
	for _, a := range l.Actions {
		switch {
		case a.Outcome == Applied:
			counts["allowed and performed"]++
		case a.Outcome == Failed:
			counts["allowed but failed"]++
		case a.Decision == Refused:
			counts["refused by the user"]++
		}
	}
	fmt.Fprintf(&b, "actions held for the user's decision: %d\n", len(l.Actions))
	for _, k := range []string{"allowed and performed", "refused by the user", "allowed but failed"} {
		fmt.Fprintf(&b, "  %s: %d\n", k, counts[k])
	}
	for _, m := range l.Report.Models {
		fmt.Fprintf(&b, "model used: %s (%d requests)\n", m.Name, m.Requests)
	}
	for _, f := range l.Report.Flags {
		fmt.Fprintf(&b, "worth a second look: %s\n", f)
	}
	for _, o := range l.Outside {
		fmt.Fprintf(&b, "content read from another server: %s (%d hidden items)\n", o.Host, o.Hidden)
	}
	return b.String()
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
	if n := l.Report.Notes; n != nil && n.Summary != "" {
		fmt.Fprintf(&b, "%s\n\n(Summary written by an AI model on this computer, from the counts below.)\n\n", n.Summary)
	} else {
		fmt.Fprintf(&b, "%s\n\n", l.Summary())
	}

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

	b.WriteString("\n## Before you pressed a key\n\n")
	switch bi := l.Report.BeforeInput; {
	case !bi.Watched:
		b.WriteString("Not available: your input did not pass through Rehearsal in this session.\n")
	case bi.Requests == 0:
		b.WriteString("No request was sent before your first input reached the agent.\n")
	default:
		when := "before your first input reached the agent"
		if !bi.InputSeen {
			when = "and no input ever reached the agent, so this is every request"
		}
		fmt.Fprintf(&b, "%s sent %s:\n\n", plural(bi.Requests, "request was", "requests were"), when)
		for _, h := range bi.Hosts {
			fmt.Fprintf(&b, "- %s: %d\n", h.Host, h.Requests)
		}
	}

	b.WriteString("\n## Models that saw your session\n\n")
	if len(l.Report.Models) == 0 {
		b.WriteString("Not available: no request to the model provider named a model.\n")
	}
	for i, m := range l.Report.Models {
		note := ""
		if i > 0 {
			note = " (not the main model of this session)"
		}
		fmt.Fprintf(&b, "- %s: %s%s\n", m.Name, plural(m.Requests, "request"), note)
	}

	t := l.Report.Tokens
	share := "not available"
	if t.CacheShare != nil {
		share = fmt.Sprintf("%d%%", *t.CacheShare)
	}
	fmt.Fprintf(&b, "\n## Tokens\n\n- Input: %s\n- Output: %s\n- Read from cache: %s\n- Written to cache: %s\n- Share of input served from cache: %s\n",
		plainCount(t.Input), plainCount(t.Output), plainCount(t.CacheRead), plainCount(t.CacheWrite), share)

	b.WriteString("\n## What the agent did\n\n")
	switch {
	case !l.Report.CommandsKnown:
		b.WriteString("Not available: the agent's conversation was not in a format Rehearsal can read.\n")
	case len(l.Report.Commands) == 0:
		b.WriteString("It ran no shell commands.\n")
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

	if n := l.Report.Notes; n != nil && n.ClaimsChecked {
		b.WriteString("\n## Claimed versus observed\n\nExperimental, written by an AI model on this computer. It was shown only the agent's final message and the commands above.\n\n")
		if len(n.Claims) == 0 {
			b.WriteString("The final message made no claim that the commands bear on.\n")
		}
		for _, c := range n.Claims {
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

	b.WriteString("\n## Held actions\n\n")
	if len(l.Actions) == 0 {
		b.WriteString("Nothing was held.\n")
	} else {
		b.WriteString(strings.TrimPrefix(l.actionsText(), "\nHELD ACTIONS\n"))
	}

	// nothing to say when the ledger was not built from a log
	if r := l.Record; r.Verified {
		fmt.Fprintf(&b, "\n## Record\n\nThe log of this session had %d entries when this report was written, each carrying the hash of the one before, and the chain verified. `rehearse verify` checks it again, with its signature.\n", r.Entries)
	} else if r.BadEntry > 0 {
		fmt.Fprintf(&b, "\n## Record\n\nThe log of this session does not verify, starting at entry %d: %s.\n", r.BadEntry, r.Problem)
	}
	return b.String()
}
