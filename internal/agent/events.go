// Package agent contains the session shell, the agentic loop, and the tool
// orchestrator. It ties together model, tools, permissions, context, and
// transcript around a single explicit state machine.
package agent

import (
	"time"

	"github.com/review-fix-agent/rfa/internal/message"
)

// EventKind classifies a runtime event emitted by the loop. Runtime events are
// for UI/SDK/logging; they do not (by themselves) become model history.
type EventKind string

const (
	EvTurnStart  EventKind = "turn_start"  // a new model request is starting
	EvText       EventKind = "text"        // streaming assistant text delta
	EvThinking   EventKind = "thinking"    // streaming thinking delta
	EvAssistant  EventKind = "assistant"   // full assistant message produced
	EvToolStart  EventKind = "tool_start"  // a tool is about to run
	EvToolEnd    EventKind = "tool_end"    // a tool finished (Result in Text)
	EvToolDenied EventKind = "tool_denied" // permission denied a tool
	EvNotice     EventKind = "notice"      // informational message (hooks, recovery)
	EvError      EventKind = "error"       // loop-level error
	EvDone       EventKind = "done"        // loop finished
)

// Event is a single runtime event.
type Event struct {
	Kind      EventKind
	Text      string
	ToolName  string
	ToolUseID string
	ToolInput map[string]any
	IsError   bool
	Usage     message.Usage
	Duration  time.Duration
}
