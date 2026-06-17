package builtin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteNewFile(t *testing.T) {
	cwd := t.TempDir()
	tc := newFixCtx(cwd)
	res, _ := WriteTool{}.Call(context.Background(), map[string]any{
		"path": "new.go", "content": "package x\n",
	}, tc)
	if res.IsError {
		t.Fatalf("writing a new file should succeed: %s", res.Text)
	}
	data, _ := os.ReadFile(filepath.Join(cwd, "new.go"))
	if string(data) != "package x\n" {
		t.Errorf("content = %q", data)
	}
}

func TestWriteExistingRequiresPriorRead(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "a.go")
	os.WriteFile(path, []byte("old\n"), 0o644)
	tc := newFixCtx(cwd)

	res, _ := WriteTool{}.Call(context.Background(), map[string]any{
		"path": "a.go", "content": "new\n",
	}, tc)
	if !res.IsError {
		t.Fatal("overwriting an unread existing file should fail")
	}

	// After reading, overwrite succeeds.
	if _, err := (ReadTool{}).Call(context.Background(), map[string]any{"path": "a.go"}, tc); err != nil {
		t.Fatal(err)
	}
	res, _ = WriteTool{}.Call(context.Background(), map[string]any{
		"path": "a.go", "content": "new\n",
	}, tc)
	if res.IsError {
		t.Fatalf("overwrite after read should succeed: %s", res.Text)
	}
}

func TestWriteDetectsExternalChange(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "a.go")
	os.WriteFile(path, []byte("v1\n"), 0o644)
	tc := newFixCtx(cwd)

	if _, err := (ReadTool{}).Call(context.Background(), map[string]any{"path": "a.go"}, tc); err != nil {
		t.Fatal(err)
	}
	// External modification after the read.
	os.WriteFile(path, []byte("v2-external\n"), 0o644)

	res, _ := WriteTool{}.Call(context.Background(), map[string]any{
		"path": "a.go", "content": "v3\n",
	}, tc)
	if !res.IsError {
		t.Fatal("write should refuse to clobber a file changed on disk since last read")
	}
	if data, _ := os.ReadFile(path); string(data) != "v2-external\n" {
		t.Errorf("external content should be intact, got %q", data)
	}
}
