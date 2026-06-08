// Package message defines the internal conversation model used across the agent
// runtime. It mirrors the Anthropic content-block shape (text / tool_use /
// tool_result / thinking) but stays provider-agnostic: ModelClient adapters are
// responsible for translating to/from a specific provider wire format.
//
// The doc's "tool_use/tool_result pairing invariant" lives here in spirit: a
// tool_use block carries an ID, and the matching tool_result block references it
// via ToolUseID. The orchestrator guarantees one result per use.
package message

// Role is the author of a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// BlockType enumerates the content-block variants we model.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockThinking   BlockType = "thinking"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
)

// Block is a single content block. Fields are populated based on Type; unused
// fields stay at their zero value. Keeping one struct (rather than an interface)
// makes JSONL transcripts and provider conversion straightforward.
type Block struct {
	Type BlockType `json:"type"`

	// text / thinking
	Text string `json:"text,omitempty"`

	// tool_use
	ToolUseID string         `json:"tool_use_id,omitempty"` // for tool_use: its id; for tool_result: the referenced id
	ToolName  string         `json:"tool_name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`

	// tool_result
	ResultText string `json:"result_text,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
}

// Message is a role-tagged sequence of content blocks.
type Message struct {
	Role    Role    `json:"role"`
	Content []Block `json:"content"`
}

// Text returns the concatenated text of all text blocks in the message.
func (m Message) Text() string {
	out := ""
	for _, b := range m.Content {
		if b.Type == BlockText {
			out += b.Text
		}
	}
	return out
}

// ToolUses returns all tool_use blocks in the message, in order.
func (m Message) ToolUses() []Block {
	var out []Block
	for _, b := range m.Content {
		if b.Type == BlockToolUse {
			out = append(out, b)
		}
	}
	return out
}

// NewUserText builds a plain user message containing a single text block.
func NewUserText(text string) Message {
	return Message{Role: RoleUser, Content: []Block{{Type: BlockText, Text: text}}}
}

// NewAssistantText builds a plain assistant message containing a single text block.
func NewAssistantText(text string) Message {
	return Message{Role: RoleAssistant, Content: []Block{{Type: BlockText, Text: text}}}
}

// ToolUse constructs a tool_use block.
func ToolUse(id, name string, input map[string]any) Block {
	return Block{Type: BlockToolUse, ToolUseID: id, ToolName: name, Input: input}
}

// ToolResult constructs a tool_result block paired to a tool_use id.
func ToolResult(toolUseID, text string, isError bool) Block {
	return Block{Type: BlockToolResult, ToolUseID: toolUseID, ResultText: text, IsError: isError}
}

// Usage captures token accounting returned by the provider.
type Usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// Add accumulates another usage record into this one.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheCreationTokens += o.CacheCreationTokens
}
