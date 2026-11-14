package review

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rehearsal/internal/events"
)

func TestFloor(t *testing.T) {
	tests := []struct {
		name string
		a    events.ActionHeld
		want events.Risk
	}{
		{"ordinary push", events.ActionHeld{Kind: events.ActionGitPush}, events.RiskLow},
		{"force push", events.ActionHeld{Kind: events.ActionGitPush, Force: true}, events.RiskHigh},
		{"deleting a repository", events.ActionHeld{Kind: events.ActionGh, Deletes: true}, events.RiskHigh},
		{"deleting a release", events.ActionHeld{Kind: events.ActionGh, Subject: "release delete", Deletes: true}, events.RiskHigh},
		{"npm publish", events.ActionHeld{Kind: events.ActionNpmPublish, Publishes: true}, events.RiskHigh},
		{"publishing a release", events.ActionHeld{Kind: events.ActionGh, Publishes: true}, events.RiskHigh},
		{"request carrying a canary", events.ActionHeld{Kind: events.ActionHTTP, IdentifierIDs: []string{"canary-word"}}, events.RiskHigh},
		{"request carrying a real identifier", events.ActionHeld{Kind: events.ActionHTTP, IdentifierIDs: []string{"git-email"}}, events.RiskHigh},
		{"request carrying nothing", events.ActionHeld{Kind: events.ActionHTTP, IdentifierIDs: []string{}}, events.RiskLow},
		{"opening a pull request", events.ActionHeld{Kind: events.ActionGh, NeedsRealAnswer: true}, events.RiskLow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Floor(tt.a); got != tt.want {
				t.Errorf("Floor = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestByRulesNeverGoesBelowTheFloor(t *testing.T) {
	actions := []events.ActionHeld{
		{Kind: events.ActionGitPush, Subject: "main", Destination: "github.com:ada/repo.git"},
		{Kind: events.ActionGitPush, Force: true, Subject: "main", Destination: "origin"},
		{Kind: events.ActionGitPush, Deletes: true, Subject: "old (delete)", Destination: "origin"},
		{Kind: events.ActionNpmPublish, Publishes: true, Subject: "left-pad@1.0.1", Destination: "https://registry.npmjs.org/"},
		{Kind: events.ActionGh, Subject: "pr create", Destination: "GitHub repository ada/repo", NeedsRealAnswer: true},
		{Kind: events.ActionGh, Subject: "repo delete", Deletes: true, Destination: "GitHub repository ada/repo"},
		{Kind: events.ActionHTTP, Method: "POST", Host: "localhost:8099", Path: "/upload", Destination: "localhost:8099"},
		{Kind: events.ActionHTTP, Method: "POST", Host: "tracker.test", Path: "/collect", Destination: "tracker.test", IdentifierIDs: []string{"canary-word"}},
		{Kind: events.ActionHTTP, Method: "DELETE", Host: "tracker.test", Path: "/item/1", Destination: "tracker.test", Deletes: true},
	}
	for _, a := range actions {
		v := ByRules(a)
		if rank(v.Risk) < rank(Floor(a)) {
			t.Errorf("%s %q: rule risk %q is below the floor %q", a.Kind, a.Subject, v.Risk, Floor(a))
		}
		if v.Summary == "" || v.Concern == "" || strings.Contains(v.Summary, "\n") {
			t.Errorf("%s %q: summary %q and concern %q must both be one non-empty line", a.Kind, a.Subject, v.Summary, v.Concern)
		}
		if v.Source != events.ReviewByRules {
			t.Errorf("%s %q: source = %q, want rules", a.Kind, a.Subject, v.Source)
		}
	}
}

func TestByRulesRisk(t *testing.T) {
	tests := []struct {
		name string
		a    events.ActionHeld
		want events.Risk
	}{
		{"ordinary push needs a look", events.ActionHeld{Kind: events.ActionGitPush}, events.RiskMedium},
		{"gh change needs a look", events.ActionHeld{Kind: events.ActionGh, Subject: "pr comment"}, events.RiskMedium},
		{"write to another server needs a look", events.ActionHeld{Kind: events.ActionHTTP, Host: "tracker.test"}, events.RiskMedium},
		{"write to this machine is routine", events.ActionHeld{Kind: events.ActionHTTP, Host: "localhost:8099"}, events.RiskLow},
		{"write to 127.0.0.1 is routine", events.ActionHeld{Kind: events.ActionHTTP, Host: "127.0.0.1:3000"}, events.RiskLow},
		{"write to this machine with a canary is not", events.ActionHeld{Kind: events.ActionHTTP, Host: "localhost:8099", IdentifierIDs: []string{"canary-word"}}, events.RiskHigh},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ByRules(tt.a).Risk; got != tt.want {
				t.Errorf("risk = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParse(t *testing.T) {
	good := `{"summary":"Uploads your saved changes to the shared copy.","risk":"medium","concern":"Others will receive these changes."}`
	tests := []struct {
		name    string
		reply   string
		wantErr string // empty means the reply is accepted
	}{
		{"valid", good, ""},
		{"valid with surrounding space", "\n  " + good + "\n", ""},
		{"risk in capitals", strings.Replace(good, `"medium"`, `"HIGH"`, 1), ""},
		{"not JSON", "Sure! This pushes code.", "not JSON"},
		{"JSON inside a code fence", "```json\n" + good + "\n```", "not JSON"},
		{"text after the object", good + " Hope that helps.", "after the JSON"},
		{"an array", "[" + good + "]", "not JSON"},
		{"unknown risk", strings.Replace(good, `"medium"`, `"moderate"`, 1), `risk "moderate"`},
		{"missing risk", `{"summary":"a","concern":"b"}`, `risk ""`},
		{"missing summary", `{"risk":"low","concern":"b"}`, "summary is empty"},
		{"blank concern", `{"summary":"a","risk":"low","concern":"   "}`, "concern is empty"},
		{"extra field", `{"summary":"a","risk":"low","concern":"b","approve":true}`, "unknown field"},
		{"number for risk", `{"summary":"a","risk":3,"concern":"b"}`, "not JSON"},
		{"summary over several lines", `{"summary":"a\nb","risk":"low","concern":"b"}`, "summary is more than one line"},
		{"summary far too long", `{"summary":"` + strings.Repeat("word ", 100) + `","risk":"low","concern":"b"}`, "summary is longer"},
		{"empty reply", "", "not JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary, risk, concern, err := Parse(tt.reply)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Parse rejected a valid reply: %v", err)
				}
				if summary == "" || concern == "" || rank(risk) == 0 {
					t.Errorf("Parse = %q, %q, %q: want all three filled in", summary, risk, concern)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Parse error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// fakeModel answers chat requests with the given replies in turn and
// keeps what it was sent.
type fakeModel struct {
	*httptest.Server
	calls atomic.Int32
	sent  atomic.Value // string: the last request body
}

func newFakeModel(t *testing.T, replies ...string) *fakeModel {
	t.Helper()
	f := &fakeModel{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(f.calls.Add(1))
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("fake model: %v", err)
		}
		f.sent.Store(string(body))
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		reply := replies[min(n, len(replies))-1]
		if reply == "HTTP 500" {
			http.Error(w, "model crashed", http.StatusInternalServerError)
			return
		}
		out, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": reply}}}})
		if err != nil {
			t.Errorf("fake model: %v", err)
		}
		if _, err := w.Write(out); err != nil {
			t.Errorf("fake model: %v", err)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func newModel(t *testing.T, url string, timeout time.Duration) *Model {
	t.Helper()
	m, err := NewModel(url+"/v1", "test-model", timeout)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

const (
	lowReply  = `{"summary":"Uploads your saved changes.","risk":"low","concern":"Others will receive them."}`
	highReply = `{"summary":"Sends data to another server.","risk":"high","concern":"It may be private."}`
)

func TestModelReview(t *testing.T) {
	push := events.ActionHeld{ID: "a1", Kind: events.ActionGitPush, Command: []string{"git", "push", "origin", "main"}, Subject: "main", Destination: "origin"}
	forcePush := push
	forcePush.Force = true

	tests := []struct {
		name       string
		action     events.ActionHeld
		replies    []string
		wantSource events.ReviewSource
		wantRisk   events.Risk
		wantCalls  int32
		wantNote   string
	}{
		{"a good answer is used", push, []string{lowReply}, events.ReviewByModel, events.RiskLow, 1, ""},
		{"the model may raise the risk", push, []string{highReply}, events.ReviewByModel, events.RiskHigh, 1, ""},
		{"the model cannot lower a force push", forcePush, []string{lowReply}, events.ReviewByModel, events.RiskHigh, 1, ""},
		{"a bad answer is retried once", push, []string{"not json", lowReply}, events.ReviewByModel, events.RiskLow, 2, ""},
		{"two bad answers fall back to the rules", push, []string{"not json", `{"risk":"low"}`, lowReply}, events.ReviewByRules, events.RiskMedium, 2, "could not be used"},
		{"the fallback keeps the floor too", forcePush, []string{"nope", "nope"}, events.ReviewByRules, events.RiskHigh, 2, "could not be used"},
		{"a server error falls back at once", push, []string{"HTTP 500"}, events.ReviewByRules, events.RiskMedium, 1, "did not answer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeModel(t, tt.replies...)
			v := newModel(t, fake.URL, 5*time.Second).Review(context.Background(), tt.action, nil, "")
			if v.Source != tt.wantSource || v.Risk != tt.wantRisk {
				t.Errorf("verdict = source %q risk %q, want %q and %q", v.Source, v.Risk, tt.wantSource, tt.wantRisk)
			}
			if got := fake.calls.Load(); got != tt.wantCalls {
				t.Errorf("model was called %d times, want %d", got, tt.wantCalls)
			}
			if tt.wantNote == "" && v.Note != "" || !strings.Contains(v.Note, tt.wantNote) {
				t.Errorf("note = %q, want one containing %q", v.Note, tt.wantNote)
			}
			if v.Summary == "" || v.Concern == "" {
				t.Errorf("verdict has an empty summary or concern: %+v", v)
			}
		})
	}
}

func TestModelUnreachableIsAskedOnlyOnce(t *testing.T) {
	fake := newFakeModel(t, lowReply)
	url := fake.URL
	fake.Close() // nothing is listening there any more
	m := newModel(t, url, 2*time.Second)
	for i := range 3 {
		v := m.Review(context.Background(), events.ActionHeld{Kind: events.ActionGitPush}, nil, "")
		if v.Source != events.ReviewByRules || !strings.Contains(v.Note, "did not answer") {
			t.Errorf("review %d = source %q note %q, want the rules and a did-not-answer note", i+1, v.Source, v.Note)
		}
	}
	if !m.Down() {
		t.Error("Down() is false after the model failed to answer")
	}
}

func TestModelTooSlowFallsBack(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer slow.Close()
	defer close(release)

	m := newModel(t, slow.URL, 150*time.Millisecond)
	start := time.Now()
	v := m.Review(context.Background(), events.ActionHeld{Kind: events.ActionGitPush}, nil, "")
	if v.Source != events.ReviewByRules || !strings.Contains(v.Note, "did not answer") {
		t.Errorf("verdict = source %q note %q, want the rules and a did-not-answer note", v.Source, v.Note)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("review took %v, want it cut off near the 150ms timeout", took)
	}
}

func TestNothingSecretIsSentToTheModel(t *testing.T) {
	fake := newFakeModel(t, lowReply)
	action := events.ActionHeld{
		Kind:        events.ActionGh,
		Command:     []string{"gh", "api", "-H", "Authorization: [hidden] [hidden]", "/x"},
		Destination: "GitHub",
		Subject:     "api",
	}
	excerpt := Excerpt([]byte(`{"note":"hello","api_key":"zz-top-secret-value","auth":"Bearer abcdefghijkl"}`), nil)
	newModel(t, fake.URL, 5*time.Second).Review(context.Background(), action, []string{"Planted secret word (CANARY.txt)"}, excerpt)

	sent, _ := fake.sent.Load().(string)
	for _, secret := range []string{"zz-top-secret-value", "abcdefghijkl"} {
		if strings.Contains(sent, secret) {
			t.Errorf("the request to the model contains %q:\n%s", secret, sent)
		}
	}
	for _, expected := range []string{"hello", "Planted secret word", "test-model"} {
		if !strings.Contains(sent, expected) {
			t.Errorf("the request to the model is missing %q:\n%s", expected, sent)
		}
	}
}

func TestExcerpt(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want string
	}{
		{"empty", nil, ""},
		{"binary is described, not sent", []byte{0, 1, 2, 3}, "(4 bytes that are not text)"},
		{"invalid text is described, not sent", []byte{0xff, 0xfe, 'a'}, "(3 bytes that are not text)"},
		{"text is kept", []byte("hello there\n"), "hello there"},
		{"long text is cut", []byte(strings.Repeat("a", 5000)), strings.Repeat("a", maxExcerpt) + " (cut short)"},
		{"a canary may be shown", []byte("key=sk-rehearsal-canary-abcdefghijklmnop"), "key=sk-rehearsal-canary-abcdefghijklmnop"},
		{"a real key may not", []byte("key=sk-live-abcdefghijklmnopqrstuvwx"), "key=[hidden]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Excerpt(tt.body, []string{"sk-rehearsal-canary-abcdefghijklmnop"}); got != tt.want {
				t.Errorf("Excerpt = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewModelRefusesRemoteAddresses(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"http://127.0.0.1:11434/v1", false},
		{"http://localhost:11434/v1", false},
		{"http://192.168.96.1:11434/v1", false},
		{"http://10.0.0.5:8080/v1", false},
		{"http://[::1]:11434/v1", false},
		{"http://8.8.8.8/v1", true},
		{"https://203.0.113.9/v1", true},
		{"ftp://127.0.0.1/v1", true},
		{"not a url", true},
		{"", true},
	}
	for _, tt := range tests {
		_, err := NewModel(tt.url, "m", time.Second)
		if (err != nil) != tt.wantErr {
			t.Errorf("NewModel(%q) error = %v, wantErr %t", tt.url, err, tt.wantErr)
		}
	}
}

func TestDefaultGateway(t *testing.T) {
	const table = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t0060A8C0\t00000000\t0001\t0\t0\t0\t00F0FFFF\t0\t0\t0\n" +
		"eth0\t00000000\t0160A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
	ip, err := defaultGateway(strings.NewReader(table))
	if err != nil || ip.String() != "192.168.96.1" {
		t.Errorf("defaultGateway = %v, %v; want 192.168.96.1", ip, err)
	}
	if _, err := defaultGateway(strings.NewReader("Iface\tDestination\tGateway\n")); err == nil {
		t.Error("defaultGateway found a gateway in a table with no default route")
	}
}
