package builtin

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/review-fix-agent/rfa/internal/tool"
)

type GetFileDiffTool struct{}

func (GetFileDiffTool) Name() string { return "get_file_diff" }

func (GetFileDiffTool) Description() string {
	return "返回指定文件的 diff 片段。支持同时查询多个文件。" +
		"当完整 diff 过大未内联到上下文时，用此工具按需拉取单文件变更。" +
		"不传参数时返回所有可用文件路径列表。"
}

func (GetFileDiffTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"files": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "要查询 diff 的文件路径列表。留空则返回可用文件清单。",
			},
		},
	}
}

func (GetFileDiffTool) ReadOnly(map[string]any) bool        { return true }
func (GetFileDiffTool) ConcurrencySafe(map[string]any) bool { return true }

func (GetFileDiffTool) Validate(input map[string]any) error {
	if raw, ok := input["files"]; ok {
		arr, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("\"files\" must be an array of strings")
		}
		for i, v := range arr {
			if _, ok := v.(string); !ok {
				return fmt.Errorf("files[%d] must be a string", i)
			}
		}
	}
	return nil
}

func (GetFileDiffTool) Call(_ context.Context, input map[string]any, tc *tool.Context) (tool.Result, error) {
	if tc.DiffData == nil || len(tc.DiffData) == 0 {
		return tool.Result{Text: "no diff data available"}, nil
	}

	rawFiles, _ := input["files"].([]any)
	if len(rawFiles) == 0 {
		paths := make([]string, 0, len(tc.DiffData))
		for k := range tc.DiffData {
			paths = append(paths, k)
		}
		sort.Strings(paths)
		return tool.Result{Text: "available files:\n" + strings.Join(paths, "\n")}, nil
	}

	var b strings.Builder
	for _, item := range rawFiles {
		path, _ := item.(string)
		if path == "" {
			continue
		}
		d, ok := tc.DiffData[path]
		if !ok {
			fmt.Fprintf(&b, "==== FILE: %s ====\n(not found in diff)\n\n", path)
			continue
		}
		fmt.Fprintf(&b, "==== FILE: %s ====\n%s\n\n", path, d)
	}

	result := b.String()
	if result == "" {
		return tool.Result{Text: "no matching files found in diff"}, nil
	}
	return tool.Result{Text: result}, nil
}
