package proxy

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"rehearsal/handlers"
	"rehearsal/internal/events"
	"rehearsal/internal/hold"
	"rehearsal/internal/ident"
)

type holdingRecorder struct {
	rec    *Recorder
	log    *events.Log
	store  *hold.Store
	holder *hold.Holder
}

func newHoldingRecorder(t *testing.T) holdingRecorder {
	t.Helper()
	log := openLog(t)
	t.Cleanup(func() {
		if err := log.Close(); err != nil {
			t.Error(err)
		}
	})
	store, err := hold.NewStore(filepath.Join(t.TempDir(), "held"))
	if err != nil {
		t.Fatal(err)
	}
	set, err := handlers.Shipped()
	if err != nil {
		t.Fatal(err)
	}
	holder := hold.NewHolder(log, store)
	rec := NewRecorder(log, ident.NewMatcher(watched), []string{"api.provider.test"})
	rec.HoldWrites(Holding{Holder: holder, Handlers: set, Writes: true, ProviderDoor: true, Allow: []string{"Telemetry.Test"}})
	return holdingRecorder{rec, log, store, holder}
}

func eventTypes(t *testing.T, log *events.Log) []events.Type {
	t.Helper()
	evs, err := log.Since(0)
	if err != nil {
		t.Fatal(err)
	}
	var types []events.Type
	for _, ev := range evs {
		types = append(types, ev.Type)
	}
	return types
}

func TestWritesAreHeld(t *testing.T) {
	h := newHoldingRecorder(t)
	body := `{"w":"canary-word-3f9a1c0b7e2d"}`
	f := newFlow(t, "http://localhost:8099/upload?session=private-query", body)
	f.Request.Header.Set("Content-Type", "application/json")
	h.rec.Request(f)

	if f.Response == nil || f.Response.StatusCode != http.StatusCreated || string(f.Response.Body) != `{"stored":true}` {
		t.Fatalf("stand-in answer = %+v, want the handler file's 201 for /upload", f.Response)
	}
	if got := eventTypes(t, h.log); !slices.Equal(got, []events.Type{events.TypeActionHeld}) {
		t.Fatalf("events = %v, want only action_held: a held request has not been sent", got)
	}

	evs, err := h.log.Since(0)
	if err != nil {
		t.Fatal(err)
	}
	var held events.ActionHeld
	if err := json.Unmarshal(evs[0].Data, &held); err != nil {
		t.Fatal(err)
	}
	want := events.ActionHeld{
		ID: "a1", Kind: events.ActionHTTP, Method: http.MethodPost, Host: "localhost:8099", Path: "/upload",
		Scheme: "http", Query: "session=[hidden]",
		RequestBytes: int64(len(body)), ContentType: "application/json", Destination: "localhost:8099",
	}
	gotIDs := held.IdentifierIDs
	held.IdentifierIDs, held.RequestID = nil, ""
	if !equalHeld(held, want) {
		t.Errorf("held event = %+v\nwant %+v", held, want)
	}
	slices.Sort(gotIDs)
	if !slices.Equal(gotIDs, []string{"user", "word"}) {
		t.Errorf("held action carries %v, want user and word", gotIDs)
	}
	for _, secret := range []string{"real-secret-token", "private-query", "canary-word-3f9a1c0b7e2d"} {
		if strings.Contains(string(evs[0].Data), secret) {
			t.Errorf("held event stores %q: %s", secret, evs[0].Data)
		}
	}

	payload, err := h.store.Load("a1")
	if err != nil {
		t.Fatal(err)
	}
	if payload.Method != http.MethodPost || payload.URL != "http://localhost:8099/upload?session=private-query" ||
		string(payload.Body) != body || payload.Header.Get("Authorization") != "Bearer real-secret-token" {
		t.Errorf("stored payload cannot replay the request exactly: %+v", payload)
	}
	if len(payload.Seen) != 2 {
		t.Errorf("stored payload remembers %d identifiers, want 2", len(payload.Seen))
	}
}

