// Package model defines the ModelClient contract and provider adapters. Per the
// doc, a ModelClient does exactly three things: convert internal messages+tools
// into a provider request, consume the provider stream into a stable assistant
// message, and surface raw stream events upward for UI/usage/debugging.
package model

import (
	"context"

	"github.com/review-fix-agent/rfa/internal/message"
)

// ToolSchema is the provider-facing description of a tool.
type ToolSchema struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Request is a provider-agnostic model request.
type Request struct {
	System      string
	Messages    []message.Message
	Tools       []ToolSchema
	Model       string
	MaxTokens   int
	Temperature float64
}

// StreamEventKind classifies a raw streaming event.
type StreamEventKind string

const (
	StreamText     StreamEventKind = "text"
	StreamThinking StreamEventKind = "thinking"
	// StreamToolInput signals incremental tool input arriving (not surfaced as
	// user-visible text; mostly useful for debugging).
	StreamToolInput StreamEventKind = "tool_input"
)

// StreamEvent is a raw, incremental event from the provider stream.
type StreamEvent struct {
	Kind StreamEventKind
	Text string
}

// Client is the unified model interface. Stream consumes the provider stream,
// invoking onEvent for each incremental event, and returns the fully aggregated
// assistant message plus token usage.
type Client interface {
	Stream(ctx context.Context, req Request, onEvent func(StreamEvent)) (message.Message, message.Usage, error)
	// Name identifies the provider (for logs/transcripts).
	Name() string
}
