package builtin

import (
	"context"
	"testing"

	"github.com/review-fix-agent/rfa/internal/tool"
)

func validFindings() map[string]any {
	return map[string]any{
		"findings": []any{
			map[string]any{"severity": "high", "file": "a.go", "line": float64(3), "title": "t", "evidence": "e", "impact": "i"},
		},
		"reviewed_scope": []any{"a.go"},
	}
}

func finding0(m map[string]any) map[string]any {
	return m["findings"].([]any)[0].(map[string]any)
}

func TestReportFindingsValidate(t *testing.T) {
	rf := ReportFindingsTool{}
	if err := rf.Validate(validFindings()); err != nil {
		t.Fatalf("valid findings rejected: %v", err)
	}
	// Empty findings array is valid (a clean review).
	if err := rf.Validate(map[string]any{"findings": []any{}, "reviewed_scope": []any{"a.go"}}); err != nil {
		t.Errorf("empty findings should be valid: %v", err)
	}

	cases := map[string]func(m map[string]any){
		"missing findings":   func(m map[string]any) { delete(m, "findings") },
		"findings not array": func(m map[string]any) { m["findings"] = "x" },
		"missing impact":     func(m map[string]any) { delete(finding0(m), "impact") },
		"bad severity":       func(m map[string]any) { finding0(m)["severity"] = "critical" },
		"line below 1":       func(m map[string]any) { finding0(m)["line"] = float64(0) },
		"empty evidence":     func(m map[string]any) { finding0(m)["evidence"] = "  " },
		"missing scope":      func(m map[string]any) { delete(m, "reviewed_scope") },
		"empty scope":        func(m map[string]any) { m["reviewed_scope"] = []any{} },
		"blank verification": func(m map[string]any) { m["verification"] = "  " },
		"finding not object": func(m map[string]any) { m["findings"] = []any{"x"} },
	}
	for name, mutate := range cases {
		in := validFindings()
		mutate(in)
		if err := rf.Validate(in); err == nil {
			t.Errorf("%s: expected validation error, got nil", name)
		}
	}
}

func validFix() map[string]any {
	return map[string]any{
		"summary":       "fixed it",
		"changed_files": []any{"a.go"},
		"verification":  []any{map[string]any{"command": "go test", "passed": true, "summary": "ok"}},
	}
}

func verif0(m map[string]any) map[string]any {
	return m["verification"].([]any)[0].(map[string]any)
}

func TestReportFixValidate(t *testing.T) {
	rf := ReportFixTool{}
	if err := rf.Validate(validFix()); err != nil {
		t.Fatalf("valid fix rejected: %v", err)
	}

	cases := map[string]func(m map[string]any){
		"missing summary":         func(m map[string]any) { delete(m, "summary") },
		"empty summary":           func(m map[string]any) { m["summary"] = "  " },
		"verification not array":  func(m map[string]any) { m["verification"] = "x" },
		"verif missing summary":   func(m map[string]any) { delete(verif0(m), "summary") },
		"verif passed not bool":   func(m map[string]any) { verif0(m)["passed"] = "yes" },
		"verif empty command":     func(m map[string]any) { verif0(m)["command"] = " " },
		"changed_files not array": func(m map[string]any) { m["changed_files"] = "x" },
		"blank patch_scope":       func(m map[string]any) { m["patch_scope"] = " " },
	}
	for name, mutate := range cases {
		in := validFix()
		mutate(in)
		if err := rf.Validate(in); err == nil {
			t.Errorf("%s: expected validation error, got nil", name)
		}
	}

	// A failed verification requires residual_risk to explain it.
	bad := validFix()
	verif0(bad)["passed"] = false
	verif0(bad)["summary"] = "still failing"
	if err := rf.Validate(bad); err == nil {
		t.Error("failed verification without residual_risk should fail")
	}
	bad["residual_risk"] = "known limitation"
	if err := rf.Validate(bad); err != nil {
		t.Errorf("failed verification with residual_risk should pass: %v", err)
	}
}

// TestReportFixCallVerifiesCommands covers the Call-level check that reported
// verification commands were actually executed via run_command.
func TestReportFixCallVerifiesCommands(t *testing.T) {
	tc := &tool.Context{Sink: tool.NewSink()}
	if _, err := (ReportFixTool{}).Call(context.Background(), validFix(), tc); err == nil {
		t.Error("reporting a command never run via run_command should error")
	}
	// After the command is actually recorded, the report is accepted.
	tc.Sink.RecordCommand(tool.CommandRecord{Command: "go test", Passed: true})
	if _, err := (ReportFixTool{}).Call(context.Background(), validFix(), tc); err != nil {
		t.Errorf("matching recorded command should pass: %v", err)
	}
}

// TestReportFixCallChecksChangedFiles covers the Call-level check that every
// file the agent actually changed appears in changed_files.
func TestReportFixCallChecksChangedFiles(t *testing.T) {
	tc := &tool.Context{Sink: tool.NewSink()}
	tc.Sink.RecordCommand(tool.CommandRecord{Command: "go test", Passed: true})
	tc.Sink.RecordChangedFile("b.go") // agent changed b.go ...
	in := validFix()                  // ... but changed_files only lists a.go
	if _, err := (ReportFixTool{}).Call(context.Background(), in, tc); err == nil {
		t.Error("changed_files omitting an actually-changed file should error")
	}
}
