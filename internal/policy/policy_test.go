package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rehearsal/internal/events"
)

// write makes a policy file and returns its path.
func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T, org, project string) *Policy {
	t.Helper()
	dir := t.TempDir()
	orgPath := ""
	if org != "" {
		orgPath = write(t, dir, "org.yaml", org)
	}
	if project != "" {
		write(t, dir, ProjectFile, project)
	}
	p, err := Load(dir, orgPath)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadRejectsWhatItCannotApply(t *testing.T) {
	tests := []struct{ name, content, want string }{
		{"not YAML", "rules: [", "not a readable policy"},
		{"an unknown field", "version: 1\nrules:\n  - {match: {host: x}, action: hold}\n", "not a readable policy"},
		{"another version", "version: 2\nrules: []\n", "version is 2"},
		{"no version", "rules: []\n", "version is 0"},
		{"an unknown action", "version: 1\nrules:\n  - {match: {kind: http}, action: permit}\n", "rule 1: action is \"permit\""},
		{"an unknown risk", "version: 1\nrules:\n  - {match: {kind: http}, risk: severe}\n", "rule 1: risk is \"severe\""},
		{"an unknown kind", "version: 1\nrules:\n  - {match: {kind: ssh}, action: hold}\n", "rule 1: kind is \"ssh\""},
		{"a rule that does nothing", "version: 1\nrules:\n  - {match: {kind: http}}\n", "neither an action nor a risk"},
		{"allow with a risk", "version: 1\nrules:\n  - {match: {kind: http}, action: allow, risk: high}\n", "allows the action and also sets a risk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, ProjectFile, tt.content)
			if _, err := Load(dir, ""); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load = %v, want an error containing %q", err, tt.want)
			}
		})
	}

	if _, err := Load(t.TempDir(), filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("a named organisation policy that is not there was accepted")
	}
	p, err := Load(t.TempDir(), "")
	if err != nil || p.Hash != "" || len(p.Project)+len(p.Org) != 0 {
		t.Errorf("no policy files: %+v, %v; want an empty policy with no hash", p, err)
	}
}

const (
	orgHoldsUploads = "version: 1\nrules:\n  - {match: {kind: http, destination: \"uploads.example.net\"}, action: hold, risk: high}\n  - {match: {destination: \"*.blocked.example\"}, action: refuse}\n"
)

