package ledger

import (
	"reflect"
	"strings"
	"testing"

	"rehearsal/internal/events"
)

func TestReport(t *testing.T) {
	provider := func(id, model string) events.Request {
		return events.Request{ID: id, Host: "api.provider.test", Category: events.CategoryProvider, RequestBytes: 2000, Model: model, Conversation: true}
	}
	evs := log(t,
		events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, InputWatched: true},
		events.TypeRequest, provider("r1", "big-model"),
		events.TypeRequest, events.Request{ID: "r2", Host: "telemetry.test", Category: events.CategoryOther, RequestBytes: 500},
		events.TypeRequest, provider("r3", "small-model"),
		events.TypeFirstInput, events.FirstInput{},
		events.TypeRequest, provider("r4", "big-model"),
		events.TypeRequest, provider("r5", "big-model"),
		events.TypeRequest, events.Request{ID: "r6", Host: "telemetry.test", Category: events.CategoryOther, RequestBytes: -1},
		events.TypeModelUsage, events.ModelUsage{RequestID: "r4", Model: "big-model", Input: 100, Output: 40, CacheRead: 900, CacheWrite: -1},
		events.TypeModelUsage, events.ModelUsage{RequestID: "r5", Model: "big-model", Input: 50, Output: 10, CacheRead: -1, CacheWrite: -1},
		events.TypeAgentAction, events.AgentAction{ID: "t1", Tool: "Bash", Kind: "shell", Text: "go test ./..."},
		events.TypeAgentResult, events.AgentResult{ID: "t1", Failed: true},
		events.TypeAgentAction, events.AgentAction{ID: "t2", Tool: "Edit", Kind: "edit", Text: "/src/shop/checkout_test.go"},
		events.TypeAgentAction, events.AgentAction{ID: "t3", Tool: "Read", Kind: "other"},
		events.TypeAgentAction, events.AgentAction{ID: "t4", Tool: "Bash", Kind: "shell", Text: "git commit -am fix --no-verify"},
		events.TypeAgentResult, events.AgentResult{ID: "t4", Failed: false},
		events.TypeAgentAction, events.AgentAction{ID: "t5", Tool: "Bash", Kind: "shell", Text: "rm -rf build"},
		events.TypeSessionEnded, events.SessionEnded{},
	)
	l, err := Build(evs)
	if err != nil {
		t.Fatal(err)
	}
	r := l.Report

	if bi := r.BeforeInput; !bi.Watched || !bi.InputSeen || bi.Requests != 3 || len(bi.Hosts) != 2 ||
		bi.Hosts[0] != (HostCount{"api.provider.test", 2}) || bi.Hosts[1] != (HostCount{"telemetry.test", 1}) {
		t.Errorf("before input = %+v, want 3 requests: 2 to the provider and 1 to telemetry", bi)
	}
	if len(r.Models) != 2 || r.Models[0] != (ModelUse{"big-model", 3}) || r.Models[1] != (ModelUse{"small-model", 1}) {
		t.Errorf("models = %+v, want big-model with 3 requests first, then small-model with 1", r.Models)
	}
	tok := r.Tokens
	if tok.Input == nil || *tok.Input != 150 || tok.Output == nil || *tok.Output != 50 || tok.CacheRead == nil || *tok.CacheRead != 900 {
		t.Errorf("tokens = input %v output %v cache-read %v, want 150, 50, 900", tok.Input, tok.Output, tok.CacheRead)
	}
	if tok.CacheWrite != nil {
		t.Errorf("cache-write total = %d, want it left unknown: no reply stated it", *tok.CacheWrite)
	}
	if tok.CacheShare == nil || *tok.CacheShare != 85 {
		t.Errorf("cache share = %v, want 85 (900 of 1050)", tok.CacheShare)
	}
	if !r.CommandsKnown || len(r.Commands) != 3 {
		t.Fatalf("commands = %+v (known %t), want the three shell commands", r.Commands, r.CommandsKnown)
	}
	if c := r.Commands[0]; c.Text != "go test ./..." || c.Failed == nil || !*c.Failed {
		t.Errorf("first command = %+v, want go test with a reported failure", c)
	}
	if c := r.Commands[2]; c.Failed != nil {
		t.Errorf("a command with no reported result has Failed = %t, want it unknown", *c.Failed)
	}
	wantFlags := []string{"--no-verify", "deletes or overwrites was run: rm -rf build", "A test file was changed (/src/shop/checkout_test.go)"}
	if len(r.Flags) != len(wantFlags) {
		t.Fatalf("flags = %q, want %d of them", r.Flags, len(wantFlags))
	}
	for i, want := range wantFlags {
		if !strings.Contains(r.Flags[i], want) {
			t.Errorf("flag %d = %q, want it to contain %q", i, r.Flags[i], want)
		}
	}
	if got := l.Servers[0].Bytes + l.Servers[1].Bytes; got != 4*2000+500 {
		t.Errorf("bytes sent = %d, want 8500 (a request of unknown size adds nothing)", got)
	}

	md := l.Markdown()
	for _, want := range []string{
		"# Rehearsal report: agent",
		"3 requests were sent before your first input reached the agent",
		"- big-model: 3 requests\n",
		"- small-model: 1 request (not the main model of this session)",
		"- Input: 150", "served from cache: 85%",
		"- `go test ./...` (reported an error)", "- `rm -rf build` (result not reported)",
		"Worth a second look:", "Nothing was held.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report is missing %q:\n%s", want, md)
		}
	}
	// the one count the provider did not report is left out, not shown as zero
	if strings.Contains(md, "Written to cache") || strings.Contains(md, "not available") {
		t.Errorf("report shows a figure that was not read:\n%s", md)
	}
}

