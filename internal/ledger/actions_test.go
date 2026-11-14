package ledger

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"rehearsal/internal/events"
)

// log builds an event list from (type, payload) pairs.
func log(t *testing.T, pairs ...any) []events.Event {
	t.Helper()
	var evs []events.Event
	for i := 0; i+1 < len(pairs); i += 2 {
		typ, ok := pairs[i].(events.Type)
		if !ok {
			t.Fatalf("argument %d is not an event type", i)
		}
		data, err := json.Marshal(pairs[i+1])
		if err != nil {
			t.Fatal(err)
		}
		seq := int64(len(evs) + 1)
		evs = append(evs, events.Event{Seq: seq, Time: time.Unix(seq, 0).UTC(), Type: typ, Data: data})
	}
	return evs
}

func TestPhaseAndOutcomes(t *testing.T) {
	started := events.SessionStarted{Command: []string{"agent"}, Holding: true, Identifiers: []events.Identifier{
		{ID: "word", Kind: events.KindCanary, Label: "Planted secret word", Value: "canary-word-1"},
	}}
	push := events.ActionHeld{ID: "a1", Kind: events.ActionGitPush, Command: []string{"git", "push"}}
	post := events.ActionHeld{ID: "a2", Kind: events.ActionHTTP, Method: "POST", Host: "tracker.test", Path: "/x", IdentifierIDs: []string{"word"}}
	del := events.ActionHeld{ID: "a3", Kind: events.ActionHTTP, Method: "DELETE", Host: "tracker.test", Path: "/y"}
	reviewed := func(id string) events.ActionReviewed {
		return events.ActionReviewed{ActionID: id, Summary: "Does a thing.", Risk: events.RiskMedium, Concern: "It may break.", Source: events.ReviewByRules}
	}
	held := []any{
		events.TypeSessionStarted, started,
		events.TypeActionHeld, push, events.TypeActionHeld, post, events.TypeActionHeld, del,
	}
	ended := append(append([]any{}, held...), events.TypeSessionEnded, events.SessionEnded{})
	described := append(append([]any{}, ended...),
		events.TypeActionReviewed, reviewed("a1"), events.TypeActionReviewed, reviewed("a2"), events.TypeActionReviewed, reviewed("a3"))
	decided := append(append([]any{}, described...),
		events.TypeActionApproved, events.ActionApproved{ActionID: "a1"},
		events.TypeActionRefused, events.ActionRefused{ActionID: "a2"},
		events.TypeActionApproved, events.ActionApproved{ActionID: "a3"})

	tests := []struct {
		name     string
		pairs    []any
		phase    Phase
		outcomes []Outcome
	}{
		{"agent still running", held, PhaseRunning, []Outcome{NoOutcome, NoOutcome, NoOutcome}},
		{"agent exited, nothing described", ended, PhaseReviewing, []Outcome{NoOutcome, NoOutcome, NoOutcome}},
		{"one described", append(append([]any{}, ended...), events.TypeActionReviewed, reviewed("a2")), PhaseReviewing, []Outcome{NoOutcome, NoOutcome, NoOutcome}},
		{"all described", described, PhaseDeciding, []Outcome{NoOutcome, NoOutcome, NoOutcome}},
		{"decided, nothing performed yet", decided, PhaseApplying, []Outcome{NoOutcome, NoOutcome, NoOutcome}},
		{"first approved action performed", append(append([]any{}, decided...),
			events.TypeActionApplied, events.ActionApplied{ActionID: "a1", Detail: "ok"}), PhaseApplying, []Outcome{Applied, NoOutcome, NoOutcome}},
		{"both approved actions performed", append(append([]any{}, decided...),
			events.TypeActionApplied, events.ActionApplied{ActionID: "a1"},
			events.TypeActionApplied, events.ActionApplied{ActionID: "a3"}), PhaseFinished, []Outcome{Applied, NoOutcome, Applied}},
		{"first approved action failed", append(append([]any{}, decided...),
			events.TypeActionFailed, events.ActionFailed{ActionID: "a1", Reason: "moved", Detail: "output"}), PhaseFinished, []Outcome{Failed, NoOutcome, NotApplied}},
		{"everything refused", append(append([]any{}, described...),
			events.TypeActionRefused, events.ActionRefused{ActionID: "a1"},
			events.TypeActionRefused, events.ActionRefused{ActionID: "a2"},
			events.TypeActionRefused, events.ActionRefused{ActionID: "a3"}), PhaseFinished, []Outcome{NoOutcome, NoOutcome, NoOutcome}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := Build(log(t, tt.pairs...))
			if err != nil {
				t.Fatal(err)
			}
			if l.Phase != tt.phase {
				t.Errorf("phase = %q, want %q", l.Phase, tt.phase)
			}
			if l.Totals.Held != 3 || len(l.Actions) != 3 || !l.Holding {
				t.Fatalf("held = %d, actions = %d, holding = %t; want 3, 3, true", l.Totals.Held, len(l.Actions), l.Holding)
			}
			for i, want := range tt.outcomes {
				if got := l.Actions[i].Outcome; got != want {
					t.Errorf("action %s outcome = %q, want %q", l.Actions[i].ID, got, want)
				}
			}
			if c := l.Actions[1].Carries; len(c) != 1 || c[0].Label != "Planted secret word" {
				t.Errorf("a2 carries %+v, want the planted word with its label", c)
			}
		})
	}

	t.Run("a session with nothing held ends when the agent does", func(t *testing.T) {
		l, err := Build(log(t, events.TypeSessionStarted, started, events.TypeSessionEnded, events.SessionEnded{}))
		if err != nil {
			t.Fatal(err)
		}
		if l.Phase != PhaseFinished {
			t.Errorf("phase = %q, want finished", l.Phase)
		}
	})

	t.Run("an event for an action that was never held is an error", func(t *testing.T) {
		_, err := Build(log(t, events.TypeSessionStarted, started, events.TypeActionApplied, events.ActionApplied{ActionID: "a7"}))
		if err == nil || !strings.Contains(err.Error(), `never held: "a7"`) {
			t.Errorf("err = %v, want one naming the unknown action a7", err)
		}
	})

	t.Run("the terminal summary keeps the exact text beside the explanation", func(t *testing.T) {
		failed := append(append([]any{}, decided...),
			events.TypeActionFailed, events.ActionFailed{ActionID: "a1", Reason: "refs/heads/main has moved", Detail: ""})
		l, err := Build(log(t, failed...))
		if err != nil {
			t.Fatal(err)
		}
		text := l.Text()
		for _, want := range []string{
			"HELD ACTIONS", "FAILED", "Does a thing.", "git push", "POST tracker.test/x", "refs/heads/main has moved",
			"3 actions held: 0 applied, 1 refused, 1 failed, 1 not applied because an earlier one failed",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("terminal text is missing %q:\n%s", want, text)
			}
		}
	})
}
