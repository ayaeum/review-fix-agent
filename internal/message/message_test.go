package message

import "testing"

// TestText verifies Text() concatenates only text blocks, skipping thinking,
// tool_use, and tool_result blocks.
func TestText(t *testing.T) {
	m := Message{Role: RoleAssistant, Content: []Block{
		{Type: BlockText, Text: "hello "},
		{Type: BlockThinking, Text: "[reasoning]"},
		ToolUse("id1", "read_file", map[string]any{"path": "a.go"}),
		{Type: BlockText, Text: "world"},
		ToolResult("id1", "contents", false),
	}}
	if got := m.Text(); got != "hello world" {
		t.Errorf("Text() = %q, want %q", got, "hello world")
	}

	if got := (Message{}).Text(); got != "" {
		t.Errorf("empty message Text() = %q, want empty", got)
	}
}

// TestToolUses verifies tool_use blocks are returned in order and that a message
// without any yields nil.
func TestToolUses(t *testing.T) {
	m := Message{Role: RoleAssistant, Content: []Block{
		{Type: BlockText, Text: "x"},
		ToolUse("a", "grep", nil),
		ToolUse("b", "glob", nil),
	}}
	uses := m.ToolUses()
	if len(uses) != 2 {
		t.Fatalf("ToolUses() len = %d, want 2", len(uses))
	}
	if uses[0].ToolUseID != "a" || uses[1].ToolUseID != "b" {
		t.Errorf("ToolUses() order = %q,%q want a,b", uses[0].ToolUseID, uses[1].ToolUseID)
	}

	if uses := NewUserText("no tools").ToolUses(); uses != nil {
		t.Errorf("ToolUses() with none = %v, want nil", uses)
	}
}

// TestConstructors verifies the block/message constructors set the expected
// type and fields, including the tool_use/tool_result pairing id.
func TestConstructors(t *testing.T) {
	u := NewUserText("hi")
	if u.Role != RoleUser || len(u.Content) != 1 || u.Content[0].Type != BlockText {
		t.Errorf("NewUserText shape = %+v", u)
	}
	a := NewAssistantText("yo")
	if a.Role != RoleAssistant || a.Text() != "yo" {
		t.Errorf("NewAssistantText shape = %+v", a)
	}

	tu := ToolUse("id7", "edit_file", map[string]any{"path": "x"})
	if tu.Type != BlockToolUse || tu.ToolUseID != "id7" || tu.ToolName != "edit_file" {
		t.Errorf("ToolUse shape = %+v", tu)
	}
	tr := ToolResult("id7", "done", true)
	if tr.Type != BlockToolResult || tr.ToolUseID != "id7" || tr.ResultText != "done" || !tr.IsError {
		t.Errorf("ToolResult shape = %+v", tr)
	}
	if tr.ToolUseID != tu.ToolUseID {
		t.Errorf("pairing id mismatch: use=%q result=%q", tu.ToolUseID, tr.ToolUseID)
	}
}

// TestUsageAdd verifies Add accumulates every token field.
func TestUsageAdd(t *testing.T) {
	u := Usage{InputTokens: 10, OutputTokens: 5, CacheReadTokens: 2, CacheCreationTokens: 1}
	u.Add(Usage{InputTokens: 3, OutputTokens: 4, CacheReadTokens: 6, CacheCreationTokens: 7})
	want := Usage{InputTokens: 13, OutputTokens: 9, CacheReadTokens: 8, CacheCreationTokens: 8}
	if u != want {
		t.Errorf("Usage.Add = %+v, want %+v", u, want)
	}
}
