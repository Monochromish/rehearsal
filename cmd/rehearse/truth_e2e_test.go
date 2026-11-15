package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"rehearsal/internal/events"
	"rehearsal/internal/ledger"
)

// The agent's last message says the tests pass and that it pushed. No
// command of any kind was seen, in a session whose commands could be read.
func TestTheSentenceSetsTheAgentsLastMessageAgainstTheCommands(t *testing.T) {
	needCurl(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		const reply = `{"type":"message","role":"assistant","model":"stand-in","content":[{"type":"text","text":"All tests pass now. The fix is in checkout.go."}],"usage":{"input_tokens":12,"output_tokens":9}}`
		if _, err := w.Write([]byte(reply)); err != nil {
			t.Errorf("provider: %v", err)
		}
	}))
	defer provider.Close()
	const asked = `{"model":"stand-in","messages":[{"role":"user","content":"Fix the rounding bug and run the tests"}]}`
	s := startWith(t, t.TempDir(), []string{"--provider", "127.0.0.1"},
		`curl -sS -o /dev/null -H "Content-Type: application/json" -d '`+asked+`' `+provider.URL+`/v1/messages`)
	code, l := s.finish()
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, s.output)
	}
	const want = "Agent said tests pass. No test command ran."
	if l.Truth == nil || l.Truth.Sentence != want || l.Truth.Form != ledger.FormClaim || l.Truth.Claim != "tests" {
		t.Fatalf("truth in the ledger = %+v, want %q\n%s", l.Truth, want, s.output)
	}

	// it comes before the ledger in what is printed, and opens the report
	out := s.output.String()
	at, ledgerAt := strings.Index(out, "\n"+want+"\n"), strings.Index(out, "Rehearsal ledger:")
	if at < 0 || ledgerAt < 0 || at > ledgerAt {
		t.Errorf("the sentence is not printed before the ledger:\n%s", out)
	}
	report, err := os.ReadFile(sessionFile(t, s.cmd.Env, "rehearsal-report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(report), "# Rehearsal report: sh\n\n"+want+"\n") {
		t.Errorf("the report does not open with the sentence:\n%s", report)
	}

	// each half names entries of the record that hold what it says
	record := exported(t, s)
	claims := entries(t, record, events.TypeAgentClaims)
	if len(claims) != 1 || len(l.Truth.Said.Entries) != 1 || claims[0].Seq != l.Truth.Said.Entries[0] ||
		!strings.Contains(string(claims[0].Data), `"kind":"tests"`) || !strings.Contains(string(claims[0].Data), "All tests pass now") {
		t.Errorf("what was said rests on %v; the record's claims are %+v", l.Truth.Said.Entries, claims)
	}
	if strings.Contains(string(claims[0].Data), "checkout.go") {
		t.Errorf("a sentence that makes no claim was kept: %s", claims[0].Data)
	}
	requests := entries(t, record, events.TypeRequest)
	if len(l.Truth.Observed.Entries) != 1 || len(requests) != 1 || requests[0].Seq != l.Truth.Observed.Entries[0] ||
		!strings.Contains(string(requests[0].Data), `"conversation":true`) {
		t.Errorf("what was observed rests on %v, want the request that shows commands could be read: %+v", l.Truth.Observed.Entries, requests)
	}
}

// No final message, and one action held and then refused: the agent was
// told it worked.
func TestTheSentenceSaysWhatTheAgentWasToldWhenNothingWasClaimed(t *testing.T) {
	needCurl(t)
	srv := newUploads(t)
	host := strings.TrimPrefix(srv.address(), "http://")
	s := start(t, t.TempDir(), `curl -sS -o /dev/null --data-binary @CANARY.txt `+srv.address()+`/upload`)
	s.waitFor(ledger.PhaseDeciding)
	if status := s.decide(`{"decision":"discard"}`); status != http.StatusAccepted {
		t.Fatalf("discard answered %d", status)
	}
	_, l := s.finish()
	want := "The agent was told sending a planted value to " + host + " worked. It has not happened."
	if l.Truth == nil || l.Truth.Sentence != want || l.Truth.Form != ledger.FormTold {
		t.Fatalf("truth in the ledger = %+v, want %q\n%s", l.Truth, want, s.output)
	}
	if out := s.output.String(); !strings.Contains(out, "\n"+want+"\n\nRehearsal ledger:") {
		t.Errorf("the sentence is not the first line of the ledger that is printed:\n%s", out)
	}
	record := exported(t, s)
	held, refused := entries(t, record, events.TypeActionHeld), entries(t, record, events.TypeActionRefused)
	if len(held) != 1 || len(refused) != 1 || len(l.Truth.Said.Entries) != 1 || len(l.Truth.Observed.Entries) != 1 ||
		held[0].Seq != l.Truth.Said.Entries[0] || refused[0].Seq != l.Truth.Observed.Entries[0] {
		t.Errorf("the halves rest on %v and %v, want the hold and the refusal", l.Truth.Said.Entries, l.Truth.Observed.Entries)
	}
}

// With nothing held and nothing claimed there is no sentence, and nothing
// stands in for it.
func TestNoSentenceWhenThereIsNothingToSetAgainstAnything(t *testing.T) {
	s := startWith(t, t.TempDir(), []string{"--no-hold"}, `echo "All done. Everything is set up."`)
	_, l := s.finish()
	if l.Truth != nil {
		t.Fatalf("truth = %+v, want none", l.Truth)
	}
	if out := s.output.String(); !strings.Contains(out, "rehearse: watching.") || strings.Contains(out, "Agent said") || strings.Contains(out, "was told") {
		t.Errorf("something was printed in place of the sentence:\n%s", out)
	}
	report, err := os.ReadFile(sessionFile(t, s.cmd.Env, "rehearsal-report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(report), "# Rehearsal report: sh\n\n## What left your computer") {
		t.Errorf("the report has something between its title and its first section:\n%s", report)
	}
}
