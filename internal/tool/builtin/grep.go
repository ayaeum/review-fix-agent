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
	return "使用 Go（RE2）正则表达式搜索文件内容。返回格式为 path:line:text 的匹配行。" +
		"可选地用 path 限定子目录，用 glob 限定文件名后缀（例如 \".go\"）。"
}

func (GrepTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern":     map[string]any{"type": "string", "description": "RE2 正则表达式。"},
			"path":        map[string]any{"type": "string", "description": "要搜索的子树（可选，默认工作目录）。"},
			"glob":        map[string]any{"type": "string", "description": "文件名后缀过滤，例如 \".go\"（可选）。"},
			"max_results": map[string]any{"type": "integer", "description": "最多返回的匹配行数（可选，默认 200）。"},
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
	if max <= 0 {
		// A non-positive cap would report "no matches" for any pattern, which a
		// reviewer could misread as "this code does not exist". Use the default.
		max = 200
	}

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
