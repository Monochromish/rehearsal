package observe

import (
	"reflect"
	"testing"
)

func TestAnthropicReplyCalls(t *testing.T) {
	f := For("/v1/messages")

	// the sample's one call, opened with an empty input and sent in one piece
	if got, want := f.ReplyCalls(sample(t, "messages-reply-streamed.txt"), true), []Call{{ID: "toolu_06", Tool: "Bash", Kind: CallShell, Text: "git status"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("streamed sample: %+v, want %+v", got, want)
	}

	// two calls in one reply, the first one's input split across pieces,
	// with text and a thinking block around them
	const stream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_02","role":"assistant","content":[],"usage":{"input_tokens":2}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Run it."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_10","name":"Bash","input":{},"caller":{"type":"direct"}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\": \"node src/che"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"ckout.js\", \"description\": \"Run the script\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_11","name":"Edit","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\": \"/home/ada/shop/src/checkout.js\", \"old_string\": \"floor\", \"new_string\": \"round\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":40}}

event: message_stop
data: {"type":"message_stop"}
`
	want := []Call{
		{ID: "toolu_10", Tool: "Bash", Kind: CallShell, Text: "node src/checkout.js"},
		{ID: "toolu_11", Tool: "Edit", Kind: CallEdit, Text: "/home/ada/shop/src/checkout.js"},
	}
	if got := f.ReplyCalls([]byte(stream), true); !reflect.DeepEqual(got, want) {
		t.Errorf("streamed:\n got %+v\nwant %+v", got, want)
	}

	// a plain reply has its calls whole
	const plain = `{"type":"message","role":"assistant","content":[{"type":"text","text":"Running the tests."},{"type":"tool_use","id":"toolu_12","name":"Bash","input":{"command":"go test ./..."}},{"type":"tool_use","id":"toolu_13","name":"Read","input":{"file_path":"/home/ada/shop/README.md"}}],"usage":{"input_tokens":3,"output_tokens":9}}`
	want = []Call{{ID: "toolu_12", Tool: "Bash", Kind: CallShell, Text: "go test ./..."}, {ID: "toolu_13", Tool: "Read", Kind: CallOther}}
	if got := f.ReplyCalls([]byte(plain), false); !reflect.DeepEqual(got, want) {
		t.Errorf("plain:\n got %+v\nwant %+v", got, want)
	}

	// a stream cut short still says a call was made, with nothing to show for it
	const cut = "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_14\",\"name\":\"Bash\",\"input\":{}}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"command\\\": \\\"rm -rf\"}}\n\n"
	if got, want := f.ReplyCalls([]byte(cut), true), []Call{{ID: "toolu_14", Tool: "Bash", Kind: CallShell}}; !reflect.DeepEqual(got, want) {
		t.Errorf("cut short: %+v, want %+v", got, want)
	}

	for _, reply := range []string{"", "not json", `{"type":"error"}`, "data: [DONE]\n\n", `{"content":[{"type":"text","text":"No tools needed."}]}`} {
		if got := f.ReplyCalls([]byte(reply), false); got != nil {
			t.Errorf("ReplyCalls(%q) = %+v, want none", reply, got)
		}
		if got := f.ReplyCalls([]byte(reply), true); got != nil {
			t.Errorf("ReplyCalls(%q), streamed = %+v, want none", reply, got)
		}
	}
}