func equalHeld(a, b events.ActionHeld) bool {
	x, errX := json.Marshal(a)
	y, errY := json.Marshal(b)
	return errX == nil && errY == nil && string(x) == string(y)
}

func TestWhatIsHeldAndWhatIsNot(t *testing.T) {
	tests := []struct {
		name   string
		method string
		url    string
		held   bool
	}{
		{"POST to an unknown host", http.MethodPost, "https://tracker.test/collect", true},
		{"PUT", http.MethodPut, "https://tracker.test/item/1", true},
		{"PATCH", http.MethodPatch, "https://tracker.test/item/1", true},
		{"DELETE", http.MethodDelete, "https://tracker.test/item/1", true},
		{"GET", http.MethodGet, "https://tracker.test/item/1", false},
		{"HEAD", http.MethodHead, "https://tracker.test/item/1", false},
		{"POST to the model provider", http.MethodPost, "https://api.provider.test/v1/messages", false},
		{"POST to an allowed telemetry host", http.MethodPost, "https://telemetry.test/logs", false},
		{"allow list ignores case", http.MethodPost, "https://TELEMETRY.test/logs", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHoldingRecorder(t)
			f := newFlow(t, tt.url, "x")
			f.Request.Method = tt.method
			f.Request.Header.Del("X-Note") // this table is about requests that carry nothing of the user's
			h.rec.Request(f)

			types := eventTypes(t, h.log)
			if tt.held {
				if f.Response == nil || f.Response.StatusCode != http.StatusOK || string(f.Response.Body) != "{}" {
					t.Errorf("stand-in answer = %+v, want the default 200 {}", f.Response)
				}
				if !slices.Contains(types, events.TypeActionHeld) || slices.Contains(types, events.TypeRequest) {
					t.Errorf("events = %v, want action_held and no request", types)
				}
				return
			}
			if f.Response != nil {
				t.Errorf("request was answered by rehearse (status %d) instead of being sent on", f.Response.StatusCode)
			}
			if slices.Contains(types, events.TypeActionHeld) || !slices.Contains(types, events.TypeRequest) {
				t.Errorf("events = %v, want a request and no action_held", types)
			}
		})
	}
}

func TestDeleteAndRealAnswerAreMarked(t *testing.T) {
	h := newHoldingRecorder(t)
	del := newFlow(t, "http://localhost:8099/files/report.txt", "")
	del.Request.Method = http.MethodDelete
	h.rec.Request(del)
	ticket := newFlow(t, "http://localhost:8099/tickets", `{"title":"x"}`)
	h.rec.Request(ticket)

	evs, err := h.log.Since(0)
	if err != nil {
		t.Fatal(err)
	}
	var first, second events.ActionHeld
	if err := json.Unmarshal(evs[0].Data, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(evs[1].Data, &second); err != nil {
		t.Fatal(err)
	}
	if !first.Deletes || first.NeedsRealAnswer || del.Response.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE: deletes=%t needsRealAnswer=%t status=%d, want true, false, 204", first.Deletes, first.NeedsRealAnswer, del.Response.StatusCode)
	}
	if second.Deletes || !second.NeedsRealAnswer {
		t.Errorf("POST /tickets: deletes=%t needsRealAnswer=%t, want false, true", second.Deletes, second.NeedsRealAnswer)
	}
}

func TestAWriteThatCannotBeHeldIsRefused(t *testing.T) {
	h := newHoldingRecorder(t)
	h.holder.Close()
	f := newFlow(t, "https://tracker.test/collect", "x")
	h.rec.Request(f)
	if f.Response == nil || f.Response.StatusCode != http.StatusBadGateway {
		t.Fatalf("response = %+v, want a 502 refusal", f.Response)
	}
	if !strings.Contains(string(f.Response.Body), "was not sent") {
		t.Errorf("refusal does not explain itself: %q", f.Response.Body)
	}
	if types := eventTypes(t, h.log); len(types) != 0 {
		t.Errorf("events = %v, want none", types)
	}
}