// A figure that could not be read is left out, with its heading. It is
// never shown as zero and never as a line saying it is not available.
func TestReportLeavesOutWhatCouldNotBeRead(t *testing.T) {
	evs := log(t,
		events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, InputWatched: false},
		events.TypeRequest, events.Request{ID: "r1", Host: "api.provider.test", Category: events.CategoryProvider, RequestBytes: 10},
		events.TypeSessionEnded, events.SessionEnded{},
	)
	l, err := Build(evs)
	if err != nil {
		t.Fatal(err)
	}
	r := l.Report
	if r.CommandsKnown || len(r.Models) != 0 || r.Tokens.Input != nil || r.Tokens.CacheShare != nil || r.BeforeInput.Watched {
		t.Errorf("report = %+v, want nothing known about commands, models, tokens or input", r)
	}
	md := l.Markdown()
	for _, wrong := range []string{
		"not available", "Not available", "Input: 0", "Output: 0", "0%", "It ran no shell commands",
		"## Before you pressed a key", "## Models that saw your session", "## Tokens", "## What the agent did",
	} {
		if strings.Contains(md, wrong) {
			t.Errorf("report shows %q where the figure is unknown:\n%s", wrong, md)
		}
	}
	// what was read is still there
	if !strings.Contains(md, "## What left your computer") || !strings.Contains(md, "api.provider.test") {
		t.Errorf("report lost the part that was read:\n%s", md)
	}
}

func TestReportWhenNoInputEverArrives(t *testing.T) {
	evs := log(t,
		events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, InputWatched: true},
		events.TypeRequest, events.Request{ID: "r1", Host: "a.test", Category: events.CategoryOther},
		events.TypeRequest, events.Request{ID: "r2", Host: "a.test", Category: events.CategoryOther},
	)
	l, err := Build(evs)
	if err != nil {
		t.Fatal(err)
	}
	if bi := l.Report.BeforeInput; bi.InputSeen || bi.Requests != 2 {
		t.Errorf("before input = %+v, want both requests counted and no input seen", bi)
	}
	if !strings.Contains(l.Markdown(), "no input ever reached the agent, so this is every request") {
		t.Errorf("report does not say that no input arrived:\n%s", l.Markdown())
	}
}

func TestIntegrityPatterns(t *testing.T) {
	tests := []struct {
		command               string
		noVerify, destructive bool
	}{
		{"git commit -m x --no-verify", true, false},
		{"git push --no-verify origin main", true, false},
		{"git -C repo commit --no-verify -m x", true, false},
		{"git commit -m 'mention --no-verify in text'", true, false},
		{"git commit -m x", false, false},
		{"npm run lint --no-verify", false, false},
		{"rm -rf node_modules", false, true},
		{"rm -fr /tmp/x", false, true},
		{"git reset --hard HEAD~3", false, true},
		{"git clean -fd", false, true},
		{"git push --force origin main", false, true},
		{"git push -f", false, true},
		{"psql -c 'DROP TABLE users'", false, true},
		{"dd if=/dev/zero of=/dev/sda", false, true},
		{"rm notes.txt", false, false},
		{"git push origin main", false, false},
		{"ls -rf", false, false},
		{"grep -rf patterns.txt src", false, false},
	}
	for _, tt := range tests {
		if got := noVerify.MatchString(tt.command); got != tt.noVerify {
			t.Errorf("%q: --no-verify flag = %t, want %t", tt.command, got, tt.noVerify)
		}
		if got := destructive.MatchString(tt.command); got != tt.destructive {
			t.Errorf("%q: destructive = %t, want %t", tt.command, got, tt.destructive)
		}
	}
}

