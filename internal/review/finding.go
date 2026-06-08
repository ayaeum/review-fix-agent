// Package review defines the structured review output and its rendering. The
// schema mirrors the architecture doc: each finding binds to file+line and
// carries evidence, impact, and an optional suggested fix.
package review

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Finding is a single evidence-bound review result.
type Finding struct {
	Severity     string `json:"severity"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	Title        string `json:"title"`
	Evidence     string `json:"evidence"`
	Impact       string `json:"impact"`
	SuggestedFix string `json:"suggested_fix,omitempty"`
}

// Report is the full Review Mode output.
type Report struct {
	Findings      []Finding `json:"findings"`
	ReviewedScope []string  `json:"reviewed_scope"`
	NotReviewed   []string  `json:"not_reviewed,omitempty"`
	Verification  string    `json:"verification,omitempty"`
}

// ParseReport converts a report_findings tool payload into a typed Report by
// round-tripping through JSON.
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

var severityRank = map[string]int{"high": 0, "medium": 1, "low": 2, "info": 3}

// Sorted returns findings ordered by severity (high first) then file/line.
func (r Report) Sorted() []Finding {
	out := make([]Finding, len(r.Findings))
	copy(out, r.Findings)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := severityRank[out[i].Severity], severityRank[out[j].Severity]
		if ri != rj {
			return ri < rj
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// Counts returns the number of findings per severity.
func (r Report) Counts() map[string]int {
	c := map[string]int{}
	for _, f := range r.Findings {
		c[f.Severity]++
	}
	return c
}

// JSON renders the report as indented JSON.
func (r Report) JSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

// Markdown renders the report for terminal/PR display.
func (r Report) Markdown() string {
	var b strings.Builder
	c := r.Counts()
	zh := r.hasChineseText()
	if zh {
		b.WriteString("# 代码审查报告\n\n")
		fmt.Fprintf(&b, "**%d 个问题** - high: %d, medium: %d, low: %d, info: %d\n\n",
			len(r.Findings), c["high"], c["medium"], c["low"], c["info"])
	} else {
		b.WriteString("# Code Review Report\n\n")
		fmt.Fprintf(&b, "**%d finding(s)** — high: %d, medium: %d, low: %d, info: %d\n\n",
			len(r.Findings), c["high"], c["medium"], c["low"], c["info"])
	}

	if len(r.Findings) == 0 {
		if zh {
			b.WriteString("_在已审查范围内未发现有证据支撑的问题。_\n\n")
		} else {
			b.WriteString("_No evidence-backed issues found in the reviewed scope._\n\n")
		}
	}
	for i, f := range r.Sorted() {
		fmt.Fprintf(&b, "## %d. [%s] %s\n", i+1, strings.ToUpper(f.Severity), f.Title)
		if zh {
			fmt.Fprintf(&b, "- **位置:** `%s:%d`\n", f.File, f.Line)
			fmt.Fprintf(&b, "- **证据:** %s\n", f.Evidence)
			fmt.Fprintf(&b, "- **影响:** %s\n", f.Impact)
		} else {
			fmt.Fprintf(&b, "- **Location:** `%s:%d`\n", f.File, f.Line)
			fmt.Fprintf(&b, "- **Evidence:** %s\n", f.Evidence)
			fmt.Fprintf(&b, "- **Impact:** %s\n", f.Impact)
		}
		if strings.TrimSpace(f.SuggestedFix) != "" {
			if zh {
				fmt.Fprintf(&b, "- **建议修复:** %s\n", f.SuggestedFix)
			} else {
				fmt.Fprintf(&b, "- **Suggested fix:** %s\n", f.SuggestedFix)
			}
		}
		b.WriteString("\n")
	}

	if len(r.ReviewedScope) > 0 {
		if zh {
			b.WriteString("## 已审查范围\n")
		} else {
			b.WriteString("## Reviewed scope\n")
		}
		for _, s := range r.ReviewedScope {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		b.WriteString("\n")
	}
	if len(r.NotReviewed) > 0 {
		if zh {
			b.WriteString("## 未审查范围\n")
		} else {
			b.WriteString("## Not reviewed\n")
		}
		for _, s := range r.NotReviewed {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(r.Verification) != "" {
		if zh {
			fmt.Fprintf(&b, "## 验证\n%s\n", r.Verification)
		} else {
			fmt.Fprintf(&b, "## Verification\n%s\n", r.Verification)
		}
	}
	return b.String()
}

func (r Report) hasChineseText() bool {
	var b strings.Builder
	for _, f := range r.Findings {
		b.WriteString(f.Title)
		b.WriteString(f.Evidence)
		b.WriteString(f.Impact)
		b.WriteString(f.SuggestedFix)
	}
	b.WriteString(strings.Join(r.ReviewedScope, ""))
	b.WriteString(strings.Join(r.NotReviewed, ""))
	b.WriteString(r.Verification)
	for _, r := range b.String() {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
