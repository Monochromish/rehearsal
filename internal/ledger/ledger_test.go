package ledger

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"rehearsal/internal/events"
)

func TestBuild(t *testing.T) {
	event := func(seq int64, typ events.Type, payload any) events.Event {
		t.Helper()
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return events.Event{Seq: seq, Time: time.Unix(seq, 0).UTC(), Type: typ, Data: data}
	}

	ids := []events.Identifier{
		{ID: "email", Kind: events.KindReal, Label: "Your git email address", Value: "a@b.example"},
		{ID: "word", Kind: events.KindCanary, Label: "Planted secret word", Value: "canary-word-1"},
	}
	evs := []events.Event{
		event(1, events.TypeSessionStarted, events.SessionStarted{Command: []string{"agent"}, Identifiers: ids}),
		event(2, events.TypeRequest, events.Request{ID: "r1", Host: "api.provider.test", Category: events.CategoryProvider}),
		event(3, events.TypeIdentifierSeen, events.IdentifierSeen{RequestID: "r1", Host: "api.provider.test", IdentifierID: "email", Where: events.WhereBody}),
		event(4, events.TypeRequest, events.Request{ID: "r2", Host: "tracker.test", Category: events.CategoryOther}),
		event(5, events.TypeIdentifierSeen, events.IdentifierSeen{RequestID: "r2", Host: "tracker.test", IdentifierID: "word", Where: events.WhereBody}),
		event(6, events.TypeIdentifierSeen, events.IdentifierSeen{RequestID: "r2", Host: "tracker.test", IdentifierID: "word", Where: events.WhereHeader}),
		event(7, events.TypeRequest, events.Request{ID: "r3", Host: "tracker.test", Category: events.CategoryOther}),
		event(8, events.TypeRequest, events.Request{ID: "r4", Host: "quiet.test", Category: events.CategoryOther}),
		event(9, events.TypeSessionEnded, events.SessionEnded{ExitCode: 0}),
	}
	l, err := Build(evs)
	if err != nil {
		t.Fatal(err)
	}

	wantTotals := Totals{Requests: 4, Servers: 3, IdentifiersSent: 2, FlaggedServers: 1}
	if l.Totals != wantTotals {
		t.Errorf("totals = %+v, want %+v", l.Totals, wantTotals)
	}
	if l.EndedAt == nil || l.ExitCode == nil || *l.ExitCode != 0 {
		t.Errorf("session end not recorded: ended=%v exit=%v", l.EndedAt, l.ExitCode)
	}

	tests := []struct {
		host     string
		requests int
		flagged  bool
		received []string
	}{
		{"tracker.test", 2, true, []string{"word"}}, // flagged servers sort first
		{"api.provider.test", 1, false, []string{"email"}},
		{"quiet.test", 1, false, nil},
	}
	if len(l.Servers) != len(tests) {
		t.Fatalf("got %d servers, want %d", len(l.Servers), len(tests))
	}
	for i, tt := range tests {
		s := l.Servers[i]
		if s.Host != tt.host || s.Requests != tt.requests || s.Flagged != tt.flagged {
			t.Errorf("server %d = {%s %d flagged=%t}, want {%s %d flagged=%t}", i, s.Host, s.Requests, s.Flagged, tt.host, tt.requests, tt.flagged)
		}
		if len(s.Received) != len(tt.received) {
			t.Errorf("%s received %d identifiers, want %d", s.Host, len(s.Received), len(tt.received))
			continue
		}
		for j, id := range tt.received {
			if s.Received[j].ID != id {
				t.Errorf("%s received[%d] = %q, want %q", s.Host, j, s.Received[j].ID, id)
			}
		}
	}
	// seen in both the body and a header of r2: still one request
	if r := l.Servers[0].Received[0]; r.Requests != 1 || len(r.Where) != 2 {
		t.Errorf("tracker.test word: requests=%d where=%v, want 1 request in 2 places", r.Requests, r.Where)
	}

	text := l.Text()
	for _, want := range []string{"4 requests to 3 servers", "MODEL PROVIDER", "EVERYTHING ELSE", "! tracker.test", "Planted secret word (planted)", "Your git email address (yours)"} {
		if !strings.Contains(text, want) {
			t.Errorf("terminal text is missing %q:\n%s", want, text)
		}
	}
}