// "Content read from other servers" is what the agent fetched. The shapes
// below are the ones a real session logged: its own telemetry and update
// check, and Rehearsal looking a package up.
func TestOnlyWhatTheAgentFetchedIsListedAsReadContent(t *testing.T) {
	get := func(id, host, path string) events.Request {
		return events.Request{ID: id, Host: host, Method: "GET", Path: path, Category: events.CategoryOther}
	}
	evs := log(t,
		events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}},
		events.TypeRequest, get("r1", "downloads.agent.test", "/releases/latest"),
		events.TypeOutsideContent, events.OutsideContent{RequestID: "r1", Host: "downloads.agent.test", Background: true},
		events.TypeRequest, events.Request{ID: "r2", Host: "telemetry.test", Method: "POST", Path: "/api/v2/logs", Category: events.CategoryOther},
		events.TypeOutsideContent, events.OutsideContent{RequestID: "r2", Host: "telemetry.test"},
		events.TypeRequest, get("r3", "registry.test", "/shop-utilz"),
		events.TypeOutsideContent, events.OutsideContent{RequestID: "r3", Host: "registry.test"},
		events.TypePackageChecked, events.PackageChecked{Manager: "npm", Name: "shop-utilz", Risk: events.RiskHigh, Facts: "published 9 hours ago", Blocked: true},
		events.TypeRequest, get("r4", "registry.test", "/@scope%2fname"),
		events.TypeOutsideContent, events.OutsideContent{RequestID: "r4", Host: "registry.test"},
		events.TypePackageChecked, events.PackageChecked{Manager: "npm", Name: "@scope/name", Risk: events.RiskLow, Facts: "published 3 years ago"},
		events.TypeRequest, get("r5", "pypi.test", "/pypi/requests/json"),
		events.TypeOutsideContent, events.OutsideContent{RequestID: "r5", Host: "pypi.test"},
		events.TypePackageChecked, events.PackageChecked{Manager: "pip", Name: "requests", Risk: events.RiskLow, Facts: "published 2 years ago"},
		events.TypeRequest, get("r6", "docs.test", "/setup"),
		events.TypeOutsideContent, events.OutsideContent{RequestID: "r6", Host: "docs.test", Comments: 1, HiddenExcerpt: "do this first"},
		events.TypeRequest, get("r7", "registry.test", "/left-pad"),
		events.TypeOutsideContent, events.OutsideContent{RequestID: "r7", Host: "registry.test"},
		events.TypeSessionEnded, events.SessionEnded{},
	)
	l, err := Build(evs)
	if err != nil {
		t.Fatal(err)
	}
	// docs.test is a page the agent fetched, and so is a registry page that
	// no package check asked for
	want := []OutsideSource{
		{Host: "docs.test", Reads: 1, Hidden: 1, HiddenExcerpt: "do this first"},
		{Host: "registry.test", Reads: 1},
	}
	if !reflect.DeepEqual(l.Outside, want) {
		t.Errorf("outside = %+v\nwant      %+v", l.Outside, want)
	}
}

func TestClaimsNothingBearsOnAreNotShown(t *testing.T) {
	notes := func(claims ...events.ReportClaim) string {
		evs := log(t,
			events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}},
			events.TypeReportNotes, events.ReportNotes{ClaimsChecked: true, Claims: claims},
			events.TypeSessionEnded, events.SessionEnded{},
		)
		l, err := Build(evs)
		if err != nil {
			t.Fatal(err)
		}
		return l.Markdown()
	}
	none := events.ReportClaim{Claim: "I tidied the notes", Verdict: "nothing bears on it"}
	if md := notes(none); strings.Contains(md, "Claimed versus observed") || strings.Contains(md, "tidied") {
		t.Errorf("a report with no claim to show still has the section:\n%s", md)
	}
	md := notes(none, events.ReportClaim{Claim: "All tests pass", Verdict: "contradicted", Command: "go test ./..."})
	if !strings.Contains(md, "## Claimed versus observed") || !strings.Contains(md, `"All tests pass": contradicted`) || strings.Contains(md, "tidied") {
		t.Errorf("want only the contradicted claim under its heading:\n%s", md)
	}
}

func TestAnUnsignedRecordSaysSo(t *testing.T) {
	for _, unsigned := range []bool{false, true} {
		evs := log(t,
			events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, Unsigned: unsigned},
			events.TypeSessionEnded, events.SessionEnded{},
		)
		l, err := Build(evs)
		if err != nil {
			t.Fatal(err)
		}
		if l.Record.Unsigned != unsigned {
			t.Errorf("record unsigned = %v, want %v", l.Record.Unsigned, unsigned)
		}
		l.Record.Verified, l.Record.Entries = true, 2 // as FromLog would find
		if md := l.Markdown(); strings.Contains(md, "The record is unsigned") != unsigned {
			t.Errorf("unsigned %v, but the report's record section reads:\n%s", unsigned, md[strings.Index(md, "## Record"):])
		}
	}
}
