package review

import (
	"strings"
	"testing"
)

func TestParseAndSort(t *testing.T) {
	payload := map[string]any{
		"findings": []any{
			map[string]any{"severity": "low", "file": "b.go", "line": float64(3), "title": "L", "evidence": "e", "impact": "i"},
			map[string]any{"severity": "high", "file": "a.go", "line": float64(9), "title": "H", "evidence": "e", "impact": "i"},
			map[string]any{"severity": "high", "file": "a.go", "line": float64(2), "title": "H2", "evidence": "e", "impact": "i"},
		},
		"reviewed_scope": []any{"a.go", "b.go"},
		"verification":   "not run; review-only mode",
	}
	r, err := ParseReport(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(r.Findings) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(r.Findings))
	}
	c := r.Counts()
	if c["high"] != 2 || c["low"] != 1 {
		t.Errorf("counts = %v, want high:2 low:1", c)
	}

	sorted := r.Sorted()
	// high before low; within high, lower line first.
	if sorted[0].Severity != "high" || sorted[0].Line != 2 {
		t.Errorf("sorted[0] = %+v, want high line 2", sorted[0])
	}
	if sorted[2].Severity != "low" {
		t.Errorf("sorted[2] = %+v, want low last", sorted[2])
	}
}

func TestMarkdownAndJSON(t *testing.T) {
	r := Report{
		Findings: []Finding{
			{Severity: "high", File: "a.go", Line: 1, Title: "Boom", Evidence: "x", Impact: "y", SuggestedFix: "guard"},
		},
		ReviewedScope: []string{"a.go"},
		Verification:  "not run; review-only mode",
	}
	md := r.Markdown()
	for _, want := range []string{"Boom", "a.go:1", "Evidence:", "Impact:", "Suggested fix:", "Reviewed scope"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n%s", want, md)
		}
	}
	js := r.JSON()
	if !strings.Contains(js, "\"severity\": \"high\"") {
		t.Errorf("json missing severity field:\n%s", js)
	}
}
