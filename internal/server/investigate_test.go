package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"rehearsal/internal/events"
	"rehearsal/internal/investigate"
)

type standIn struct{ reply string }

func (m standIn) Name() string { return "stand-in" }

func (m standIn) Complete(context.Context, string, string, int) (string, error) {
	return m.reply, nil
}

func post(t *testing.T, address, contentType, body string) (int, string) {
	t.Helper()
	res, err := http.Post(address, contentType, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(res.Body)
	if cerr := res.Body.Close(); err != nil || cerr != nil {
		t.Fatal(err, cerr)
	}
	return res.StatusCode, string(data)
}

// The investigation is served for one session and for all of them, with
// the raw entries its findings rest on, and questions are answered from
// the record with the entries that were retrieved.
func TestInvestigationIsServed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	sessions := t.TempDir()
	const older, newer = "20260101-090000-aaaaaa", "20260102-090000-bbbbbb"
	old := recorded(t, sessions, older, "/home/ada/shop", func(log *events.Log) {
		for _, ev := range []struct {
			typ     events.Type
			payload any
		}{
			{events.TypePackageChecked, events.PackageChecked{Manager: "npm", Name: "shop-utilz", Risk: events.RiskHigh, Facts: "published 9 hours ago", Blocked: true}},
			{events.TypeSessionEnded, events.SessionEnded{}},
		} {
			if _, err := log.Append(ev.typ, ev.payload); err != nil {
				t.Fatal(err)
			}
		}
	})
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	live := recorded(t, sessions, newer, "/home/ada/site", nil)
	defer func() {
		if err := live.Close(); err != nil {
			t.Error(err)
		}
	}()
	s, err := Start(live, fstest.MapFS{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()

	if status, _ := get(t, s.URL+"/api/investigate"); status != http.StatusNotFound {
		t.Errorf("before the sessions are served, /api/investigate answered %d", status)
	}
	s.ServeSessions(sessions, newer)
	s.Describe(Info{Version: "9.9.9"})

	var one investigate.Case
	getInto(t, s.URL+"/api/investigate?session="+older, &one)
	if len(one.Records) != 1 || one.Records[0].Verified || one.Records[0].Entries != 3 || one.ToolVersion != "9.9.9" {
		t.Errorf("custody = %+v, version %q", one.Records, one.ToolVersion)
	}
	if len(one.Findings) != 1 || one.Findings[0].Kind != investigate.KindInstall || one.Findings[0].RecordNote == "" {
		t.Fatalf("findings = %+v", one.Findings)
	}
	if len(one.Entries) != 1 || !strings.Contains(one.Entries[0].Data, `"blocked":true`) || one.Findings[0].Refs[0].Hash != one.Entries[0].Hash {
		t.Errorf("the raw entry the finding rests on is not served: %+v", one.Entries)
	}
	if one.Findings[0].Narrative != nil {
		t.Error("an account was written with no model set")
	}

	var all investigate.Case
	getInto(t, s.URL+"/api/investigate", &all)
	if len(all.Records) != 2 || len(all.Overview) != 2 || all.Records[1].Session != newer {
		t.Errorf("the case for every session = %+v", all.Records)
	}
	for _, bad := range []string{"..", "no-such-session", "a/b"} {
		if status, _ := get(t, s.URL+"/api/investigate?session="+bad); status != http.StatusNotFound {
			t.Errorf("session %q answered %d", bad, status)
		}
	}

	// questions: JSON only, answered without a model and then with one
	if status, _ := post(t, s.URL+"/api/investigate/ask", "text/plain", `{"question":"x"}`); status != http.StatusUnsupportedMediaType {
		t.Errorf("a question not sent as JSON answered %d", status)
	}
	if status, _ := post(t, s.URL+"/api/investigate/ask", "application/json", `{"session":"`+older+`"}`); status != http.StatusBadRequest {
		t.Errorf("a request with no question answered %d", status)
	}
	status, body := post(t, s.URL+"/api/investigate/ask", "application/json", `{"session":"`+older+`","question":"Was shop-utilz installed?"}`)
	if status != http.StatusOK || !strings.Contains(body, "No local model") || !strings.Contains(body, "shop-utilz") {
		t.Errorf("with no model: %d %s", status, body)
	}
	s.UseInvestigator(standIn{reply: "Its install was stopped [S1#2]. It was a mistake by the user."})
	status, body = post(t, s.URL+"/api/investigate/ask", "application/json", `{"session":"`+older+`","question":"Was shop-utilz installed?"}`)
	if status != http.StatusOK || !strings.Contains(body, "Its install was stopped [S1#2].") || strings.Contains(body, "mistake") || !strings.Contains(body, `"removed":1`) {
		t.Errorf("with a model: %d %s", status, body)
	}
	getInto(t, s.URL+"/api/investigate?session="+older+"&narrate=1", &one)
	if n := one.Findings[0].Narrative; n == nil || len(n.Sentences) != 1 || n.Removed != 1 || n.Model != "stand-in" {
		t.Errorf("the account = %+v", n)
	}
}