func TestDecide(t *testing.T) {
	write := Facts{Kind: events.ActionHTTP, Destination: "api.example.net", Held: true, Floor: events.RiskMedium}
	upload := Facts{Kind: events.ActionHTTP, Destination: "uploads.example.net", Held: true, Floor: events.RiskMedium}
	read := Facts{Kind: events.ActionHTTP, Destination: "docs.example.net"}
	carrying := Facts{Kind: events.ActionHTTP, Destination: "api.example.net", Data: []string{"canary", "Planted secret word"}, Held: true, Floor: events.RiskHigh}
	forcePush := Facts{Kind: events.ActionGitPush, Destination: "github.com:ada/shop.git", Held: true, Floor: events.RiskHigh}
	push := Facts{Kind: events.ActionGitPush, Destination: "github.com:ada/shop.git", Held: true, Floor: events.RiskMedium}

	rule := func(s string) string { return "version: 1\nrules:\n  - " + s + "\n" }
	tests := []struct {
		name         string
		org, project string
		facts        Facts
		action       string
		risk         events.Risk
		ignored      string // part of the reason, "" for none
	}{
		{"no policy: a held action stays held", "", "", write, Hold, "", ""},
		{"no policy: a read is not held", "", "", read, "", "", ""},

		{"tighten: refuse a destination", "", rule(`{match: {destination: "api.example.net"}, action: refuse}`), write, Refuse, "", ""},
		{"tighten: hold a read the built-in rules let through", "", rule(`{match: {destination: "docs.*"}, action: hold}`), read, Hold, "", ""},
		{"tighten: raise the rating", "", rule(`{match: {kind: git-push}, risk: high}`), push, Hold, events.RiskHigh, ""},
		{"tighten: a rule for another destination does not apply", "", rule(`{match: {destination: "other.example"}, action: refuse}`), write, Hold, "", ""},
		{"tighten: a rule for another kind does not apply", "", rule(`{match: {kind: gh}, action: refuse}`), write, Hold, "", ""},

		{"loosen: allow a medium action", "", rule(`{match: {kind: http, destination: "api.example.net"}, action: allow}`), write, Allow, "", ""},
		{"loosen: allow by wildcard", "", rule(`{match: {destination: "github.com:ada/*"}, action: allow}`), push, Allow, "", ""},
		{"loosen: allowing what nothing holds does nothing", "", rule(`{match: {destination: "docs.example.net"}, action: allow}`), read, "", "", ""},

		{"built-in floor: allow cannot pass a force push", "", rule(`{match: {kind: git-push}, action: allow}`), forcePush, Hold, "", "built-in rules always hold"},
		{"built-in floor: allow cannot pass anything carrying a planted value", "", rule(`{match: {destination: "api.example.net"}, action: allow}`), carrying, Hold, "", "built-in rules always hold"},
		{"built-in floor: the organisation cannot loosen it either", rule(`{match: {kind: git-push}, action: allow}`), "", forcePush, Hold, "", "built-in rules always hold"},
		{"built-in floor: a rule on what is carried still tightens", "", rule(`{match: {data: canary}, action: refuse}`), carrying, Refuse, "", ""},

		{"organisation: its hold applies", orgHoldsUploads, "", upload, Hold, events.RiskHigh, ""},
		{"organisation: its refusal applies", orgHoldsUploads, "", Facts{Kind: events.ActionHTTP, Destination: "x.blocked.example"}, Refuse, "", ""},
		{"organisation: a project allow cannot loosen its hold", orgHoldsUploads, rule(`{match: {destination: "uploads.example.net"}, action: allow}`), upload, Hold, events.RiskHigh, "organisation policy holds or refuses"},
		{"organisation: a project allow cannot loosen its refusal", orgHoldsUploads, rule(`{match: {destination: "*"}, action: allow}`), Facts{Kind: events.ActionHTTP, Destination: "x.blocked.example", Held: true, Floor: events.RiskMedium}, Refuse, "", "organisation policy holds or refuses"},
		{"organisation: a project can be stricter than its hold", orgHoldsUploads, rule(`{match: {destination: "uploads.example.net"}, action: refuse}`), upload, Refuse, events.RiskHigh, ""},
		{"organisation: a project hold does not undo its refusal", orgHoldsUploads, rule(`{match: {destination: "*"}, action: hold}`), Facts{Kind: events.ActionHTTP, Destination: "x.blocked.example"}, Refuse, "", ""},
		{"organisation: a project can hold what it allows", rule(`{match: {destination: "api.example.net"}, action: allow}`), rule(`{match: {destination: "api.example.net"}, action: hold}`), write, Hold, "", ""},
		{"organisation: a project may allow what it says nothing about", orgHoldsUploads, rule(`{match: {destination: "api.example.net"}, action: allow}`), write, Allow, "", ""},
		{"organisation: its rating is a floor under the project's", rule(`{match: {kind: git-push}, risk: high}`), rule(`{match: {kind: git-push}, risk: low}`), push, Hold, events.RiskHigh, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := load(t, tt.org, tt.project).Decide(tt.facts)
			if d.Action != tt.action || d.MinRisk != tt.risk {
				t.Errorf("Decide = %q, minimum %q; want %q, minimum %q (rule %q)", d.Action, d.MinRisk, tt.action, tt.risk, d.Rule)
			}
			switch {
			case tt.ignored == "" && len(d.Ignored) != 0:
				t.Errorf("a rule was reported as ignored: %+v", d.Ignored)
			case tt.ignored != "" && (len(d.Ignored) != 1 || !strings.Contains(d.Ignored[0].Why, tt.ignored) || d.Ignored[0].Rule == ""):
				t.Errorf("ignored = %+v, want one rule ignored because %q", d.Ignored, tt.ignored)
			}
		})
	}

	var none *Policy
	if d := none.Decide(write); d.Action != Hold {
		t.Errorf("with no policy at all a held action came back %q", d.Action)
	}
}

func TestCheckFindsRulesWithNoEffect(t *testing.T) {
	p := load(t, orgHoldsUploads, `version: 1
rules:
  - {match: {kind: http, destination: "uploads.example.net"}, action: allow}
  - {match: {data: secret}, action: allow}
  - {match: {kind: npm-publish}, action: allow}
  - {match: {destination: "api.example.net"}, action: allow}
  - {match: {kind: git-push}, risk: high}
`)
	got := p.Check()
	if len(got) != 3 {
		t.Fatalf("Check = %+v, want three rules with no effect", got)
	}
	for i, want := range []string{"organisation policy holds or refuses", "built-in rules always hold", "built-in rules always hold"} {
		if !strings.Contains(got[i].Why, want) || got[i].Source != "project" {
			t.Errorf("rule %d: %+v, want a project rule ignored because %q", i+1, got[i], want)
		}
	}
	if got := load(t, "", "version: 1\nrules:\n  - {match: {destination: \"api.example.net\"}, action: allow}\n").Check(); len(got) != 0 {
		t.Errorf("a rule that works was reported: %+v", got)
	}
}

