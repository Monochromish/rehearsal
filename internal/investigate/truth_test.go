package investigate

import (
	"strings"
	"testing"

	"rehearsal/internal/events"
)

func TestTheSessionsOwnSentenceIsListedWithItsEntries(t *testing.T) {
	key, pub := newKey(t)
	dir := t.TempDir()
	add := func(log *events.Log, typ events.Type, data any) {
		t.Helper()
		if _, err := log.Append(typ, data); err != nil {
			t.Fatal(err)
		}
	}
	folder := session(t, dir, "20260110-090000-truths", key, func(log *events.Log) {
		add(log, events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, WorkDir: "/home/ada/shop", Holding: true, ProviderHosts: []string{"api.provider.test"}})
		add(log, events.TypeRequest, events.Request{ID: "r1", Host: "api.provider.test", Category: events.CategoryProvider, Conversation: true})
		add(log, events.TypeAgentAction, events.AgentAction{ID: "t1", Tool: "Bash", Kind: "shell", Text: "go test ./..."})
		add(log, events.TypeAgentResult, events.AgentResult{ID: "t1", Failed: true})
		add(log, events.TypeAgentClaims, events.AgentClaims{Claims: []events.AgentClaim{{Kind: "tests", Words: "All tests pass"}}})
		add(log, events.TypeSessionEnded, events.SessionEnded{})
	})
	c := open(t, folder, pub)
	if len(c.Truth) != 1 {
		t.Fatalf("%d sentences, want the session's one: %+v", len(c.Truth), c.Truth)
	}
	s := c.Truth[0]
	if s.Kind != Observed || s.Text != "Agent said tests pass. The test command failed." {
		t.Errorf("statement = %+v", s)
	}
	// what was said, then the command and its result, each with the part
	// of the stored line that bears it out
	want := []struct {
		seq  int64
		mark string
	}{{5, `"kind":"tests"`}, {3, `"text":`}, {4, `"failed":true`}}
	if len(s.Refs) != len(want) {
		t.Fatalf("cites %v, want three entries", s.Refs)
	}
	for i, w := range want {
		r := s.Refs[i]
		e, ok := c.Entry(r)
		if r.Seq != w.seq || r.Mark != w.mark || !ok || !strings.Contains(e.Data, r.Mark) {
			t.Errorf("citation %d = %+v, want entry %d marked %s and holding it", i+1, r, w.seq, w.mark)
		}
		// the cited entries travel with the case, for a front end to show
		found := false
		for _, kept := range c.Entries {
			found = found || kept.Seq == r.Seq
		}
		if !found {
			t.Errorf("entry %d is cited and is not among the case's entries", r.Seq)
		}
	}
	md := c.Markdown()
	for _, part := range []string{"## Said against observed", "- **Observed.** S1: Agent said tests pass. The test command failed. [S1#5] [S1#3] [S1#4]", `Supporting part: ` + "`" + `"failed":true` + "`", "All tests pass"} {
		if !strings.Contains(md, part) {
			t.Errorf("the report does not hold %q:\n%s", part, md)
		}
	}

	// a session with nothing to set against anything has no such section
	quiet := session(t, dir, "20260110-100000-quiets", key, func(log *events.Log) {
		add(log, events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, WorkDir: "/home/ada/shop"})
		add(log, events.TypeSessionEnded, events.SessionEnded{})
	})
	if c := open(t, quiet, pub); len(c.Truth) != 0 || c.Truth == nil || strings.Contains(c.Markdown(), "Said against observed") {
		t.Errorf("a quiet session has %+v", c.Truth)
	}
}
