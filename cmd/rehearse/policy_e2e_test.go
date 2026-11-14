package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
	"rehearsal/internal/policy"
)

func writePolicy(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rules(lines ...string) string {
	return "version: 1\nrules:\n  - " + strings.Join(lines, "\n  - ") + "\n"
}

// ledgerNow reads the ledger from the page API while the session waits.
func (s *session) ledgerNow() ledger.Ledger {
	s.t.Helper()
	res, err := http.Get(s.url + "/api/ledger")
	if err != nil {
		s.t.Fatal(err)
	}
	body, readErr := io.ReadAll(res.Body)
	if err := errors.Join(readErr, res.Body.Close()); err != nil {
		s.t.Fatal(err)
	}
	var l ledger.Ledger
	if err := json.Unmarshal(body, &l); err != nil {
		s.t.Fatalf("ledger: %v\n%s", err, body)
	}
	return l
}

func post(address, path string) string {
	return fmt.Sprintf(`echo "status $(curl -sS -o /dev/null -w '%%{http_code}' --data-binary hello %s%s)"`, address, path)
}

func TestPolicyTightens(t *testing.T) {
	needCurl(t)
	srv := newUploads(t)
	host := strings.TrimPrefix(srv.address(), "http://")
	work := t.TempDir()
	writePolicy(t, filepath.Join(work, policy.ProjectFile), rules(
		fmt.Sprintf(`{match: {kind: http, destination: %q}, action: refuse}`, host),
	))
	other := newUploads(t)
	s := start(t, work, post(srv.address(), "/upload")+"; "+post(other.address(), "/elsewhere"))
	v := s.waitFor(ledger.PhaseDeciding)

	if !strings.Contains(s.output.String(), "status 403") {
		t.Errorf("the agent was not told the request was refused:\n%s", s.output)
	}
	if len(v.Actions) != 2 || v.Actions[0].Decision != ledger.Refused || !strings.Contains(v.Actions[0].Detail, "Refused by the policy") ||
		v.Actions[1].Decision != ledger.Undecided {
		t.Fatalf("held = %+v; want the first already refused by the policy and the second waiting", v.Actions)
	}
	// the refused one is not the user's to allow any more
	if status := s.decide(`{"decision":"apply","approve":["a1"]}`); status != http.StatusBadRequest {
		t.Errorf("allowing a policy-refused action answered %d, want 400", status)
	}
	s.approve("a2")
	code, l := s.finish()
	if code != 0 || len(srv.received()) != 0 || len(other.received()) != 1 {
		t.Errorf("exit %d; the refused server received %q and the other %q; want 0, nothing, and one request", code, srv.received(), other.received())
	}
	if l.Policy.Hash == "" {
		t.Error("the ledger has no policy hash")
	}

	// when everything held was refused by the policy, there is nothing to
	// decide and the session ends by itself
	cmd := exec.Command(buildRehearse(t), "--no-open", "--", "sh", "-c", post(srv.address(), "/upload"))
	cmd.Dir, cmd.Env = work, cleanEnv(t)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "status 403") || len(srv.received()) != 0 {
		t.Errorf("a session whose only action was refused by the policy: %v\n%s", err, out)
	}
}

func TestPolicyAllowLetsAMediumActionThrough(t *testing.T) {
	needCurl(t)
	srv := newUploads(t)
	host := strings.TrimPrefix(srv.address(), "http://")
	work := t.TempDir()
	writePolicy(t, filepath.Join(work, policy.ProjectFile), rules(
		fmt.Sprintf(`{match: {kind: http, destination: %q}, action: allow}`, host),
	))
	bin := buildRehearse(t)
	cmd := exec.Command(bin, "--no-open", "--", "sh", "-c", post(srv.address(), "/upload"))
	cmd.Dir, cmd.Env = work, cleanEnv(t)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "status 201") {
		t.Fatalf("the allowed request did not go straight through: %v\n%s", err, out)
	}
	if got := srv.received(); len(got) != 1 {
		t.Errorf("server received %q, want the one request", got)
	}
	var l ledger.Ledger
	raw, err := os.ReadFile(filepath.Join(work, "rehearsal-ledger.json"))
	if err != nil || json.Unmarshal(raw, &l) != nil {
		t.Fatalf("ledger: %v", err)
	}
	if len(l.Actions) != 0 || len(l.Policy.Allowed) != 1 || l.Policy.Allowed[0].Destination != host || l.Policy.Allowed[0].Rule == "" {
		t.Errorf("held %d, allowed by policy %+v; want nothing held and one action noted as let through", len(l.Actions), l.Policy.Allowed)
	}
	report, err := os.ReadFile(filepath.Join(work, "rehearsal-report.md"))
	if err != nil || !strings.Contains(string(report), "let through by a policy rule") {
		t.Errorf("the report does not mention what the policy let through: %v\n%s", err, report)
	}
}

