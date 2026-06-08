package builtin

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/review-fix-agent/rfa/internal/tool"
)

// ReadTool reads a file and returns it with line numbers. Reads are recorded in
// the read-state so the edit tool can require a prior read.
type ReadTool struct{}

func (ReadTool) Name() string { return "read_file" }

func (ReadTool) Description() string {
	return "Read a UTF-8 text file from the working tree and return its contents with line numbers. " +
		"Use offset/limit to read a window of a large file. Always read a file before editing it."
}

func (ReadTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "File path, relative to the working directory or absolute."},
			"offset": map[string]any{"type": "integer", "description": "1-based line to start from (optional)."},
			"limit":  map[string]any{"type": "integer", "description": "Max number of lines to read (optional, default 2000)."},
		},
		"required": []string{"path"},
	}
}

func (ReadTool) ReadOnly(map[string]any) bool        { return true }
func (ReadTool) ConcurrencySafe(map[string]any) bool { return true }

func (ReadTool) Validate(input map[string]any) error {
	_, err := strInput(input, "path")
	return err
}

func (ReadTool) Call(_ context.Context, input map[string]any, tc *tool.Context) (tool.Result, error) {
	path, err := strInput(input, "path")
	if err != nil {
		return tool.Result{}, err
	}
	abs := resolvePath(tc.Cwd, path)
	data, err := os.ReadFile(abs)
	if err != nil {
		return tool.Result{Text: fmt.Sprintf("cannot read %s: %v", relTo(tc.Cwd, abs), err), IsError: true}, nil
	}
	if isProbablyBinary(data) {
		return tool.Result{Text: fmt.Sprintf("%s appears to be a binary file; not reading", relTo(tc.Cwd, abs)), IsError: true}, nil
	}

	// Record full content for edit-tool staleness checks.
	if tc.ReadState != nil {
		tc.ReadState.Record(abs, tool.ReadRecord{Content: string(data), ModUnix: modUnix(abs)})
	}

	offset := intInput(input, "offset", 1)
	if offset < 1 {
		offset = 1
	}
	limit := intInput(input, "limit", 2000)

	lines := strings.Split(string(data), "\n")
	var b strings.Builder
	count := 0
	for i := offset - 1; i < len(lines) && count < limit; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, lines[i])
		count++
	}
	if count == 0 {
		return tool.Result{Text: fmt.Sprintf("%s: no lines in requested range", relTo(tc.Cwd, abs))}, nil
	}
	header := fmt.Sprintf("%s (%d lines total)\n", relTo(tc.Cwd, abs), len(lines))
	return tool.Result{Text: truncate(header + b.String())}, nil
}
