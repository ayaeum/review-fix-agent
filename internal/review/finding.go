// Package review defines the structured review output and its rendering. The
// schema mirrors the architecture doc: each finding binds to file+line and
// carries evidence, impact, and an optional suggested fix.
package review

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	b.WriteString("# Code Review Report\n\n")
	fmt.Fprintf(&b, "**%d finding(s)** — high: %d, medium: %d, low: %d, info: %d\n\n",
		len(r.Findings), c["high"], c["medium"], c["low"], c["info"])

	if len(r.Findings) == 0 {
		b.WriteString("_No evidence-backed issues found in the reviewed scope._\n\n")
	}
	for i, f := range r.Sorted() {
		fmt.Fprintf(&b, "## %d. [%s] %s\n", i+1, strings.ToUpper(f.Severity), f.Title)
		fmt.Fprintf(&b, "- **Location:** `%s:%d`\n", f.File, f.Line)
		fmt.Fprintf(&b, "- **Evidence:** %s\n", f.Evidence)
		fmt.Fprintf(&b, "- **Impact:** %s\n", f.Impact)
		if strings.TrimSpace(f.SuggestedFix) != "" {
			fmt.Fprintf(&b, "- **Suggested fix:** %s\n", f.SuggestedFix)
		}
		b.WriteString("\n")
	}

	if len(r.ReviewedScope) > 0 {
		b.WriteString("## Reviewed scope\n")
		for _, s := range r.ReviewedScope {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		b.WriteString("\n")
	}
	if len(r.NotReviewed) > 0 {
		b.WriteString("## Not reviewed\n")
		for _, s := range r.NotReviewed {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(r.Verification) != "" {
		fmt.Fprintf(&b, "## Verification\n%s\n", r.Verification)
	}
	return b.String()
}