func TestPolicyCannotLoosenTheOrganisationOrTheBuiltInRules(t *testing.T) {
	needCurl(t)
	srv := newUploads(t)
	host := strings.TrimPrefix(srv.address(), "http://")
	work := t.TempDir()
	org := filepath.Join(t.TempDir(), "org.yaml")
	writePolicy(t, org, rules(fmt.Sprintf(`{match: {destination: %q}, action: hold, risk: high}`, host)))
	writePolicy(t, filepath.Join(work, policy.ProjectFile), rules(
		`{match: {data: canary}, action: allow}`,
		fmt.Sprintf(`{match: {destination: %q}, action: allow}`, host),
	))
	// one plain write, and one that carries the planted word
	script := post(srv.address(), "/plain") + "; curl -sS -o /dev/null --data-binary @CANARY.txt " + srv.address() + "/carrying"
	s := startWith(t, work, []string{"--org-policy", org}, script)
	v := s.waitFor(ledger.PhaseDeciding)

	if len(v.Actions) != 2 || len(srv.received()) != 0 {
		t.Fatalf("held %d actions and the server received %q; want both held and nothing sent\n%s", len(v.Actions), srv.received(), s.output)
	}
	for _, a := range v.Actions {
		if a.Review == nil || a.Review.Risk != events.RiskHigh {
			t.Errorf("%s %s reviewed as %+v, want high: the organisation's minimum, or the built-in one", a.Method, a.Path, a.Review)
		}
	}
	l := s.ledgerNow()
	var whys []string
	for _, ig := range l.Policy.Ignored {
		whys = append(whys, ig.Source+": "+ig.Why)
	}
	joined := strings.Join(whys, "\n")
	if !strings.Contains(joined, "project: the organisation policy holds or refuses") || !strings.Contains(joined, "project: the built-in rules always hold") {
		t.Errorf("ignored rules reported as:\n%s\nwant the project rule that loosens the organisation's, and the one that loosens a built-in rule", joined)
	}
	s.approve()
	s.finish()
	report := read(t, filepath.Join(work, "rehearsal-report.md"))
	if !strings.Contains(report, "Policy rules that were ignored") {
		t.Errorf("the report does not list the ignored rules:\n%s", report)
	}
}

func TestPolicyEditedDuringTheSessionHasNoEffect(t *testing.T) {
	needCurl(t)
	srv := newUploads(t)
	host := strings.TrimPrefix(srv.address(), "http://")
	work := t.TempDir()
	file := filepath.Join(work, policy.ProjectFile)
	writePolicy(t, file, "version: 1\nrules:\n")
	loosen := fmt.Sprintf(`printf '  - {match: {destination: "%s"}, action: allow}\n' >> %s`, host, file)
	s := start(t, work, loosen+" && "+post(srv.address(), "/upload"))
	v := s.waitFor(ledger.PhaseDeciding)

	if len(v.Actions) != 1 || len(srv.received()) != 0 {
		t.Fatalf("held %d actions, server received %q: the agent's edit to the policy took effect\n%s", len(v.Actions), srv.received(), s.output)
	}
	noted := slices.ContainsFunc(s.ledgerNow().Sensitive, func(w events.SensitiveWrite) bool {
		return w.Path == policy.ProjectFile && strings.Contains(w.Reason, "no effect on this session")
	})
	if !noted {
		t.Errorf("the edit to the policy file was not flagged: %+v", s.ledgerNow().Sensitive)
	}
	s.approve()
	s.finish()
}

func TestRuleIsOfferedAfterThreeApprovalsAndWritesOneLine(t *testing.T) {
	needCurl(t)
	srv := newUploads(t)
	host := strings.TrimPrefix(srv.address(), "http://")
	work, state := t.TempDir(), t.TempDir()
	file := filepath.Join(work, policy.ProjectFile)
	before := "version: 1\nrules:\n  - {match: {kind: gh}, risk: high}\n"
	writePolicy(t, file, before)
	run := func() *session {
		s := start(t, work, post(srv.address(), "/upload"), "XDG_STATE_HOME="+state)
		s.waitFor(ledger.PhaseDeciding)
		return s
	}

	for session := 1; session <= 3; session++ {
		s := run()
		if offers := s.ledgerNow().Policy.Offers; len(offers) != 0 {
			t.Fatalf("session %d: a rule was offered after %d approvals: %+v", session, session-1, offers)
		}
		s.approve("a1")
		if code, _ := s.finish(); code != 0 {
			t.Fatalf("session %d: exit %d\n%s", session, code, s.output)
		}
	}
	if got := read(t, file); got != before {
		t.Fatalf("the policy file changed without anyone accepting a rule:\n%s", got)
	}

	// the fourth time, the rule is offered, and nothing is written yet
	s := run()
	offers := s.ledgerNow().Policy.Offers
	if len(offers) != 1 || offers[0].Kind != events.ActionHTTP || offers[0].Destination != host || offers[0].Times != 3 {
		t.Fatalf("offers = %+v, want one for requests to %s after 3 sessions", offers, host)
	}
	if got := read(t, file); got != before {
		t.Fatalf("the rule was written before it was accepted:\n%s", got)
	}
	if status := s.decide(`{"decision":"apply","approve":["a1"],"rules":["r9"]}`); status != http.StatusBadRequest {
		t.Errorf("accepting a rule that was never offered answered %d, want 400", status)
	}
	body := fmt.Sprintf(`{"decision":"apply","approve":["a1"],"rules":[%q]}`, offers[0].ID)
	if status := s.decide(body); status != http.StatusAccepted {
		t.Fatalf("accepting the rule answered %d", status)
	}
	_, l := s.finish()
	want := before + "  - " + offers[0].Line + "\n"
	if got := read(t, file); got != want {
		t.Errorf("the policy file is now:\n%s\nwant exactly one line more:\n%s", got, want)
	}
	if len(l.Policy.Offers) != 1 || !l.Policy.Offers[0].Added {
		t.Errorf("the ledger does not say the rule was added: %+v", l.Policy.Offers)
	}

	// and from then on the request is not held
	sent := len(srv.received())
	cmd := exec.Command(buildRehearse(t), "--no-open", "--", "sh", "-c", post(srv.address(), "/upload"))
	cmd.Dir, cmd.Env = work, cleanEnv(t, "XDG_STATE_HOME="+state)
	out, err := cmd.CombinedOutput()
	if err != nil || len(srv.received()) != sent+1 {
		t.Errorf("with the rule in place the request was not let through: %v\n%s", err, out)
	}
}

