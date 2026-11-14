package observe

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sample(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFor(t *testing.T) {
	tests := []struct {
		path  string
		known bool
	}{
		{"/v1/messages", true},
		{"/api/v1/messages", true},
		{"/v1/messages/count_tokens", false},
		{"/v1/chat/completions", false},
		{"/api/event_logging/v2/batch", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := For(tt.path) != nil; got != tt.known {
			t.Errorf("For(%q) known = %t, want %t", tt.path, got, tt.known)
		}
	}
}

func TestModel(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"recorded request", string(sample(t, "messages-request.json")), "claude-sonnet-4-5"},
		{"another provider's shape", `{"model":"gpt-x","messages":[]}`, "gpt-x"},
		{"no model named", `{"events":[1,2]}`, ""},
		{"not JSON", "name=value", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		if got := Model([]byte(tt.body)); got != tt.want {
			t.Errorf("%s: Model = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestAnthropicCalls(t *testing.T) {
	calls, results := For("/v1/messages").Calls(sample(t, "messages-request.json"))
	wantCalls := []Call{
		{ID: "toolu_01", Tool: "Bash", Kind: CallShell, Text: "go test ./..."},
		{ID: "toolu_02", Tool: "Edit", Kind: CallEdit, Text: "/home/ada/shop/checkout_test.go"},
		{ID: "toolu_03", Tool: "Read", Kind: CallOther},
		{ID: "toolu_04", Tool: "Bash", Kind: CallShell, Text: "git commit -am 'Fix rounding' --no-verify"},
		{ID: "toolu_05", Tool: "Bash", Kind: CallShell, Text: "git push origin main"},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("calls:\n got %+v\nwant %+v", calls, wantCalls)
	}
	wantResults := []Result{
		{CallID: "toolu_01", Failed: true},
		{CallID: "toolu_02"},
		{CallID: "toolu_03"},
		{CallID: "toolu_04"},
	}
	if !reflect.DeepEqual(results, wantResults) {
		t.Errorf("results:\n got %+v\nwant %+v (the last call has no result yet)", results, wantResults)
	}

	for _, body := range []string{"", "not json", `{"messages":"nope"}`, `{"messages":[{"content":"plain text"}]}`} {
		if calls, results := For("/v1/messages").Calls([]byte(body)); calls != nil || results != nil {
			t.Errorf("Calls(%q) = %v, %v; want nothing from a body with no tool calls", body, calls, results)
		}
	}
}

func TestAnthropicUsage(t *testing.T) {
	f := For("/v1/messages")
	tests := []struct {
		name     string
		reply    []byte
		streamed bool
		want     Usage
	}{
		{"recorded streamed reply", sample(t, "messages-reply-streamed.txt"), true, Usage{Input: 12, Output: 87, CacheRead: 18450, CacheWrite: 340}},
		{"recorded plain reply", sample(t, "messages-reply-plain.json"), false, Usage{Input: 950, Output: 14, CacheRead: Unknown, CacheWrite: Unknown}},
		{"stream cut off before its end", []byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5}}}\n\n"), true, Usage{Input: 5, Output: Unknown, CacheRead: Unknown, CacheWrite: Unknown}},
		{"an error reply", []byte(`{"type":"error","error":{"type":"overloaded_error"}}`), false, noUsage()},
		{"not JSON", []byte("<html>502</html>"), false, noUsage()},
		{"empty stream", nil, true, noUsage()},
		{"a plain reply read as a stream", sample(t, "messages-reply-plain.json"), true, noUsage()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := f.Usage(tt.reply, tt.streamed)
			if got != tt.want {
				t.Errorf("Usage = %+v, want %+v", got, tt.want)
			}
			if got.Known() != (tt.want != noUsage()) {
				t.Errorf("Known() = %t for %+v", got.Known(), got)
			}
		})
	}
}

func TestAnthropicFinalText(t *testing.T) {
	f := For("/v1/messages")
	if got, want := f.FinalText(sample(t, "messages-reply-streamed.txt"), true), "All tests pass now and the fix is pushed."; got != want {
		t.Errorf("streamed: FinalText = %q, want %q", got, want)
	}
	if got, want := f.FinalText(sample(t, "messages-reply-plain.json"), false), "Done. Nothing else is needed."; got != want {
		t.Errorf("plain: FinalText = %q, want %q", got, want)
	}
	if got := f.FinalText([]byte("garbage"), true); got != "" {
		t.Errorf("FinalText of garbage = %q, want nothing", got)
	}
}

func TestFirstWords(t *testing.T) {
	const path = "/v1/messages"
	tests := []struct {
		name, body, want string
	}{
		{"a plain message", `{"messages":[{"role":"user","content":"Fix the rounding in refund totals"}]}`, "Fix the rounding in refund totals"},
		{"a long one is cut at eight words",
			`{"messages":[{"role":"user","content":"Please add partial refunds to the shop API and cover them with tests"}]}`,
			"Please add partial refunds to the shop API …"},
		{"a note from the agent program comes first",
			`{"messages":[{"role":"user","content":[{"type":"text","text":"<system-reminder>Today is Saturday.</system-reminder>"},{"type":"text","text":"Rename   the\nmoney helper"}]}]}`,
			"Rename the money helper"},
		{"only the first message from the user",
			`{"messages":[{"role":"user","content":"quota"},{"role":"assistant","content":"ok"},{"role":"user","content":"Delete the old tests"}]}`, ""},
		{"a long word is cut", `{"messages":[{"role":"user","content":"Explain ` + strings.Repeat("x", 90) + `"}]}`, "Explain " + strings.Repeat("x", 52) + "…"},
		{"no message from the user", `{"messages":[{"role":"assistant","content":"Hello there"}]}`, ""},
		{"not JSON", `Fix the rounding`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FirstWords(path, []byte(tt.body)); got != tt.want {
				t.Errorf("FirstWords = %q, want %q", got, tt.want)
			}
		})
	}
	if got := FirstWords("/v1/chat/completions", []byte(`{"messages":[{"role":"user","content":"Fix the rounding"}]}`)); got != "" {
		t.Errorf("a format that is not read gave %q", got)
	}
}
