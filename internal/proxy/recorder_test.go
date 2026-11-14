package proxy

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	mitm "github.com/lqqyt2423/go-mitmproxy/proxy"

	"rehearsal/internal/events"
	"rehearsal/internal/ident"
)

func newFlow(t *testing.T, rawURL, body string) *mitm.Flow {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return &mitm.Flow{Request: &mitm.Request{
		Method: http.MethodPost,
		URL:    u,
		Header: http.Header{"Authorization": {"Bearer real-secret-token"}, "X-Note": {"from adal"}},
		Body:   []byte(body),
	}}
}

func openLog(t *testing.T) *events.Log {
	t.Helper()
	log, err := events.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	return log
}

var watched = []events.Identifier{
	{ID: "user", Kind: events.KindReal, Value: "adal"},
	{ID: "word", Kind: events.KindCanary, Value: "canary-word-3f9a1c0b7e2d"},
}

func TestRecorderStoresWhereNotWhat(t *testing.T) {
	log := openLog(t)
	t.Cleanup(func() {
		if err := log.Close(); err != nil {
			t.Error(err)
		}
	})
	rec := NewRecorder(log, ident.NewMatcher(watched), []string{"api.provider.test"})

	f := newFlow(t, "https://tracker.test:8443/collect?session=private-query", `{"w":"canary-word-3f9a1c0b7e2d"}`)
	rec.Request(f)
	if f.Response != nil {
		t.Fatalf("request was refused with status %d", f.Response.StatusCode)
	}
	if err := rec.Err(); err != nil {
		t.Fatal(err)
	}

	evs, err := log.Since(0)
	if err != nil {
		t.Fatal(err)
	}
	var req events.Request
	seen := map[string]events.Where{}
	for _, ev := range evs {
		for _, secret := range []string{"real-secret-token", "private-query", "canary-word-3f9a1c0b7e2d", "from adal"} {
			if strings.Contains(string(ev.Data), secret) {
				t.Errorf("event %d stores %q: %s", ev.Seq, secret, ev.Data)
			}
		}
		switch ev.Type {
		case events.TypeRequest:
			if err := json.Unmarshal(ev.Data, &req); err != nil {
				t.Fatal(err)
			}
		case events.TypeIdentifierSeen:
			var s events.IdentifierSeen
			if err := json.Unmarshal(ev.Data, &s); err != nil {
				t.Fatal(err)
			}
			seen[s.IdentifierID] = s.Where
		}
	}

	want := events.Request{
		Host: "tracker.test:8443", Method: http.MethodPost, Path: "/collect",
		RequestBytes: 32, Category: events.CategoryOther, BodyChecked: true,
	}
	req.ID = ""
	if req != want {
		t.Errorf("request event = %+v, want %+v", req, want)
	}
	if len(seen) != 2 || seen["word"] != events.WhereBody || seen["user"] != events.WhereHeader {
		t.Errorf("identifiers seen = %v, want word in the body and user in a header", seen)
	}
}

func TestRecorderRefusesWhatItCannotRecord(t *testing.T) {
	log := openLog(t)
	if err := log.Close(); err != nil { // every write to the log now fails
		t.Fatal(err)
	}
	rec := NewRecorder(log, ident.NewMatcher(watched), nil)

	first := newFlow(t, "https://tracker.test/collect", "x")
	rec.Request(first)
	if first.Response == nil || first.Response.StatusCode != http.StatusBadGateway {
		t.Fatalf("unrecorded request was let through: response = %+v", first.Response)
	}
	if !strings.Contains(string(first.Response.Body), "was not sent") {
		t.Errorf("refusal does not explain itself: %q", first.Response.Body)
	}
	if rec.Err() == nil {
		t.Error("Err() is nil after a failed write")
	}

	upgrade := newFlow(t, "https://tracker.test/socket", "")
	upgrade.Request.Header.Set("Connection", "Upgrade")
	upgrade.Request.Header.Set("Upgrade", "websocket")
	rec.Requestheaders(upgrade)
	if upgrade.Response == nil || upgrade.Response.StatusCode != http.StatusBadGateway {
		t.Errorf("WebSocket upgrade was let through after the log failed: response = %+v", upgrade.Response)
	}
}
