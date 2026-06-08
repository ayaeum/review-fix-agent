package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/review-fix-agent/rfa/internal/tool"
)

// ReportFindingsTool is the Review Mode finalizer. The model calls it exactly
// once to emit the structured, evidence-bound findings. Capturing structured
// output via a tool (rather than parsing free text) makes the result reliable.
type ReportFindingsTool struct{}

func (ReportFindingsTool) Name() string { return "report_findings" }

func (ReportFindingsTool) Description() string {
	return "提交最终结构化代码审查报告。审查完成时必须且只调用一次。" +
		"每个 finding 都必须绑定到文件和行号，并包含具体 evidence 和 impact。" +
		"不要包含纯风格问题，除非它影响正确性、可维护性或安全性。" +
		"所有面向人的字符串都使用与用户请求相同的自然语言；用户请求是中文时必须使用中文。"
}

func (ReportFindingsTool) InputSchema() map[string]any {
	finding := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"severity":      map[string]any{"type": "string", "enum": []string{"high", "medium", "low", "info"}},
			"file":          map[string]any{"type": "string"},
			"line":          map[string]any{"type": "integer"},
			"title":         map[string]any{"type": "string"},
			"evidence":      map[string]any{"type": "string", "description": "具体证据：触发问题的失败路径、调用方行为或取值。"},
			"impact":        map[string]any{"type": "string", "description": "该问题导致的行为回归或故障。"},
			"suggested_fix": map[string]any{"type": "string"},
		},
		"required": []string{"severity", "file", "line", "title", "evidence", "impact"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"findings":       map[string]any{"type": "array", "items": finding},
			"reviewed_scope": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"not_reviewed":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"verification":   map[string]any{"type": "string", "description": "执行了哪些验证，或为什么没有验证（例如 'not run; review-only mode'）。"},
		},
		"required": []string{"findings", "reviewed_scope"},
	}
}

func (ReportFindingsTool) ReadOnly(map[string]any) bool        { return true }
func (ReportFindingsTool) ConcurrencySafe(map[string]any) bool { return false }

func (ReportFindingsTool) Validate(input map[string]any) error {
	raw, ok := input["findings"]
	if !ok {
		return fmt.Errorf("missing required field \"findings\"")
	}
	arr, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("\"findings\" must be an array")
	}
	required := []string{"severity", "file", "line", "title", "evidence", "impact"}
	for i, f := range arr {
		fm, ok := f.(map[string]any)
		if !ok {
			return fmt.Errorf("findings[%d] must be an object", i)
		}
		for _, k := range required {
			if _, ok := fm[k]; !ok {
				return fmt.Errorf("findings[%d] missing required field %q", i, k)
			}
		}
		if !validSeverity(stringValue(fm["severity"])) {
			return fmt.Errorf("findings[%d].severity must be one of high, medium, low, info", i)
		}
		line, ok := intValue(fm["line"])
		if !ok || line < 1 {
			return fmt.Errorf("findings[%d].line must be a positive integer", i)
		}
		for _, k := range []string{"file", "title", "evidence", "impact"} {
			if strings.TrimSpace(stringValue(fm[k])) == "" {
				return fmt.Errorf("findings[%d].%s must not be empty", i, k)
			}
		}
	}
	scope, ok := input["reviewed_scope"]
	if !ok {
		return fmt.Errorf("missing required field \"reviewed_scope\"")
	}
	if err := validateStringArray("reviewed_scope", scope, true); err != nil {
		return err
	}
	if raw, ok := input["not_reviewed"]; ok {
		if err := validateStringArray("not_reviewed", raw, false); err != nil {
			return err
		}
	}
	if raw, ok := input["verification"]; ok && strings.TrimSpace(stringValue(raw)) == "" {
		return fmt.Errorf("verification must not be empty when provided")
	}
	return nil
}

func (ReportFindingsTool) Call(_ context.Context, input map[string]any, tc *tool.Context) (tool.Result, error) {
	if tc.Sink != nil {
		tc.Sink.SetFindings(input)
	}
	n := 0
	if arr, ok := input["findings"].([]any); ok {
		n = len(arr)
	}
	return tool.Result{Text: fmt.Sprintf("review report recorded: %d finding(s)", n), Meta: input}, nil
}

// ReportFixTool is the Fix Mode finalizer: a patch summary plus verification
// outcome and residual risk. Verification results are a first-class output.
type ReportFixTool struct{}

func (ReportFixTool) Name() string { return "report_fix" }

func (ReportFixTool) Description() string {
	return "提交最终修复报告。应用最小补丁并完成验证后，必须且只调用一次。" +
		"说明补丁范围，列出变更文件，并如实报告验证结果，包括失败。" +
		"所有面向人的字符串都使用与用户请求相同的自然语言；用户请求是中文时必须使用中文。"
}

func (ReportFixTool) InputSchema() map[string]any {
	verification := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
			"passed":  map[string]any{"type": "boolean"},
			"summary": map[string]any{"type": "string"},
		},
		"required": []string{"command", "passed", "summary"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary":       map[string]any{"type": "string", "description": "结合已知问题说明改了什么以及为什么改。"},
			"patch_scope":   map[string]any{"type": "string", "description": "变更边界；说明哪些内容是有意不改的。"},
			"changed_files": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"verification":  map[string]any{"type": "array", "items": verification, "description": "执行过的命令及其结果。"},
			"residual_risk": map[string]any{"type": "string", "description": "仍有风险、未验证或超出范围的事项。"},
		},
		"required": []string{"summary", "changed_files", "verification"},
	}
}

