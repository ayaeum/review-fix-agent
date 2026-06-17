package builtin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/review-fix-agent/rfa/internal/tool"
)

func newFixCtx(cwd string) *tool.Context {
	return &tool.Context{Cwd: cwd, Mode: "fix", ReadState: tool.NewReadState(), Sink: tool.NewSink()}
}

func TestEditRequiresPriorRead(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "a.go")
	os.WriteFile(path, []byte("hello world\n"), 0o644)
	tc := newFixCtx(cwd)

	res, _ := EditTool{}.Call(context.Background(), map[string]any{
		"path": "a.go", "old_string": "hello", "new_string": "hi",
	}, tc)
	if !res.IsError {
		t.Fatal("edit without prior read should fail")
	}

	// Read first, then edit succeeds.
	if _, err := (ReadTool{}).Call(context.Background(), map[string]any{"path": "a.go"}, tc); err != nil {
		t.Fatal(err)
	}
	res, _ = EditTool{}.Call(context.Background(), map[string]any{
		"path": "a.go", "old_string": "hello", "new_string": "hi",
	}, tc)
	if res.IsError {
		t.Fatalf("edit after read should succeed: %s", res.Text)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hi world\n" {
		t.Errorf("file content = %q, want %q", string(data), "hi world\n")
	}
}

func TestEditNonUniqueRequiresReplaceAll(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "a.txt")
	os.WriteFile(path, []byte("x x x\n"), 0o644)
	tc := newFixCtx(cwd)
	ReadTool{}.Call(context.Background(), map[string]any{"path": "a.txt"}, tc)

	res, _ := EditTool{}.Call(context.Background(), map[string]any{
		"path": "a.txt", "old_string": "x", "new_string": "y",
	}, tc)
	if !res.IsError {
		t.Error("non-unique old_string without replace_all should fail")
	}

	res, _ = EditTool{}.Call(context.Background(), map[string]any{
		"path": "a.txt", "old_string": "x", "new_string": "y", "replace_all": true,
	}, tc)
	if res.IsError {
		t.Fatalf("replace_all should succeed: %s", res.Text)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "y y y\n" {
		t.Errorf("content = %q, want %q", string(data), "y y y\n")
	}
}

func TestEditDetectsStaleRead(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "a.txt")
	os.WriteFile(path, []byte("original\n"), 0o644)
	tc := newFixCtx(cwd)
	ReadTool{}.Call(context.Background(), map[string]any{"path": "a.txt"}, tc)

	// Simulate an external modification after the read.
	os.WriteFile(path, []byte("changed externally\n"), 0o644)

	res, _ := EditTool{}.Call(context.Background(), map[string]any{
		"path": "a.txt", "old_string": "changed", "new_string": "x",
	}, tc)
	if !res.IsError {
		t.Error("editing a file changed on disk since read should fail")
	}
}

func TestEditRejectsEmptyOldString(t *testing.T) {
	err := EditTool{}.Validate(map[string]any{
		"path": "a.go", "old_string": "", "new_string": "x",
	})
	if err == nil {
		t.Fatal("empty old_string must be rejected (would corrupt the file)")
	}
}
