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
	return "Create a new file or overwrite an existing one (Fix Mode only). To overwrite, read_file it first. " +
		"Prefer edit_file for changes to existing files; use write_file for genuinely new files."
}

func (WriteTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "File path to write."},
			"content": map[string]any{"type": "string", "description": "Full file contents."},
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
