package model

import (
	"strings"
	"testing"

	"github.com/review-fix-agent/rfa/internal/message"
)

// A representative Responses API SSE stream: one text message item and one
// function_call item, followed by usage on completion.
const responsesSSE = `event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"delta":"Hello "}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"delta":"world"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello world"}]}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_abc","name":"read_file","arguments":""}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"path\":\"a.go\"}"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_abc","name":"read_file","arguments":"{\"path\":\"a.go\"}"}}

event: response.completed
data: {"type":"response.completed","response":{"usage":{"input_tokens":12,"output_tokens":7}}}

`

func TestAggregateResponsesSSE(t *testing.T) {
	var streamedText, streamedToolInput strings.Builder
	msg, usage, err := aggregateResponsesSSE(strings.NewReader(responsesSSE), func(e StreamEvent) {
		switch e.Kind {
		case StreamText:
			streamedText.WriteString(e.Text)
		case StreamToolInput:
			streamedToolInput.WriteString(e.Text)
		}
	})
	if err != nil {
		t.Fatalf("aggregate error: %v", err)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d (%+v)", len(msg.Content), msg.Content)
	}
	if msg.Content[0].Type != message.BlockText || msg.Content[0].Text != "Hello world" {
		t.Errorf("block[0] = %+v, want text 'Hello world'", msg.Content[0])
	}
	tu := msg.Content[1]
	if tu.Type != message.BlockToolUse || tu.ToolName != "read_file" || tu.ToolUseID != "call_abc" {
		t.Errorf("block[1] = %+v, want tool_use read_file/call_abc", tu)
	}
	if tu.Input["path"] != "a.go" {
		t.Errorf("tool input path = %v, want a.go", tu.Input["path"])
	}
	if usage.InputTokens != 12 || usage.OutputTokens != 7 {
		t.Errorf("usage = %+v, want 12/7", usage)
	}
	if streamedText.String() != "Hello world" {
		t.Errorf("streamed text = %q, want 'Hello world'", streamedText.String())
	}
	if streamedToolInput.String() != `{"path":"a.go"}` {
		t.Errorf("streamed tool input = %q", streamedToolInput.String())
	}
}

func TestResponsesFailedEvent(t *testing.T) {
	sse := `data: {"type":"response.failed","response":{"error":{"message":"model overloaded"}}}` + "\n"
	_, _, err := aggregateResponsesSSE(strings.NewReader(sse), nil)
	if err == nil || !strings.Contains(err.Error(), "model overloaded") {
		t.Errorf("expected failure error, got %v", err)
	}
}

func TestToResponsesInput(t *testing.T) {
	msgs := []message.Message{
		message.NewUserText("review this"),
		{Role: message.RoleAssistant, Content: []message.Block{
			{Type: message.BlockText, Text: "let me read it"},
			message.ToolUse("call_1", "read_file", map[string]any{"path": "a.go"}),
		}},
		{Role: message.RoleUser, Content: []message.Block{
			message.ToolResult("call_1", "file contents", false),
		}},
	}
	items := toResponsesInput(msgs)

	// Expect: user message, assistant message, function_call, function_call_output.
	if len(items) != 4 {
		t.Fatalf("expected 4 input items, got %d: %+v", len(items), items)
	}
	if items[0]["role"] != "user" {
		t.Errorf("item[0] should be user message, got %+v", items[0])
	}
	if items[1]["role"] != "assistant" {
		t.Errorf("item[1] should be assistant message, got %+v", items[1])
	}
	if items[2]["type"] != "function_call" || items[2]["call_id"] != "call_1" || items[2]["name"] != "read_file" {
		t.Errorf("item[2] should be function_call call_1/read_file, got %+v", items[2])
	}
	if items[2]["arguments"] != `{"path":"a.go"}` {
		t.Errorf("function_call arguments = %v", items[2]["arguments"])
	}
	if items[3]["type"] != "function_call_output" || items[3]["call_id"] != "call_1" || items[3]["output"] != "file contents" {
		t.Errorf("item[3] should be function_call_output for call_1, got %+v", items[3])
	}
}

func TestNewOpenAIResponsesBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://ai-gw.mjclouds.com":  "https://ai-gw.mjclouds.com/v1",
		"https://ai-gw.mjclouds.com/": "https://ai-gw.mjclouds.com/v1",
		"https://api.openai.com/v1":   "https://api.openai.com/v1",
		"https://gw.example.com/v1/":  "https://gw.example.com/v1",
	}
	for in, want := range cases {
		c := NewOpenAIResponses("k", in, "gpt-5.5")
		if c.BaseURL != want {
			t.Errorf("NewOpenAIResponses(%q).BaseURL = %q, want %q", in, c.BaseURL, want)
		}
	}
}
