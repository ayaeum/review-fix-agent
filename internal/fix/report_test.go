package fix

import (
	"strings"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

// TestProvedFix covers the baseline-aware FAIL->PASS evidence logic that guards
// against a "passed all along" patch being reported as a proven fix.
func TestProvedFix(t *testing.T) {
	cases := []struct {
		name string
		v    []Verification
		want bool
	}{
		{"fail->pass proves the fix", []Verification{{Command: "t", Passed: true, BaselinePassed: boolPtr(false)}}, true},
		{"pass->pass proves nothing", []Verification{{Command: "t", Passed: true, BaselinePassed: boolPtr(true)}}, false},
		{"regression pass->fail", []Verification{{Command: "t", Passed: false, BaselinePassed: boolPtr(true)}}, false},
		{"no baseline recorded", []Verification{{Command: "t", Passed: true}}, false},
		{"fail->pass alongside a pass->pass", []Verification{
			{Command: "a", Passed: true, BaselinePassed: boolPtr(false)},
			{Command: "b", Passed: true, BaselinePassed: boolPtr(true)},
		}, true},
		{"fail->pass but with a regression elsewhere", []Verification{
			{Command: "a", Passed: true, BaselinePassed: boolPtr(false)},
			{Command: "b", Passed: false, BaselinePassed: boolPtr(true)},
		}, false},
	}
	for _, c := range cases {
		r := Report{Verification: c.v}
		if got := r.ProvedFix(); got != c.want {
			t.Errorf("%s: ProvedFix()=%v want %v", c.name, got, c.want)
		}
	}
}

// TestVerificationStatusAndMarkdown checks the BEFORE->AFTER status rendering and
// the "not proven" warning shown when no verification went FAIL->PASS.
func TestVerificationStatusAndMarkdown(t *testing.T) {
	r := Report{
		Summary:      "修复了 Add 的减法 bug",
		ChangedFiles: []string{"add.go"},
		Verification: []Verification{
			{Command: "go test", Passed: true, Summary: "现在通过", BaselinePassed: boolPtr(false)},
		},
	}
	md := r.Markdown()
	if !strings.Contains(md, "FAIL→PASS") {
		t.Errorf("markdown missing FAIL->PASS transition:\n%s", md)
	}
	if strings.Contains(md, "未直接证明") {
		t.Errorf("a proven fix must not show the warning:\n%s", md)
	}

	r2 := Report{
		Summary:      "改动了一段正常代码",
		ChangedFiles: []string{"x.go"},
		Verification: []Verification{
			{Command: "go test", Passed: true, Summary: "一直都通过", BaselinePassed: boolPtr(true)},
		},
	}
	md2 := r2.Markdown()
	if !strings.Contains(md2, "PASS→PASS") {
		t.Errorf("markdown missing PASS->PASS:\n%s", md2)
	}
	if !strings.Contains(md2, "未直接证明") {
		t.Errorf("an unproven fix must show the warning:\n%s", md2)
	}

	r3 := Report{
		Summary:      "做了一处修复",
		ChangedFiles: []string{"x.go"},
		Verification: []Verification{{Command: "c", Passed: true, Summary: "ok"}},
	}
	if !r3.AllPassed() {
		t.Error("AllPassed should be true for a passing verification without baseline")
	}
	md3 := r3.Markdown()
	if !strings.Contains(md3, "[PASS]") {
		t.Errorf("a no-baseline verification should render plain [PASS]:\n%s", md3)
	}
	// With no baseline captured, the warning must NOT claim "passed before and
	// after" (that transition was never measured); it must flag the missing baseline.
	if strings.Contains(md3, "修复前后都通过") {
		t.Errorf("no-baseline report must not claim a before/after observation:\n%s", md3)
	}
	if !strings.Contains(md3, "未记录修复前的基线") {
		t.Errorf("no-baseline report must flag the missing baseline:\n%s", md3)
	}
}

func TestParseReport(t *testing.T) {
	payload := map[string]any{
		"summary":       "flip operator",
		"changed_files": []any{"add.go"},
		"verification": []any{
			map[string]any{"command": "go test", "passed": true, "summary": "ok", "baseline_passed": false},
		},
		"residual_risk": "none",
	}
	r, err := ParseReport(payload)
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if r.Summary != "flip operator" || len(r.ChangedFiles) != 1 || len(r.Verification) != 1 {
		t.Errorf("parsed report = %+v", r)
	}
	if r.Verification[0].BaselinePassed == nil || *r.Verification[0].BaselinePassed {
		t.Errorf("baseline_passed should parse to a non-nil false")
	}
	if !r.ProvedFix() {
		t.Error("FAIL->PASS report should be ProvedFix")
	}
}

func TestAllPassed(t *testing.T) {
	if (Report{}).AllPassed() {
		t.Error("no verification should not be AllPassed")
	}
	pass := Report{Verification: []Verification{{Command: "a", Passed: true}, {Command: "b", Passed: true}}}
	if !pass.AllPassed() {
		t.Error("all-passing should be AllPassed")
	}
	mixed := Report{Verification: []Verification{{Command: "a", Passed: true}, {Command: "b", Passed: false}}}
	if mixed.AllPassed() {
		t.Error("a failing command means not AllPassed")
	}
}

// TestMarkdownEnglish covers the English rendering branch (no Han characters),
// including patch scope, residual risk, and the no-baseline warning.
func TestMarkdownEnglish(t *testing.T) {
	r := Report{
		Summary:      "fixed the off-by-one",
		PatchScope:   "only loop bound",
		ChangedFiles: []string{"loop.go"},
		Verification: []Verification{{Command: "go test", Passed: true, Summary: "passes"}},
		ResidualRisk: "none observed",
	}
	md := r.Markdown()
	for _, want := range []string{"# Fix Report", "Patch scope:", "Changed files", "loop.go", "Verification", "[PASS]", "Residual risk", "No pre-fix baseline"} {
		if !contains(md, want) {
			t.Errorf("English markdown missing %q\n%s", want, md)
		}
	}

	empty := Report{Summary: "x"}
	if !contains(empty.Markdown(), "(none)") {
		t.Errorf("empty changed files should render (none):\n%s", empty.Markdown())
	}
	if !contains(r.JSON(), "\"summary\"") {
		t.Error("JSON should contain summary field")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
