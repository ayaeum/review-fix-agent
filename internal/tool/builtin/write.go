package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/review-fix-agent/rfa/internal/tool"
)

// WriteTool creates or overwrites a file (Fix Mode only). Overwriting an
// existing file requires it to have been read first, matching the edit tool's
// safety stance.
type WriteTool struct{}

func (WriteTool) Name() string { return "write_file" }

func (WriteTool) Description() string {
	return "创建新文件或覆盖已有文件（仅 Fix Mode 可用）。覆盖已有文件前必须先 read_file。" +
		"修改已有文件时优先使用 edit_file；write_file 主要用于真正的新文件。"
}

func (WriteTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "要写入的文件路径。"},
			"content": map[string]any{"type": "string", "description": "完整文件内容。"},
		},
		"required": []string{"path", "content"},
	}
}

func (WriteTool) ReadOnly(map[string]any) bool        { return false }
func (WriteTool) ConcurrencySafe(map[string]any) bool { return false }

func (WriteTool) Validate(input map[string]any) error {
	if _, err := strInput(input, "path"); err != nil {
		return err
	}
	_, ok := input["content"].(string)
	if !ok {
		return fmt.Errorf("field \"content\" must be a string")
	}
	return nil
}

func (WriteTool) Call(_ context.Context, input map[string]any, tc *tool.Context) (tool.Result, error) {
	path, _ := strInput(input, "path")
	content, _ := input["content"].(string)
	abs := resolvePath(tc.Cwd, path)

	if _, err := os.Stat(abs); err == nil {
		// Existing file: require a prior read before overwrite.
		if _, seen := tc.ReadState.Get(abs); !seen {
			return tool.Result{Text: fmt.Sprintf("%s exists; read it before overwriting", relTo(tc.Cwd, abs)), IsError: true}, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return tool.Result{Text: fmt.Sprintf("mkdir failed: %v", err), IsError: true}, nil
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return tool.Result{Text: fmt.Sprintf("write failed: %v", err), IsError: true}, nil
	}
	tc.ReadState.Record(abs, tool.ReadRecord{Content: content, ModUnix: modUnix(abs)})
	return tool.Result{
		Text: fmt.Sprintf("wrote %s (%d bytes)", relTo(tc.Cwd, abs), len(content)),
		Meta: map[string]any{"changed_file": relTo(tc.Cwd, abs)},
	}, nil
}
