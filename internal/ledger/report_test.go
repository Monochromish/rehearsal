package ledger

import (
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
		"- Input: 150", "- Written to cache: not available", "served from cache: 85%",
		"- `go test ./...` (reported an error)", "- `rm -rf build` (result not reported)",
		"Worth a second look:", "Nothing was held.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report is missing %q:\n%s", want, md)
		}
	}
}

func TestReportSaysNotAvailable(t *testing.T) {
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
	if strings.Count(md, "not available")+strings.Count(md, "Not available") != 9 {
		t.Errorf("want nine \"not available\" (the summary's model, input, models, five token lines, commands), got:\n%s", md)
	}
	for _, wrong := range []string{"Input: 0", "Output: 0", "0%", "It ran no shell commands"} {
		if strings.Contains(md, wrong) {
			t.Errorf("report shows %q where the figure is unknown", wrong)
		}
	}
	if !strings.Contains(l.Summary(), "Which model it used is not available.") {
		t.Errorf("summary = %q, want it to say the model is not available", l.Summary())
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
