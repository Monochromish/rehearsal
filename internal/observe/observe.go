// Package observe reads facts out of the traffic between an agent and its
// model provider: which model was asked, how many tokens were used, and
// what the agent did with its tools. It only reports what is in the bytes
// it is given. Anything it cannot read is "not available", never a guess.
package observe

import (
	"bufio"
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

// CallKind sorts what an agent did with a tool.
type CallKind string

const (
	CallShell CallKind = "shell" // ran a command
	CallEdit  CallKind = "edit"  // wrote or changed a file
	CallOther CallKind = "other"
)

// Call is one use of a tool by the agent, as written in the conversation
// it sends to the model.
type Call struct {
	ID   string
	Tool string
	Kind CallKind
	// the command for a shell call, the file path for an edit
	Text string
}

// Result is how a tool call turned out, once the agent has reported it.
type Result struct {
	CallID string
	Failed bool
}

// Unknown marks a token count the traffic did not state.
const Unknown = -1

// Usage is the token counts from one reply. A count the reply does not
// state is Unknown.
type Usage struct {
	Input      int
	Output     int
	CacheRead  int
	CacheWrite int
}

func noUsage() Usage {
	return Usage{Unknown, Unknown, Unknown, Unknown}
}

// Known reports whether the reply stated any count at all.
func (u Usage) Known() bool {
	return u != noUsage()
}

// Format reads one provider's wire format.
type Format interface {
	// Calls lists the tool calls and results in a request's conversation.
	Calls(request []byte) ([]Call, []Result)
	// ReplyCalls lists the tool calls the model asks for in a reply, plain
	// or streamed. An agent that sends only the newest part of the
	// conversation never repeats them in a request, so the reply is the
	// one place they are sure to be.
	ReplyCalls(reply []byte, streamed bool) []Call
	// Usage reads token counts from a reply, plain or streamed.
	Usage(reply []byte, streamed bool) Usage
	// FinalText is what the model said in a reply, without its tool calls.
	FinalText(reply []byte, streamed bool) string
}

// For returns the format of requests to path, or nil when it is not one
// this package can read.
func For(path string) Format {
	if strings.HasSuffix(path, "/v1/messages") {
		return anthropic{}
	}
	return nil
}

// Model is the model named in a request, or "" if it names none. Every
// provider this is used with puts it in the same place.
func Model(request []byte) string {
	var body struct {
		Model string `json:"model"`
	}
	// a request that is not JSON names no model
	if json.Unmarshal(request, &body) != nil {
		return ""
	}
	return body.Model
}

// anthropic reads the Anthropic Messages format.
type anthropic struct{}

type anthropicBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Text      string          `json:"text"`
}

// tools Claude Code names for changing files
var editTools = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}

func (anthropic) Calls(request []byte) ([]Call, []Result) {
	var body struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(request, &body) != nil {
		return nil, nil
	}
	var calls []Call
	var results []Result
	for _, m := range body.Messages {
		var blocks []anthropicBlock
		// content may also be a plain string, which holds no tool calls
		if json.Unmarshal(m.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				calls = append(calls, anthropicCall(b))
			case "tool_result":
				results = append(results, Result{CallID: b.ToolUseID, Failed: b.IsError})
			}
		}
	}
	return calls, results
}

func anthropicCall(b anthropicBlock) Call {
	var input struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
		Path     string `json:"notebook_path"`
	}
	// an input in some other shape just has no command or path to show
	_ = json.Unmarshal(b.Input, &input)
	c := Call{ID: b.ID, Tool: b.Name, Kind: CallOther}
	switch {
	case b.Name == "Bash":
		c.Kind, c.Text = CallShell, input.Command
	case slices.Contains(editTools, b.Name):
		c.Kind, c.Text = CallEdit, input.FilePath+input.Path
	}
	return c
}

type anthropicUsage struct {
	Input      *int `json:"input_tokens"`
	Output     *int `json:"output_tokens"`
	CacheRead  *int `json:"cache_read_input_tokens"`
	CacheWrite *int `json:"cache_creation_input_tokens"`
}

// into copies the counts that are stated over what is known so far.
func (a anthropicUsage) into(u *Usage) {
	for _, f := range []struct {
		from *int
		to   *int
	}{{a.Input, &u.Input}, {a.Output, &u.Output}, {a.CacheRead, &u.CacheRead}, {a.CacheWrite, &u.CacheWrite}} {
		if f.from != nil {
			*f.to = *f.from
		}
	}
}

