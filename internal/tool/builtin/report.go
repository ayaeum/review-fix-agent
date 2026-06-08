package builtin

import (
	"context"
	"fmt"

	"github.com/review-fix-agent/rfa/internal/tool"
)

// ReportFindingsTool is the Review Mode finalizer. The model calls it exactly
// once to emit the structured, evidence-bound findings. Capturing structured
// output via a tool (rather than parsing free text) makes the result reliable.
type ReportFindingsTool struct{}

func (ReportFindingsTool) Name() string { return "report_findings" }

func (ReportFindingsTool) Description() string {
	return "Submit the final structured code-review report. Call this exactly once when the review is complete. " +
		"Every finding must bind to a file and line and include concrete evidence and impact. " +
		"Do not include style nits unless they affect correctness, maintainability, or security."
}

func (ReportFindingsTool) InputSchema() map[string]any {
	finding := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"severity":      map[string]any{"type": "string", "enum": []string{"high", "medium", "low", "info"}},
			"file":          map[string]any{"type": "string"},
			"line":          map[string]any{"type": "integer"},
			"title":         map[string]any{"type": "string"},
			"evidence":      map[string]any{"type": "string", "description": "Concrete proof: the failing path, caller behavior, or value that triggers the issue."},
			"impact":        map[string]any{"type": "string", "description": "The behavior regression or failure this causes."},
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
			"verification":   map[string]any{"type": "string", "description": "What verification was run, or why none (e.g. 'not run; review-only mode')."},
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
	}
	if _, ok := input["reviewed_scope"]; !ok {
		return fmt.Errorf("missing required field \"reviewed_scope\"")
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
	return "Submit the final fix report. Call this exactly once after applying the minimal patch and running verification. " +
		"Explain the patch scope, list changed files, and report verification outcome honestly — including failures."
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
			"summary":       map[string]any{"type": "string", "description": "What was changed and why, tied to the known issue."},
			"patch_scope":   map[string]any{"type": "string", "description": "The boundary of the change; what was intentionally left untouched."},
			"changed_files": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"verification":  map[string]any{"type": "array", "items": verification, "description": "Commands run and their outcomes."},
			"residual_risk": map[string]any{"type": "string", "description": "Anything still risky, unverified, or out of scope."},
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
	if _, ok := input["changed_files"].([]any); !ok {
		return fmt.Errorf("\"changed_files\" must be an array")
	}
	if _, ok := input["verification"].([]any); !ok {
		return fmt.Errorf("\"verification\" must be an array")
	}
	return nil
}

func (ReportFixTool) Call(_ context.Context, input map[string]any, tc *tool.Context) (tool.Result, error) {
	if tc.Sink != nil {
		tc.Sink.SetFix(input)
	}
	return tool.Result{Text: "fix report recorded", Meta: input}, nil
}
