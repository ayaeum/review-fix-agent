package model

import (
	"context"

	"github.com/review-fix-agent/rfa/internal/message"
)

// Mock is a deterministic ModelClient for tests and offline smoke runs. It
// replays scripted assistant turns, or delegates to Responder for dynamic
// behavior driven by the request (e.g. reacting to tool_results).
type Mock struct {
	Turns     []message.Message
	Responder func(req Request, turn int) message.Message
	turn      int
}

// Name identifies the provider.
func (m *Mock) Name() string { return "mock" }

// Stream returns the next scripted assistant message, emitting its text blocks
// as streaming events so UI paths exercise the same code as a real provider.
func (m *Mock) Stream(_ context.Context, req Request, onEvent func(StreamEvent)) (message.Message, message.Usage, error) {
	var msg message.Message
	switch {
	case m.Responder != nil:
		msg = m.Responder(req, m.turn)
	case m.turn < len(m.Turns):
		msg = m.Turns[m.turn]
	default:
		msg = message.NewAssistantText("(mock: no further scripted turns)")
	}
	m.turn++
	if onEvent != nil {
		for _, b := range msg.Content {
			if b.Type == message.BlockText {
				onEvent(StreamEvent{Kind: StreamText, Text: b.Text})
			}
		}
	}
	return msg, message.Usage{InputTokens: 10, OutputTokens: 5}, nil
}