// anthropicEvent is one event of a streamed reply, or a whole plain reply:
// the fields that matter have the same names in both.
type anthropicEvent struct {
	Type    string `json:"type"`
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	Usage anthropicUsage `json:"usage"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
		// a piece of a tool call's input, which a stream sends as text
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Content []anthropicBlock `json:"content"`
	// in a stream: which block an event is about, and the block as it opens
	Index        int            `json:"index"`
	ContentBlock anthropicBlock `json:"content_block"`
}

// events calls each with every JSON object in a reply: the reply itself
// when plain, each "data:" line when streamed.
func (anthropic) events(reply []byte, streamed bool, each func(anthropicEvent)) {
	if !streamed {
		var ev anthropicEvent
		if json.Unmarshal(reply, &ev) == nil {
			each(ev)
		}
		return
	}
	lines := bufio.NewScanner(bytes.NewReader(reply))
	lines.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for lines.Scan() {
		data, ok := bytes.CutPrefix(lines.Bytes(), []byte("data:"))
		if !ok {
			continue
		}
		var ev anthropicEvent
		// keep-alive and end-of-stream lines are not JSON
		if json.Unmarshal(bytes.TrimSpace(data), &ev) == nil {
			each(ev)
		}
	}
}

func (a anthropic) Usage(reply []byte, streamed bool) Usage {
	u := noUsage()
	a.events(reply, streamed, func(ev anthropicEvent) {
		// a stream states the input counts at its start and the final
		// output count at its end; a plain reply states them all at once
		ev.Message.Usage.into(&u)
		ev.Usage.into(&u)
	})
	return u
}

func (a anthropic) ReplyCalls(reply []byte, streamed bool) []Call {
	var calls []Call
	// A stream opens a tool call with its name and an empty input, sends
	// the input as pieces of JSON text, and closes it.
	type opened struct {
		block anthropicBlock
		input strings.Builder
	}
	open := map[int]*opened{}
	var order []int
	a.events(reply, streamed, func(ev anthropicEvent) {
		for _, b := range ev.Content {
			if b.Type == "tool_use" {
				calls = append(calls, anthropicCall(b))
			}
		}
		switch ev.Type {
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" && open[ev.Index] == nil {
				open[ev.Index] = &opened{block: ev.ContentBlock}
				order = append(order, ev.Index)
			}
		case "content_block_delta":
			if o := open[ev.Index]; o != nil && ev.Delta.Type == "input_json_delta" {
				o.input.WriteString(ev.Delta.PartialJSON)
			}
		}
	})
	for _, i := range order {
		o := open[i]
		// a stream that was cut short leaves an input that cannot be read,
		// and the call then has no command or path to show
		if o.input.Len() > 0 {
			o.block.Input = json.RawMessage(o.input.String())
		}
		calls = append(calls, anthropicCall(o.block))
	}
	return calls
}

func (a anthropic) FinalText(reply []byte, streamed bool) string {
	var text strings.Builder
	a.events(reply, streamed, func(ev anthropicEvent) {
		if ev.Delta.Type == "text_delta" {
			text.WriteString(ev.Delta.Text)
		}
		for _, b := range ev.Content {
			if b.Type == "text" {
				text.WriteString(b.Text)
			}
		}
	})
	return text.String()
}

// How much of the person's first message is kept.
const (
	firstWords = 8
	firstRunes = 60
)

// FirstWords is the start of the first thing the person asked the agent,
// as it stands in a request's conversation: the first few words of the
// first message from the user that is more than a single word and is not
// a note the agent program wrote for the model. It is "" when the request
// is not in a format this package reads, or holds no such message.
func FirstWords(path string, request []byte) string {
	if For(path) == nil {
		return ""
	}
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(request, &body) != nil {
		return ""
	}
	for _, m := range body.Messages {
		if m.Role != "user" {
			continue
		}
		var texts []string
		var plain string
		var blocks []anthropicBlock
		switch {
		case json.Unmarshal(m.Content, &plain) == nil:
			texts = []string{plain}
		case json.Unmarshal(m.Content, &blocks) == nil:
			for _, b := range blocks {
				if b.Type == "text" {
					texts = append(texts, b.Text)
				}
			}
		}
		for _, text := range texts {
			words := strings.Fields(text)
			// what an agent program adds for the model is wrapped in a tag,
			// and its own one-word checks are not something a person asked
			if len(words) < 2 || strings.HasPrefix(words[0], "<") {
				continue
			}
			if len(words) > firstWords {
				words = append(words[:firstWords], "…")
			}
			out := strings.Join(words, " ")
			if runes := []rune(out); len(runes) > firstRunes {
				out = string(runes[:firstRunes]) + "…"
			}
			return out
		}
		return "" // only the first message from the user is looked at
	}
	return ""
}