func TestPolicyAppliesToStandInCommands(t *testing.T) {
	bin := buildRehearse(t)
	session := func(t *testing.T, rule string) (out string, work, bare string, l ledger.Ledger) {
		t.Helper()
		work, bare = repo(t)
		writePolicy(t, filepath.Join(work, policy.ProjectFile), rules(rule))
		cmd := exec.Command(bin, "--no-open", "--", "sh", "-c", "git push origin main; echo \"push exit $?\"")
		cmd.Dir, cmd.Env = work, cleanEnv(t)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("rehearse: %v\n%s", err, raw)
		}
		data, err := os.ReadFile(filepath.Join(work, "rehearsal-ledger.json"))
		if err != nil || json.Unmarshal(data, &l) != nil {
			t.Fatalf("ledger: %v", err)
		}
		return string(raw), work, bare, l
	}

	t.Run("allow runs the real command", func(t *testing.T) {
		out, work, bare, l := session(t, `{match: {kind: git-push}, action: allow}`)
		if got, want := remoteMain(t, bare), git(t, work, "rev-parse", "HEAD"); got != want {
			t.Errorf("the remote's main is %q, want %q: the allowed push did not happen\n%s", got, want, out)
		}
		if !strings.Contains(out, "push exit 0") || len(l.Actions) != 0 || len(l.Policy.Allowed) != 1 {
			t.Errorf("held %d, let through %d; want the push let through and nothing held\n%s", len(l.Actions), len(l.Policy.Allowed), out)
		}
	})

	t.Run("refuse fails the command and says why", func(t *testing.T) {
		out, _, bare, l := session(t, `{match: {kind: git-push}, action: refuse}`)
		if remoteMain(t, bare) != "" {
			t.Error("the refused push reached the remote")
		}
		if !strings.Contains(out, "push exit 1") || !strings.Contains(out, "refused by the policy for this project") {
			t.Errorf("the agent was not told the push was refused:\n%s", out)
		}
		if len(l.Actions) != 1 || l.Actions[0].Decision != ledger.Refused || l.Actions[0].Outcome != ledger.NoOutcome {
			t.Errorf("actions = %+v, want the push recorded as refused", l.Actions)
		}
	})
}

func TestPolicyCheckPrintsTheRulesAndWhatIsIgnored(t *testing.T) {
	work := t.TempDir()
	org := filepath.Join(t.TempDir(), "org.yaml")
	writePolicy(t, org, rules(`{match: {destination: "uploads.example.net"}, action: refuse}`))
	writePolicy(t, filepath.Join(work, policy.ProjectFile), rules(
		`{match: {destination: "uploads.example.net"}, action: allow}`,
		`{match: {kind: git-push}, risk: high}`,
	))
	cmd := exec.Command(buildRehearse(t), "policy", "check", "--org-policy", org)
	cmd.Dir, cmd.Env = work, cleanEnv(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("policy check: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Organisation policy (" + org + ")", "Project policy (", `action: refuse`, `risk: high`,
		"Rules with no effect:", "the organisation policy holds or refuses this", "Policy hash: ",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("policy check output lacks %q:\n%s", want, out)
		}
	}

	writePolicy(t, filepath.Join(work, policy.ProjectFile), "version: 1\nrules:\n  - {match: {kind: telnet}, action: hold}\n")
	cmd = exec.Command(buildRehearse(t), "policy", "check")
	cmd.Dir, cmd.Env = work, cleanEnv(t)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), `kind is "telnet"`) {
		t.Errorf("a policy that cannot be applied passed the check: %v\n%s", err, out)
	}
	// and a session will not start on it either
	cmd = exec.Command(buildRehearse(t), "--no-open", "--", "true")
	cmd.Dir, cmd.Env = work, cleanEnv(t)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), `kind is "telnet"`) {
		t.Errorf("a session started on a policy that cannot be applied: %v\n%s", err, out)
	}
}
