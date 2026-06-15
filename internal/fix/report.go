// Package fix defines the structured fix output and its rendering. Verification
// outcomes are a first-class part of the report, per the architecture doc.
package fix

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// Verification is one command run and its outcome. BaselinePassed, when set,
// records whether the same command passed BEFORE the patch — so the report can
// show a FAIL→PASS transition that actually proves the fix worked, rather than a
// command that passed all along and proves nothing.
type Verification struct {
	Command        string `json:"command"`
	Passed         bool   `json:"passed"`
	Summary        string `json:"summary"`
	BaselinePassed *bool  `json:"baseline_passed,omitempty"`
}

// Report is the full Fix Mode output.
type Report struct {
	Summary      string         `json:"summary"`
	PatchScope   string         `json:"patch_scope,omitempty"`
	ChangedFiles []string       `json:"changed_files"`
	Verification []Verification `json:"verification"`
	ResidualRisk string         `json:"residual_risk,omitempty"`
}

// ParseReport converts a report_fix tool payload into a typed Report.
func ParseReport(payload map[string]any) (Report, error) {
	var r Report
	raw, err := json.Marshal(payload)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	return r, nil
}

// AllPassed reports whether every verification command passed.
func (r Report) AllPassed() bool {
	if len(r.Verification) == 0 {
		return false
	}
	for _, v := range r.Verification {
		if !v.Passed {
			return false
		}
	}
	return true
}

// ProvedFix reports whether at least one verification went from failing before
// the patch to passing after it (FAIL→PASS), with no command that passed at
// baseline now failing (regression). This is the SWE-bench-style evidence that
// the patch addressed the issue, not merely that nothing is currently broken.
func (r Report) ProvedFix() bool {
	sawFailToPass := false
	for _, v := range r.Verification {
		if v.BaselinePassed == nil {
			continue
		}
		if *v.BaselinePassed && !v.Passed {
			return false // regression: passed at baseline, fails now
		}
		if !*v.BaselinePassed && v.Passed {
			sawFailToPass = true
		}
	}
	return sawFailToPass
}

// verificationStatus renders PASS/FAIL, or a BEFORE→AFTER transition when a
// baseline was recorded (FAIL→PASS proves the fix; PASS→PASS proves nothing).
func verificationStatus(v Verification) string {
	after := "FAIL"
	if v.Passed {
		after = "PASS"
	}
	if v.BaselinePassed == nil {
		return after
	}
	before := "FAIL"
	if *v.BaselinePassed {
		before = "PASS"
	}
	return before + "→" + after
}

// JSON renders the report as indented JSON.
func (r Report) JSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

// Markdown renders the report for terminal/PR display.
func (r Report) Markdown() string {
	var b strings.Builder
	zh := r.hasChineseText()
	if zh {
		b.WriteString("# 修复报告\n\n")
	} else {
		b.WriteString("# Fix Report\n\n")
	}
	fmt.Fprintf(&b, "%s\n\n", r.Summary)

	if strings.TrimSpace(r.PatchScope) != "" {
		if zh {
			fmt.Fprintf(&b, "**补丁范围:** %s\n\n", r.PatchScope)
		} else {
			fmt.Fprintf(&b, "**Patch scope:** %s\n\n", r.PatchScope)
		}
	}

	if zh {
		b.WriteString("## 变更文件\n")
	} else {
		b.WriteString("## Changed files\n")
	}
	if len(r.ChangedFiles) == 0 {
		if zh {
			b.WriteString("- (无)\n")
		} else {
			b.WriteString("- (none)\n")
		}
	}
	for _, f := range r.ChangedFiles {
		fmt.Fprintf(&b, "- `%s`\n", f)
	}
	b.WriteString("\n")

	if zh {
		b.WriteString("## 验证\n")
	} else {
		b.WriteString("## Verification\n")
	}
	if len(r.Verification) == 0 {
		if zh {
			b.WriteString("- _未运行_\n")
		} else {
			b.WriteString("- _none run_\n")
		}
	}
	for _, v := range r.Verification {
		fmt.Fprintf(&b, "- [%s] `%s` — %s\n", verificationStatus(v), v.Command, v.Summary)
	}
	if len(r.Verification) > 0 && !r.ProvedFix() {
		if zh {
			b.WriteString("> ⚠️ 没有验证命令从失败转为通过——这些命令修复前后都通过，未直接证明问题已修复，请确认根因。\n")
		} else {
			b.WriteString("> ⚠️ No verification went FAIL→PASS — commands passed before and after, so the fix is not directly proven; confirm the root cause.\n")
		}
	}
	b.WriteString("\n")

	if strings.TrimSpace(r.ResidualRisk) != "" {
		if zh {
			fmt.Fprintf(&b, "## 残余风险\n%s\n", r.ResidualRisk)
		} else {
			fmt.Fprintf(&b, "## Residual risk\n%s\n", r.ResidualRisk)
		}
	}
	return b.String()
}

func (r Report) hasChineseText() bool {
	var b strings.Builder
	b.WriteString(r.Summary)
	b.WriteString(r.PatchScope)
	b.WriteString(strings.Join(r.ChangedFiles, ""))
	for _, v := range r.Verification {
		b.WriteString(v.Command)
		b.WriteString(v.Summary)
	}
	b.WriteString(r.ResidualRisk)
	for _, r := range b.String() {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
