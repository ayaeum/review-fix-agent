package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/review-fix-agent/rfa/internal/tool"
)

func newReadCtx(cwd string) *tool.Context {
	return &tool.Context{Cwd: cwd, Mode: "review", ReadState: tool.NewReadState(), Sink: tool.NewSink()}
}

// TestGrepNonPositiveMaxFallsBack guards against a non-positive max_results
// silently reporting "no matches" for code that does exist.
func TestGrepNonPositiveMaxFallsBack(t *testing.T) {
	cwd := t.TempDir()
	os.WriteFile(filepath.Join(cwd, "a.go"), []byte("package a\nfunc Target() {}\n"), 0o644)
	tc := newReadCtx(cwd)

	for _, max := range []int{0, -5} {
		res, err := GrepTool{}.Call(context.Background(), map[string]any{
			"pattern": "Target", "max_results": max,
		}, tc)
		if err != nil {
			t.Fatalf("max=%d: %v", max, err)
		}
		if res.IsError || !strings.Contains(res.Text, "Target") {
			t.Errorf("max=%d: expected a match, got %q", max, res.Text)
		}
	}
}

// TestReadNonPositiveLimitFallsBack guards against a non-positive limit
// returning "no lines" for a non-empty file.
func TestReadNonPositiveLimitFallsBack(t *testing.T) {
	cwd := t.TempDir()
	os.WriteFile(filepath.Join(cwd, "a.go"), []byte("line1\nline2\nline3\n"), 0o644)
	tc := newReadCtx(cwd)

	res, err := ReadTool{}.Call(context.Background(), map[string]any{
		"path": "a.go", "limit": 0,
	}, tc)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(res.Text, "line1") || !strings.Contains(res.Text, "line3") {
		t.Errorf("limit=0 should return the file, got %q", res.Text)
	}
}

// TestGrepSkipsLargeFiles verifies grep does not read files above the size cap
// into memory (it skips them), while still searching normal-sized files.
func TestGrepSkipsLargeFiles(t *testing.T) {
	cwd := t.TempDir()
	// A small file that contains the pattern.
	if err := os.WriteFile(filepath.Join(cwd, "small.go"), []byte("package a\n// TARGET here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A large file (> maxGrepFileBytes) that also contains the pattern; it must be skipped.
	big := make([]byte, maxGrepFileBytes+1024)
	copy(big, []byte("TARGET\n"))
	if err := os.WriteFile(filepath.Join(cwd, "big.txt"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	tc := newReadCtx(cwd)
	res, err := GrepTool{}.Call(context.Background(), map[string]any{"pattern": "TARGET"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "small.go") {
		t.Errorf("expected match in small.go, got %q", res.Text)
	}
	if strings.Contains(res.Text, "big.txt") {
		t.Errorf("large file should have been skipped, got %q", res.Text)
	}
}
