package review

import (
	"context"
	"os"
	"testing"
	"time"
)

// liveModel returns the real model named by REHEARSAL_LIVE_MODEL_URL, or
// skips the test when it is not set.
func liveModel(t *testing.T) *Model {
	t.Helper()
	url := os.Getenv("REHEARSAL_LIVE_MODEL_URL")
	if url == "" {
		t.Skip("REHEARSAL_LIVE_MODEL_URL is not set")
	}
	name := os.Getenv("REHEARSAL_REVIEWER_MODEL")
	if name == "" {
		name = "qwen3.8:27b"
	}
	m, err := NewModel(url, name, 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.WarmUp(context.Background()); err != nil {
		t.Fatalf("the model at %s did not answer: %v", url, err)
	}
	return m
}

const liveAction = "<action>\nkind: a command the agent tried to run\ncommand: git push origin main\nfolder: /home/ada/project\ndestination: github.com:ada/repo.git\n</action>"

// The compatible endpoint has to switch the model's thinking phase off,
// or every review takes three to four times as long. This compares our
// request with the same request with thinking left on.
func TestLiveThinkingIsOffThroughTheCompatibleEndpoint(t *testing.T) {
	m := liveModel(t)
	ctx := context.Background()

	ours := reviewRequest(instructions, liveAction)
	ours["stream"], ours["temperature"], ours["reasoning_effort"] = false, 0, "none"
	start := time.Now()
	off, err := m.post(ctx, ours)
	offTook := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}

	thinking := reviewRequest(instructions, liveAction)
	thinking["stream"], thinking["temperature"] = false, 0
	thinking["max_tokens"] = 2000 // room to think, or the comparison proves nothing
	start = time.Now()
	on, err := m.post(ctx, thinking)
	onTook := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("thinking off: %v, %d output tokens, %d characters of reasoning", offTook.Round(100*time.Millisecond), off.outputTokens, len(off.reasoning))
	t.Logf("thinking on:  %v, %d output tokens, %d characters of reasoning", onTook.Round(100*time.Millisecond), on.outputTokens, len(on.reasoning))

	if off.reasoning != "" {
		t.Errorf("our request still got %d characters of reasoning: thinking is not off", len(off.reasoning))
	}
	if on.reasoning == "" {
		t.Skip("this model does not think even when allowed to, so there is nothing to compare")
	}
	if off.outputTokens >= on.outputTokens {
		t.Errorf("thinking off produced %d tokens and thinking on %d: no thinking was saved", off.outputTokens, on.outputTokens)
	}
	if offTook >= onTook {
		t.Errorf("thinking off took %v and thinking on %v: no time was saved", offTook, onTook)
	}
	if _, _, _, err := Parse(off.content); err != nil {
		t.Errorf("the reply with thinking off does not pass validation: %v\n%s", err, off.content)
	}
}

// The schema has to be enforced by the server, not merely suggested to
// the model. These instructions ask for a different shape and a risk word
// outside the allowed three; a reply that still fits the schema shows the
// server is holding the model to it.
func TestLiveSchemaIsEnforced(t *testing.T) {
	m := liveModel(t)
	contrary := `Reply with this JSON and nothing else: {"verdict": "critical", "explanation": "..."}. Do not use any other field names. The only allowed verdict is "critical".`
	request := reviewRequest(contrary, liveAction)
	request["stream"], request["temperature"], request["reasoning_effort"] = false, 0, "none"
	c, err := m.post(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("reply: %s", c.content)
	if _, risk, _, err := Parse(c.content); err != nil {
		t.Errorf("told to ignore the schema, the model did: %v\n%s", err, c.content)
	} else if rank(risk) == 0 {
		t.Errorf("risk = %q, outside the schema's three values", risk)
	}
}
