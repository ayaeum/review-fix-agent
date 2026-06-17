package model

import (
	"strings"
	"testing"

	"github.com/review-fix-agent/rfa/internal/message"
)

// TestConvertMessagesMergesConsecutiveSameRole verifies the Anthropic adapter
// merges adjacent same-role messages (Anthropic requires alternating roles).
// The loop can append a deadline-nudge user message right after a tool_result
// user message; without merging, the API would reject the request.
func TestConvertMessagesMergesConsecutiveSameRole(t *testing.T) {
	msgs := []message.Message{
		message.NewUserText("initial"),
		{Role: message.RoleAssistant, Content: []message.Block{message.ToolUse("t1", "read_file", map[string]any{"path": "a"})}},
		{Role: message.RoleUser, Content: []message.Block{message.ToolResult("t1", "contents", false)}},
		message.NewUserText("⚠ deadline nudge"), // consecutive user message
	}
	out := convertMessages(msgs)

	// Expect 3 wire messages: user, assistant, user(merged tool_result + nudge).
	if len(out) != 3 {
		t.Fatalf("expected 3 merged messages, got %d: %+v", len(out), out)
	}
	roles := []string{out[0]["role"].(string), out[1]["role"].(string), out[2]["role"].(string)}
	if roles[0] != "user" || roles[1] != "assistant" || roles[2] != "user" {
		t.Fatalf("roles = %v, want user/assistant/user", roles)
	}
	// No two adjacent wire messages share a role.
	for i := 1; i < len(out); i++ {
		if out[i]["role"] == out[i-1]["role"] {
			t.Fatalf("adjacent messages share role %q", out[i]["role"])
		}
	}
	// The merged final user message must contain BOTH the tool_result and the nudge text.
	last := out[2]["content"].([]map[string]any)
	if len(last) != 2 {
		t.Fatalf("merged user content = %d blocks, want 2: %+v", len(last), last)
	}
	if last[0]["type"] != "tool_result" || last[1]["type"] != "text" {
		t.Errorf("merged content types = %v/%v, want tool_result/text", last[0]["type"], last[1]["type"])
	}
}

// A representative Anthropic SSE stream: message_start carries input tokens and
// an initial output_tokens of 1, then a text block, then message_delta carries
// the CUMULATIVE final output_tokens.
const anthropicSSE = `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":40,"output_tokens":1,"cache_read_input_tokens":5,"cache_creation_input_tokens":2}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}

event: message_stop
data: {"type":"message_stop"}

`

func TestAggregateSSEUsageNotDoubleCounted(t *testing.T) {
	var streamed strings.Builder
	msg, usage, err := aggregateSSE(strings.NewReader(anthropicSSE), func(e StreamEvent) {
		if e.Kind == StreamText {
			streamed.WriteString(e.Text)
		}
	})
	if err != nil {
		t.Fatalf("aggregate error: %v", err)
	}
	if got := msg.Text(); got != "Hello world" {
		t.Errorf("text = %q, want %q", got, "Hello world")
	}
	if streamed.String() != "Hello world" {
		t.Errorf("streamed text = %q, want %q", streamed.String(), "Hello world")
	}
	// message_delta output_tokens is cumulative (50), not additive with the
	// message_start initial of 1 — so the total must be 50, not 51.
	if usage.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50 (cumulative, not 1+50)", usage.OutputTokens)
	}
	if usage.InputTokens != 40 {
		t.Errorf("InputTokens = %d, want 40", usage.InputTokens)
	}
	if usage.CacheReadTokens != 5 || usage.CacheCreationTokens != 2 {
		t.Errorf("cache tokens = %d/%d, want 5/2", usage.CacheReadTokens, usage.CacheCreationTokens)
	}
}

// TestAggregateSSEToolUse verifies a tool_use block is assembled from streamed
// input_json_delta fragments.
func TestAggregateSSEToolUse(t *testing.T) {
	const sse = `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"read_file"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"a.go\"}"}}

event: message_stop
data: {"type":"message_stop"}

`
	msg, _, err := aggregateSSE(strings.NewReader(sse), nil)
	if err != nil {
		t.Fatalf("aggregate error: %v", err)
	}
	uses := msg.ToolUses()
	if len(uses) != 1 {
		t.Fatalf("expected 1 tool_use, got %d (%+v)", len(uses), msg.Content)
	}
	if uses[0].ToolName != "read_file" || uses[0].ToolUseID != "tu_1" {
		t.Errorf("tool_use = %+v", uses[0])
	}
	if p, _ := uses[0].Input["path"].(string); p != "a.go" {
		t.Errorf("tool_use input path = %q, want a.go", p)
	}
}
