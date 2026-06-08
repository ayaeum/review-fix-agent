package builtin

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/review-fix-agent/rfa/internal/tool"
)

// EditTool performs an exact string replacement in a file. It enforces the
// "read before write" invariant and refuses to edit a file that changed on disk
// since it was last read, preventing silent clobbering.
type EditTool struct{}

func (EditTool) Name() string { return "edit_file" }

func (EditTool) Description() string {
	return "Replace an exact substring in a file (Fix Mode only). You must read_file first. " +
		"old_string must be unique unless replace_all is true. Keep edits minimal and scoped to the known issue."
}

func (EditTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":        map[string]any{"type": "string", "description": "File to edit."},
			"old_string":  map[string]any{"type": "string", "description": "Exact text to replace (include enough context to be unique)."},
			"new_string":  map[string]any{"type": "string", "description": "Replacement text."},
			"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence (optional, default false)."},
		},
		"required": []string{"path", "old_string", "new_string"},
	}
}

func (EditTool) ReadOnly(map[string]any) bool        { return false }
func (EditTool) ConcurrencySafe(map[string]any) bool { return false }

func (EditTool) Validate(input map[string]any) error {
	for _, k := range []string{"path", "old_string", "new_string"} {
		if _, err := strInput(input, k); err != nil {
			return err
		}
	}
	oldS, _ := strInput(input, "old_string")
	newS, _ := strInput(input, "new_string")
	if oldS == newS {
		return fmt.Errorf("old_string and new_string are identical")
	}
	return nil
}

func (EditTool) Call(_ context.Context, input map[string]any, tc *tool.Context) (tool.Result, error) {
	path, _ := strInput(input, "path")
	oldS, _ := strInput(input, "old_string")
	newS, _ := strInput(input, "new_string")
	replaceAll, _ := input["replace_all"].(bool)

	abs := resolvePath(tc.Cwd, path)

	// Read-before-write: require a prior read and detect external changes.
	rec, seen := tc.ReadState.Get(abs)
	data, err := os.ReadFile(abs)
	if err != nil {
		return tool.Result{Text: fmt.Sprintf("cannot read %s: %v", relTo(tc.Cwd, abs), err), IsError: true}, nil
	}
	cur := string(data)
	if !seen {
		return tool.Result{Text: fmt.Sprintf("read %s with read_file before editing it", relTo(tc.Cwd, abs)), IsError: true}, nil
	}
	if rec.Content != cur {
		return tool.Result{Text: fmt.Sprintf("%s changed on disk since it was last read; read it again before editing", relTo(tc.Cwd, abs)), IsError: true}, nil
	}

	n := strings.Count(cur, oldS)
	if n == 0 {
		return tool.Result{Text: fmt.Sprintf("old_string not found in %s", relTo(tc.Cwd, abs)), IsError: true}, nil
	}
	if n > 1 && !replaceAll {
		return tool.Result{Text: fmt.Sprintf("old_string occurs %d times in %s; add surrounding context to make it unique, or set replace_all", n, relTo(tc.Cwd, abs)), IsError: true}, nil
	}

	var updated string
	if replaceAll {
		updated = strings.ReplaceAll(cur, oldS, newS)
	} else {
		updated = strings.Replace(cur, oldS, newS, 1)
	}

	fi, _ := os.Stat(abs)
	mode := os.FileMode(0o644)
	if fi != nil {
		mode = fi.Mode()
	}
	if err := os.WriteFile(abs, []byte(updated), mode); err != nil {
		return tool.Result{Text: fmt.Sprintf("write failed: %v", err), IsError: true}, nil
	}
	tc.ReadState.Record(abs, tool.ReadRecord{Content: updated, ModUnix: modUnix(abs)})

	replaced := 1
	if replaceAll {
		replaced = n
	}
	return tool.Result{
		Text: fmt.Sprintf("edited %s (%d replacement(s))", relTo(tc.Cwd, abs), replaced),
		Meta: map[string]any{"changed_file": relTo(tc.Cwd, abs)},
	}, nil
}