func (ReportFixTool) ReadOnly(map[string]any) bool        { return true }
func (ReportFixTool) ConcurrencySafe(map[string]any) bool { return false }

func (ReportFixTool) Validate(input map[string]any) error {
	for _, k := range []string{"summary", "changed_files", "verification"} {
		if _, ok := input[k]; !ok {
			return fmt.Errorf("missing required field %q", k)
		}
	}
	if strings.TrimSpace(stringValue(input["summary"])) == "" {
		return fmt.Errorf("\"summary\" must not be empty")
	}
	if raw, ok := input["patch_scope"]; ok && strings.TrimSpace(stringValue(raw)) == "" {
		return fmt.Errorf("\"patch_scope\" must not be empty when provided")
	}
	if err := validateStringArray("changed_files", input["changed_files"], false); err != nil {
		return err
	}
	arr, ok := input["verification"].([]any)
	if !ok {
		return fmt.Errorf("\"verification\" must be an array")
	}
	for i, v := range arr {
		vm, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("verification[%d] must be an object", i)
		}
		for _, k := range []string{"command", "passed", "summary"} {
			if _, ok := vm[k]; !ok {
				return fmt.Errorf("verification[%d] missing required field %q", i, k)
			}
		}
		if strings.TrimSpace(stringValue(vm["command"])) == "" {
			return fmt.Errorf("verification[%d].command must not be empty", i)
		}
		if _, ok := vm["passed"].(bool); !ok {
			return fmt.Errorf("verification[%d].passed must be a boolean", i)
		}
		if strings.TrimSpace(stringValue(vm["summary"])) == "" {
			return fmt.Errorf("verification[%d].summary must not be empty", i)
		}
	}
	if hasFailedVerification(arr) && strings.TrimSpace(stringValue(input["residual_risk"])) == "" {
		return fmt.Errorf("\"residual_risk\" must explain failed verification")
	}
	return nil
}

func (ReportFixTool) Call(_ context.Context, input map[string]any, tc *tool.Context) (tool.Result, error) {
	if err := validateReportedVerification(input, tc); err != nil {
		return tool.Result{}, err
	}
	if err := validateReportedChangedFiles(input, tc); err != nil {
		return tool.Result{}, err
	}
	if tc.Sink != nil {
		tc.Sink.SetFix(input)
	}
	return tool.Result{Text: "fix report recorded", Meta: input}, nil
}

func validSeverity(s string) bool {
	switch s {
	case "high", "medium", "low", "info":
		return true
	default:
		return false
	}
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func intValue(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}
	return 0, false
}

func validateStringArray(name string, raw any, requireNonEmpty bool) error {
	arr, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("\"%s\" must be an array", name)
	}
	if requireNonEmpty && len(arr) == 0 {
		return fmt.Errorf("\"%s\" must not be empty", name)
	}
	for i, v := range arr {
		if strings.TrimSpace(stringValue(v)) == "" {
			return fmt.Errorf("%s[%d] must be a non-empty string", name, i)
		}
	}
	return nil
}

func validateReportedVerification(input map[string]any, tc *tool.Context) error {
	raw, ok := input["verification"].([]any)
	if !ok || len(raw) == 0 || tc == nil || tc.Sink == nil {
		return nil
	}
	records := tc.Sink.CommandRecords()
	byCommand := map[string]tool.CommandRecord{}
	for _, r := range records {
		byCommand[normalizeCommand(r.Command)] = r
	}
	for i, v := range raw {
		vm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		cmd := stringValue(vm["command"])
		rec, ok := byCommand[normalizeCommand(cmd)]
		if !ok {
			return fmt.Errorf("verification[%d].command %q was not executed through run_command", i, cmd)
		}
		if passed, _ := vm["passed"].(bool); passed != rec.Passed {
			return fmt.Errorf("verification[%d].passed for %q does not match actual run_command result", i, cmd)
		}
	}
	return nil
}

func normalizeCommand(cmd string) string {
	return strings.Join(strings.Fields(cmd), " ")
}

func validateReportedChangedFiles(input map[string]any, tc *tool.Context) error {
	raw, ok := input["changed_files"].([]any)
	if !ok || tc == nil || tc.Sink == nil {
		return nil
	}
	actual := tc.Sink.ChangedFiles()
	if len(actual) == 0 {
		return nil
	}
	if len(inputArray(input["verification"])) == 0 && strings.TrimSpace(stringValue(input["residual_risk"])) == "" {
		return fmt.Errorf("\"residual_risk\" must explain why changed files were not verified")
	}
	reported := map[string]bool{}
	for _, v := range raw {
		reported[cleanReportPath(stringValue(v))] = true
	}
	for _, p := range actual {
		if !reported[cleanReportPath(p)] {
			return fmt.Errorf("changed_files is missing file changed by agent: %s", p)
		}
	}
	actualSet := map[string]bool{}
	for _, p := range actual {
		actualSet[cleanReportPath(p)] = true
	}
	for _, v := range raw {
		p := cleanReportPath(stringValue(v))
		if !actualSet[p] {
			return fmt.Errorf("changed_files contains file not changed through agent write tools: %s", stringValue(v))
		}
	}
	return nil
}

func cleanReportPath(path string) string {
	return strings.Trim(strings.TrimSpace(path), "`")
}

func inputArray(v any) []any {
	arr, _ := v.([]any)
	return arr
}

func hasFailedVerification(arr []any) bool {
	for _, v := range arr {
		vm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		passed, _ := vm["passed"].(bool)
		if !passed {
			return true
		}
	}
	return false
}
