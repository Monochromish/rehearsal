package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"rehearsal/internal/events"
	"rehearsal/internal/investigate"
)

// The command, end to end: custody first, the findings with the entries
// they rest on, the report beside what was read, the same bytes twice,
// and a question answered from the record with no model to ask.
func TestInvestigateCommand(t *testing.T) {
	bin := buildRehearse(t)
	dir := filepath.Join(t.TempDir(), "20260101-090000-aaaaaa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	log, err := events.Open(filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []struct {
		typ     events.Type
		payload any
	}{
		{events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, ProviderHosts: []string{}, Holding: true}},
		{events.TypePackageChecked, events.PackageChecked{Manager: "npm", Name: "shop-utilz", Version: "1.0.0", Risk: events.RiskHigh, Facts: "published 9 hours ago", Blocked: true}},
		{events.TypeSessionEnded, events.SessionEnded{}},
	} {
		if _, err := log.Append(ev.typ, ev.payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	env := cleanEnv(t)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"investigate"}, args...)...)
		cmd.Dir, cmd.Env = t.TempDir(), env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("investigate %v: %v\n%s", args, err, out)
		}
		return string(out)
	}

	out := run("--no-model", dir)
	for _, want := range []string{
		"Custody", "NOT VERIFIED", "3 entries read; each one matches its hash", "not checked: there is no key on this computer",
		"to check it yourself: rehearse verify " + dir,
		"1 finding, most serious first", "F1  The install of shop-utilz@1.0.0 was stopped", "rests on [S1#2]",
		"From an unverified record (S1)", filepath.Join(dir, investigate.ReportFile), "rule set " + investigate.RuleSet,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "Custody") > strings.Index(out, "F1") {
		t.Errorf("custody does not come first:\n%s", out)
	}
	first, err := os.ReadFile(filepath.Join(dir, investigate.ReportFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Custody", "## What the record cannot show", `"blocked":true`, "How this was found:", "**Observed.**"} {
		if !strings.Contains(string(first), want) {
			t.Errorf("the report lacks %q", want)
		}
	}
	if strings.Contains(string(first), "**Written by a model** (") {
		t.Error("with --no-model the report holds writing by a model")
	}
	elsewhere := filepath.Join(t.TempDir(), "again.md")
	run("--no-model", "-o", elsewhere, dir)
	second, err := os.ReadFile(elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("two runs over the same record wrote different reports")
	}

	var c investigate.Case
	if err := json.Unmarshal([]byte(run("--no-model", "--json", dir)), &c); err != nil || len(c.Findings) != 1 || len(c.Entries) != 1 {
		t.Errorf("--json: %v, %d findings, %d entries", err, len(c.Findings), len(c.Entries))
	}

	answer := run("--no-model", "--ask", "Was shop-utilz installed?", dir)
	for _, want := range []string{"Custody", "No local model is available", "[S1#2]", `"name":"shop-utilz"`} {
		if !strings.Contains(answer, want) {
			t.Errorf("the answer lacks %q:\n%s", want, answer)
		}
	}
	nothing := run("--no-model", "--ask", "Which airline flew to Lisbon?", dir)
	if !strings.Contains(nothing, "The record does not contain the answer") {
		t.Errorf("a question the record cannot answer:\n%s", nothing)
	}

	// a folder with nothing to read is an error, not an empty report
	cmd := exec.Command(bin, "investigate", "--no-model", t.TempDir())
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("an empty folder was investigated:\n%s", out)
	}
}

// The second half of bench: every attack's chain is rebuilt, ordinary
// work gives no finding, and the plain-text baseline is printed beside
// what the rules report.
func TestBenchMeasuresTheInvestigation(t *testing.T) {
	cmd := exec.Command(buildRehearse(t), "bench")
	cmd.Dir, cmd.Env = t.TempDir(), cleanEnv(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bench: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Attacks whose chain was reconstructed: 16 of 16. Ordinary cases with no findings: 11 of 11.",
		"leak-across-two-sessions", "sent-after-reading, same-destination", "encoded-value",
		"Leaks: 10 steps in these scenarios carry a watched value out.",
		"Found by searching what was sent for the value's plain text: 8.",
		"Reported by the investigation's rules: 10.",
		"synthetic and were written by the same hand",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("bench output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "NOT RECONSTRUCTED") || strings.Contains(string(out), "UNEXPECTED FINDING") {
		t.Errorf("bench reports a miss in the investigation:\n%s", out)
	}
}
