package builtin

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/review-fix-agent/rfa/internal/tool"
)

// GrepTool searches file contents with a regular expression. Implemented in pure
// Go so it has no external dependency on ripgrep/grep.
type GrepTool struct{}

func (GrepTool) Name() string { return "grep" }

func (GrepTool) Description() string {
	return "Search file contents using a Go (RE2) regular expression. Returns matching lines as path:line:text. " +
		"Optionally restrict to a subtree (path) and a filename suffix (glob, e.g. \".go\")."
}

func (GrepTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern":     map[string]any{"type": "string", "description": "RE2 regular expression."},
			"path":        map[string]any{"type": "string", "description": "Subtree to search (optional, default working dir)."},
			"glob":        map[string]any{"type": "string", "description": "Filename suffix filter, e.g. \".go\" (optional)."},
			"max_results": map[string]any{"type": "integer", "description": "Max matching lines (optional, default 200)."},
		},
		"required": []string{"pattern"},
	}
}

func (GrepTool) ReadOnly(map[string]any) bool        { return true }
func (GrepTool) ConcurrencySafe(map[string]any) bool { return true }

func (GrepTool) Validate(input map[string]any) error {
	pat, err := strInput(input, "pattern")
	if err != nil {
		return err
	}
	if _, err := regexp.Compile(pat); err != nil {
		return fmt.Errorf("invalid regular expression: %w", err)
	}
	return nil
}

func (GrepTool) Call(_ context.Context, input map[string]any, tc *tool.Context) (tool.Result, error) {
	pat, _ := strInput(input, "pattern")
	re, err := regexp.Compile(pat)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	root := tc.Cwd
	if p, _ := input["path"].(string); p != "" {
		root = resolvePath(tc.Cwd, p)
	}
	suffix, _ := input["glob"].(string)
	max := intInput(input, "max_results", 200)

	var matches []string
	count := 0
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if suffix != "" && !strings.HasSuffix(d.Name(), suffix) {
			return nil
		}
		if count >= max {
			return filepath.SkipAll
		}
		data, err := os.ReadFile(path)
		if err != nil || isProbablyBinary(data) {
			return nil
		}
		rel := relTo(tc.Cwd, path)
		for i, line := range strings.Split(string(data), "\n") {
			if count >= max {
				break
			}
			if re.MatchString(line) {
				trimmed := line
				if len(trimmed) > 240 {
					trimmed = trimmed[:240] + "…"
				}
				matches = append(matches, fmt.Sprintf("%s:%d:%s", rel, i+1, trimmed))
				count++
			}
		}
		return nil
	})
	if walkErr != nil {
		return tool.Result{Text: walkErr.Error(), IsError: true}, nil
	}
	if len(matches) == 0 {
		return tool.Result{Text: fmt.Sprintf("no matches for /%s/", pat)}, nil
	}
	hdr := fmt.Sprintf("%d match(es) for /%s/:\n", len(matches), pat)
	return tool.Result{Text: truncate(hdr + strings.Join(matches, "\n"))}, nil
}
