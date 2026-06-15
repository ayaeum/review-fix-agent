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
	if !strings.Contains(r3.Markdown(), "[PASS]") {
		t.Errorf("a no-baseline verification should render plain [PASS]:\n%s", r3.Markdown())
	}
}
