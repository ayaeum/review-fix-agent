// Package trace reads rfa session transcripts (JSONL) and serves them to a web
// UI for observing/debugging an agent run: the turn-by-turn message flow, tool
// calls with their inputs/outputs and timing, token usage, and the final report.
package trace

// Usage is the token accounting surfaced per assistant turn.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Block is a simplified content block for display.
type Block struct {
	Type       string         `json:"type"` // text | thinking | tool_use | tool_result
	Text       string         `json:"text,omitempty"`
	ToolUseID  string         `json:"tool_use_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	Input      map[string]any `json:"input,omitempty"`
	ResultText string         `json:"result_text,omitempty"`
	IsError    bool           `json:"is_error,omitempty"`
}

// Entry is one decoded transcript line: a persisted message, a runtime event, or
// a session marker. Front-end renders entries in file order.
type Entry struct {
	Seq  int    `json:"seq"`
	TS   string `json:"ts"`
	Type string `json:"type"` // session_start | message | event | session_end

	// message
	Role   string  `json:"role,omitempty"`
	Blocks []Block `json:"blocks,omitempty"`

	// event
	Kind       string         `json:"kind,omitempty"`
	Tool       string         `json:"tool,omitempty"`
	ToolUseID  string         `json:"tool_use_id,omitempty"`
	Input      map[string]any `json:"input,omitempty"`
	Text       string         `json:"text,omitempty"`
	IsError    bool           `json:"is_error,omitempty"`
	Usage      *Usage         `json:"usage,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`

	// session_start / session_end / misc
	Extra map[string]any `json:"extra,omitempty"`
}

// SessionMeta summarizes a session for the list view.
type SessionMeta struct {
	ID           string `json:"id"`
	File         string `json:"file"`
	Mode         string `json:"mode"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	StartedAt    string `json:"started_at"`
	EndedAt      string `json:"ended_at,omitempty"`
	Running      bool   `json:"running"`
	Turns        int    `json:"turns"`
	ToolCalls    int    `json:"tool_calls"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	HasFindings  *bool  `json:"has_findings,omitempty"`
	HasFix       *bool  `json:"has_fix,omitempty"`
	DurationMS   int64  `json:"duration_ms"`
}

// SessionDetail is the full decoded session: metadata plus ordered entries.
type SessionDetail struct {
	Meta    SessionMeta `json:"meta"`
	Entries []Entry     `json:"entries"`
}
