package builtin

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/review-fix-agent/rfa/internal/tool"
)

// GlobTool lists files matching a glob pattern (supports **, *, ?). Pure Go.
type GlobTool struct{}

func (GlobTool) Name() string { return "glob" }

func (GlobTool) Description() string {
	return "按 glob 模式查找文件（支持 **、*、?），例如 \"**/*.go\" 或 \"internal/**/*_test.go\"。" +
		"返回按最近修改时间排序的匹配路径。"
}

func (GlobTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string", "description": "Glob 模式，例如 \"**/*.go\"。"},
			"path":    map[string]any{"type": "string", "description": "搜索根目录（可选，默认工作目录）。"},
		},
		"required": []string{"pattern"},
	}
}

func (GlobTool) ReadOnly(map[string]any) bool        { return true }
func (GlobTool) ConcurrencySafe(map[string]any) bool { return true }

func (GlobTool) Validate(input map[string]any) error {
	_, err := strInput(input, "pattern")
	return err
}

func (GlobTool) Call(_ context.Context, input map[string]any, tc *tool.Context) (tool.Result, error) {
	pattern, _ := strInput(input, "pattern")
	root := tc.Cwd
	if p, _ := input["path"].(string); p != "" {
		root = resolvePath(tc.Cwd, p)
	}
	re, err := globToRegexp(pattern)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}

	type hit struct {
		rel string
		mod int64
	}
	var hits []hit
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if re.MatchString(rel) {
			hits = append(hits, hit{rel: relTo(tc.Cwd, path), mod: modUnix(path)})
		}
		return nil
	})
	if len(hits) == 0 {
		return tool.Result{Text: fmt.Sprintf("no files match %q", pattern)}, nil
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mod > hits[j].mod })
	var b strings.Builder
	fmt.Fprintf(&b, "%d file(s) match %q:\n", len(hits), pattern)
	for _, h := range hits {
		b.WriteString(h.rel)
		b.WriteByte('\n')
	}
	return tool.Result{Text: truncate(b.String())}, nil
}

// globToRegexp converts a glob pattern into an anchored RE2 regular expression.
// ** matches across path separators; * and ? do not.
func globToRegexp(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	runes := []rune(filepath.ToSlash(glob))
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				b.WriteString(".*")
				i++
				// consume an optional trailing slash after **
				if i+1 < len(runes) && runes[i+1] == '/' {
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '(', ')', '+', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteRune(runes[i])
		default:
			b.WriteRune(runes[i])
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Errorf("invalid glob %q: %w", glob, err)
	}
	return re, nil
}
