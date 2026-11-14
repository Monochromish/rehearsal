package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mitm "github.com/lqqyt2423/go-mitmproxy/proxy"

	"rehearsal/internal/events"
)

func recorded(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "observe", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// decoded returns every event of one type in the log.
func decoded[T any](t *testing.T, log *events.Log, typ events.Type) []T {
	t.Helper()
	evs, err := log.Since(0)
	if err != nil {
		t.Fatal(err)
	}
	var out []T
	for _, ev := range evs {
		if ev.Type != typ {
			continue
		}
		var v T
		if err := json.Unmarshal(ev.Data, &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func messages(t *testing.T, body []byte) *mitm.Flow {
	t.Helper()
	f := bare(t, http.MethodPost, "https://api.provider.test/v1/messages")
	f.Request.Header.Set("Authorization", "Bearer session-credential")
	f.Request.Body = body
	return f
}

// The first words of what the person asked are written down once, with a
// watched value in them replaced by its name.
func TestTheFirstWordsOfTheSessionAreKept(t *testing.T) {
	h := carrying(t)
	first := []byte(`{"model":"m","messages":[{"role":"user","content":"Email the report to adal and then close the ticket for good"}]}`)
	h.rec.Request(messages(t, first))
	h.rec.Request(messages(t, []byte(`{"model":"m","messages":[{"role":"user","content":"Something else entirely, asked later on"}]}`)))
	got := decoded[events.FirstMessage](t, h.log, events.TypeFirstMessage)
	if len(got) != 1 {
		t.Fatalf("%d first-message entries, want one: %+v", len(got), got)
	}
	if strings.Contains(got[0].Words, "adal") || !strings.HasPrefix(got[0].Words, "Email the report to [") || !strings.HasSuffix(got[0].Words, "…") {
		t.Errorf("words = %q, want eight words with the username replaced by its name", got[0].Words)
	}
}

func TestProviderRequestsAreRead(t *testing.T) {
	h := carrying(t)
	request := recorded(t, "messages-request.json")
	h.rec.Request(messages(t, request))
	h.rec.Request(messages(t, request)) // the same conversation, sent again with the next turn

	reqs := decoded[events.Request](t, h.log, events.TypeRequest)
	if len(reqs) != 2 || reqs[0].Model != "claude-sonnet-4-5" || !reqs[0].Conversation {
		t.Fatalf("request events = %+v, want two naming the model and marked as a readable conversation", reqs)
	}
	actions := decoded[events.AgentAction](t, h.log, events.TypeAgentAction)
	if len(actions) != 5 {
		t.Fatalf("got %d agent actions, want the 5 tool calls written once each: %+v", len(actions), actions)
	}
	if a := actions[3]; a.Kind != "shell" || a.Tool != "Bash" || a.Text != "git commit -am 'Fix rounding' --no-verify" {
		t.Errorf("fourth action = %+v, want the commit command", a)
	}
	results := decoded[events.AgentResult](t, h.log, events.TypeAgentResult)
	if len(results) != 4 || !results[0].Failed || results[1].Failed {
		t.Errorf("results = %+v, want 4, the first one failed", results)
	}
}

func TestSecretsInCommandsAreMasked(t *testing.T) {
	h := carrying(t)
	body := `{"model":"m","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash",
		"input":{"command":"curl -H 'Authorization: Bearer abcdef0123456789' https://x.test --data token=hunter2hunter2"}}]}]}`
	h.rec.Request(messages(t, []byte(body)))
	actions := decoded[events.AgentAction](t, h.log, events.TypeAgentAction)
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	for _, secret := range []string{"abcdef0123456789", "hunter2hunter2"} {
		if strings.Contains(actions[0].Text, secret) {
			t.Errorf("the logged command contains %q: %s", secret, actions[0].Text)
		}
	}
	if !strings.Contains(actions[0].Text, "curl") {
		t.Errorf("the logged command lost its own text: %s", actions[0].Text)
	}
}

func TestUsageIsReadFromReplies(t *testing.T) {
	t.Run("a streamed reply passes through whole and is read afterwards", func(t *testing.T) {
		h := carrying(t)
		reply := recorded(t, "messages-reply-streamed.txt")
		f := messages(t, recorded(t, "messages-request.json"))
		f.Stream = true
		f.Response = &mitm.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}}

		through, err := io.ReadAll(h.rec.StreamResponseModifier(f, iotestOneByteReader{bytes.NewReader(reply)}))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(through, reply) {
			t.Fatal("the reply the agent received differs from what the provider sent")
		}
		// the copy is read off the stream's path, so wait for it
		var usage []events.ModelUsage
		for deadline := time.Now().Add(5 * time.Second); len(usage) == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			usage = decoded[events.ModelUsage](t, h.log, events.TypeModelUsage)
		}
		want := events.ModelUsage{RequestID: f.Id.String(), Model: "claude-sonnet-4-5", Input: 12, Output: 87, CacheRead: 18450, CacheWrite: 340}
		if len(usage) != 1 || usage[0] != want {
			t.Errorf("usage = %+v, want %+v", usage, want)
		}
		if got := h.rec.FinalText(); got != "All tests pass now and the fix is pushed." {
			t.Errorf("final text = %q", got)
		}
	})

	t.Run("a plain reply", func(t *testing.T) {
		h := carrying(t)
		f := messages(t, []byte(`{"model":"claude-haiku-4-5","messages":[]}`))
		f.Response = &mitm.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: recorded(t, "messages-reply-plain.json")}
		h.rec.Response(f)
		usage := decoded[events.ModelUsage](t, h.log, events.TypeModelUsage)
		want := events.ModelUsage{RequestID: f.Id.String(), Model: "claude-haiku-4-5", Input: 950, Output: 14, CacheRead: -1, CacheWrite: -1}
		if len(usage) != 1 || usage[0] != want {
			t.Errorf("usage = %+v, want %+v", usage, want)
		}
	})

	t.Run("an error reply states no usage and none is written", func(t *testing.T) {
		h := carrying(t)
		f := messages(t, []byte(`{"model":"m","messages":[]}`))
		f.Response = &mitm.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Body: []byte(`{"type":"error"}`)}
		h.rec.Response(f)
		if usage := decoded[events.ModelUsage](t, h.log, events.TypeModelUsage); len(usage) != 0 {
			t.Errorf("usage = %+v, want none", usage)
		}
	})

	t.Run("replies from other servers are not copied", func(t *testing.T) {
		h := carrying(t)
		f := bare(t, http.MethodGet, "https://docs.test/stream")
		f.Stream = true
		f.Response = &mitm.Response{StatusCode: http.StatusOK, Header: http.Header{}}
		in := strings.NewReader("data")
		if out := h.rec.StreamResponseModifier(f, in); out != io.Reader(in) {
			t.Error("a stream from a server that is not the provider was wrapped")
		}
	})
}

// iotestOneByteReader hands a stream over one byte at a time, as a slow
// network would.
type iotestOneByteReader struct{ r io.Reader }

func (o iotestOneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}