func TestHashFollowsTheFiles(t *testing.T) {
	a := load(t, "", "version: 1\nrules: []\n")
	b := load(t, "", "version: 1\nrules: []\n")
	c := load(t, "", "version: 1\nrules:\n  - {match: {kind: gh}, action: refuse}\n")
	d := load(t, "version: 1\nrules: []\n", "")
	if a.Hash == "" || a.Hash != b.Hash {
		t.Errorf("the same file gave %q and %q", a.Hash, b.Hash)
	}
	if a.Hash == c.Hash || a.Hash == d.Hash {
		t.Errorf("different policies share a hash: %q %q %q", a.Hash, c.Hash, d.Hash)
	}
}

func TestAppendRuleWritesExactlyOneLine(t *testing.T) {
	rule := Rule{Match: Match{Kind: "git-push", Destination: "github.com:ada/shop.git"}, Action: Allow}
	line := `  - {match: {kind: "git-push", destination: "github.com:ada/shop.git"}, action: allow}` + "\n"

	tests := []struct{ name, before, after string }{
		{"a file with rules", "version: 1\nrules:\n  - {match: {kind: gh}, action: refuse}\n", "version: 1\nrules:\n  - {match: {kind: gh}, action: refuse}\n" + line},
		{"a file with rules in block form and a comment", "# ours\nversion: 1\nrules:\n  - match:\n      kind: gh\n    action: refuse\n", "# ours\nversion: 1\nrules:\n  - match:\n      kind: gh\n    action: refuse\n" + line},
		{"no newline at the end", "version: 1\nrules:\n  - {match: {kind: gh}, action: refuse}", "version: 1\nrules:\n  - {match: {kind: gh}, action: refuse}\n" + line},
		{"an empty list", "version: 1\nrules: []\n", "version: 1\nrules:\n" + line},
		{"no file", "", "version: 1\nrules:\n" + line},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.before != "" {
				write(t, dir, ProjectFile, tt.before)
			}
			if err := AppendRule(dir, rule); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(dir, ProjectFile))
			if err != nil || string(got) != tt.after {
				t.Errorf("file is now:\n%s\nwant:\n%s (%v)", got, tt.after, err)
			}
			p, err := Load(dir, "")
			if err != nil || p.Project[len(p.Project)-1] != rule {
				t.Errorf("the file does not load with the new rule last: %+v, %v", p, err)
			}
		})
	}

	// a file the line cannot be added to is left exactly as it was
	dir := t.TempDir()
	odd := "version: 1\nrules:\n  - {match: {kind: gh}, action: refuse}\nnotes: kept\n"
	write(t, dir, ProjectFile, odd)
	if _, err := Load(dir, ""); err == nil {
		t.Fatal("the odd file was expected not to load")
	}
	if err := AppendRule(dir, rule); err == nil {
		t.Error("a rule was added to a file that does not load")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, ProjectFile)); string(got) != odd {
		t.Errorf("the file was changed:\n%s", got)
	}
}

func TestApprovalsCountSessions(t *testing.T) {
	state := t.TempDir()
	push := Allowed{Kind: events.ActionGitPush, Destination: "github.com:ada/shop.git"}
	other := Allowed{Kind: events.ActionHTTP, Destination: "api.example.net"}
	for session := 1; session <= 3; session++ {
		a, err := LoadApprovals(state)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.Times("/work/shop", push); got != session-1 {
			t.Errorf("before session %d: %d, want %d", session, got, session-1)
		}
		if err := a.Record("/work/shop", []Allowed{push}); err != nil {
			t.Fatal(err)
		}
	}
	a, err := LoadApprovals(state)
	if err != nil {
		t.Fatal(err)
	}
	if a.Times("/work/shop", push) != 3 || a.Times("/work/shop", other) != 0 || a.Times("/work/elsewhere", push) != 0 {
		t.Errorf("counts = %+v; want 3 for the push from this project and nothing else", a.Sessions)
	}
	info, err := os.Stat(filepath.Join(state, approvalsFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the record's file: %v, %v; want it private", info, err)
	}
}
