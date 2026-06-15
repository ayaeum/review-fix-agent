package agent

import (
	"strings"
	"testing"

	"github.com/review-fix-agent/rfa/internal/message"
)

func TestEstimateTokens(t *testing.T) {
	msgs := []message.Message{
		message.NewUserText(strings.Repeat("a", 400)),
	}
	est := estimateTokens(msgs)
	if est != 100 {
		t.Fatalf("expected ~100 tokens, got %d", est)
	}
}

func TestBuildCompressXML(t *testing.T) {
	msgs := []message.Message{
		{Role: message.RoleAssistant, Content: []message.Block{
			{Type: message.BlockText, Text: "I will read the file."},
			{Type: message.BlockToolUse, ToolName: "read_file"},
		}},
		{Role: message.RoleUser, Content: []message.Block{
			{Type: message.BlockToolResult, ResultText: "file contents here"},
		}},
	}
	xml := buildCompressXML(msgs)
	if !strings.Contains(xml, `role="assistant"`) {
		t.Fatal("expected assistant role in XML")
	}
	if !strings.Contains(xml, `tool_use name="read_file"`) {
		t.Fatal("expected tool_use in XML")
	}
	if !strings.Contains(xml, "file contents here") {
		t.Fatal("expected tool_result in XML")
	}
}

func TestBuildCompressXMLTruncatesLargeContent(t *testing.T) {
	msgs := []message.Message{
		{Role: message.RoleUser, Content: []message.Block{
			{Type: message.BlockToolResult, ResultText: strings.Repeat("x", 1000)},
		}},
	}
	xml := buildCompressXML(msgs)
	if !strings.Contains(xml, "...[truncated]") {
		t.Fatal("expected truncation marker for large tool_result")
	}
}

func TestStripPreviousSummary(t *testing.T) {
	orig := "initial content"
	withSummary := orig + "\n\n<previous_context_summary>\nold summary\n</previous_context_summary>"
	got := stripPreviousSummary(withSummary)
	if got != orig {
		t.Fatalf("expected %q, got %q", orig, got)
	}

	noSummary := "no summary here"
	if stripPreviousSummary(noSummary) != noSummary {
		t.Fatal("should return original when no summary marker")
	}
}

func TestMaybeCompressBelowThreshold(t *testing.T) {
	l := &Loop{}
	state := []message.Message{
		message.NewUserText("short"),
		message.NewAssistantText("response"),
	}
	got := l.maybeCompress(nil, state, nil)
	if len(got) != len(state) {
		t.Fatal("should not compress below threshold")
	}
}
