package model

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/review-fix-agent/rfa/internal/message"
)

// TestOpenAIStreamIntegration exercises the full OpenAIResponses.Stream path
// (buildBody -> doWithRetry -> aggregateResponsesSSE) against a fake gateway.
func TestOpenAIStreamIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, responsesSSE)
	}))
	defer srv.Close()

	c := NewOpenAIResponses("key", srv.URL, "gpt-5.5")
	c.HTTPClient = srv.Client()

	msg, usage, err := c.Stream(context.Background(), Request{
		Messages: []message.Message{message.NewUserText("review this")},
		Tools:    []ToolSchema{{Name: "read_file", InputSchema: map[string]any{"type": "object"}}},
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if msg.Text() != "Hello world" {
		t.Errorf("text = %q, want 'Hello world'", msg.Text())
	}
	if uses := msg.ToolUses(); len(uses) != 1 || uses[0].ToolName != "read_file" {
		t.Errorf("tool uses = %+v", uses)
	}
	if usage.InputTokens != 12 || usage.OutputTokens != 7 {
		t.Errorf("usage = %+v, want 12/7", usage)
	}
}

// TestAnthropicStreamIntegration exercises Anthropic.Stream end-to-end.
func TestAnthropicStreamIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, anthropicSSE)
	}))
	defer srv.Close()

	c := NewAnthropic("key", srv.URL, "claude-sonnet-4-6")
	c.HTTPClient = srv.Client()

	msg, usage, err := c.Stream(context.Background(), Request{
		Messages: []message.Message{message.NewUserText("hi")},
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if msg.Text() != "Hello world" {
		t.Errorf("text = %q, want 'Hello world'", msg.Text())
	}
	if usage.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50 (cumulative)", usage.OutputTokens)
	}
}

// TestStreamSurfacesHTTPError verifies a non-2xx (non-transient) status becomes
// an error rather than an empty message.
func TestStreamSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"bad request"}`)
	}))
	defer srv.Close()

	c := NewOpenAIResponses("key", srv.URL, "m")
	c.HTTPClient = srv.Client()
	if _, _, err := c.Stream(context.Background(), Request{Messages: []message.Message{message.NewUserText("x")}}, nil); err == nil {
		t.Error("a 400 response should surface as an error")
	}
}

func TestConvertTools(t *testing.T) {
	out := convertTools([]ToolSchema{
		{Name: "read_file", Description: "reads", InputSchema: map[string]any{"type": "object"}},
	})
	if len(out) != 1 {
		t.Fatalf("convertTools len = %d", len(out))
	}
	if out[0]["name"] != "read_file" || out[0]["description"] != "reads" {
		t.Errorf("convertTools = %+v", out[0])
	}
	if _, ok := out[0]["input_schema"]; !ok {
		t.Error("convertTools must include input_schema")
	}
}
