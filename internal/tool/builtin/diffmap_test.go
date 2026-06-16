package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/review-fix-agent/rfa/internal/tool"
)

func TestGetFileDiffListsAndFetches(t *testing.T) {
	tc := &tool.Context{DiffData: map[string]string{
		"a.go": "diff a",
		"b.go": "diff b",
	}}

	// No files arg → sorted available list.
	res, _ := GetFileDiffTool{}.Call(context.Background(), map[string]any{}, tc)
	if !strings.Contains(res.Text, "a.go") || !strings.Contains(res.Text, "b.go") {
		t.Errorf("list missing files: %q", res.Text)
	}

	// Specific file → its diff; missing file → annotated.
	res, _ = GetFileDiffTool{}.Call(context.Background(), map[string]any{
		"files": []any{"a.go", "missing.go"},
	}, tc)
	if !strings.Contains(res.Text, "diff a") {
		t.Errorf("expected diff a, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "not found in diff") {
		t.Errorf("expected not-found annotation, got %q", res.Text)
	}
}

// TestGetFileDiffTruncatesLargeOutput ensures the result is capped like other
// tools rather than returning an unbounded blob.
func TestGetFileDiffTruncatesLargeOutput(t *testing.T) {
	big := strings.Repeat("x", maxResultBytes*2)
	tc := &tool.Context{DiffData: map[string]string{"huge.go": big}}
	res, _ := GetFileDiffTool{}.Call(context.Background(), map[string]any{
		"files": []any{"huge.go"},
	}, tc)
	if len(res.Text) > maxResultBytes+512 {
		t.Errorf("output not truncated: len=%d (cap=%d)", len(res.Text), maxResultBytes)
	}
	if !strings.Contains(res.Text, "truncated") {
		t.Errorf("expected truncation marker, got tail: %q", res.Text[len(res.Text)-80:])
	}
}

func TestGetFileDiffNoData(t *testing.T) {
	tc := &tool.Context{}
	res, _ := GetFileDiffTool{}.Call(context.Background(), map[string]any{}, tc)
	if !strings.Contains(res.Text, "no diff data") {
		t.Errorf("expected no-data message, got %q", res.Text)
	}
}
