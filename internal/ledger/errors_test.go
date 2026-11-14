package ledger

import (
	"encoding/json"
	"strings"
	"testing"

	"rehearsal/internal/events"
)

func TestBuildRejectsABrokenLog(t *testing.T) {
	tests := []struct {
		name    string
		ev      events.Event
		wantErr string
	}{
		{
			"unreadable payload",
			events.Event{Seq: 7, Type: events.TypeRequest, Data: json.RawMessage(`{"host": 12}`)},
			"event 7 (request) is unreadable",
		},
		{
			"identifier seen on a server with no request",
			events.Event{Seq: 9, Type: events.TypeIdentifierSeen, Data: json.RawMessage(`{"host":"ghost.test","identifier_id":"word"}`)},
			"event 9 names an unknown server or identifier (ghost.test, word)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build([]events.Event{tt.ev})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Build error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}
