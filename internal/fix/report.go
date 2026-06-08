// Package fix defines the structured fix output and its rendering. Verification
// outcomes are a first-class part of the report, per the architecture doc.
package fix

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Verification is one command run and its outcome.
type Verification struct {
	Command string `json:"command"`
	Passed  bool   `json:"passed"`
	Summary string `json:"summary"`
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

// JSON renders the report as indented JSON.
func (r Report) JSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

// Markdown renders the report for terminal/PR display.
func (r Report) Markdown() string {
	var b strings.Builder
	b.WriteString("# Fix Report\n\n")
	fmt.Fprintf(&b, "%s\n\n", r.Summary)

	if strings.TrimSpace(r.PatchScope) != "" {
		fmt.Fprintf(&b, "**Patch scope:** %s\n\n", r.PatchScope)
	}

	b.WriteString("## Changed files\n")
	if len(r.ChangedFiles) == 0 {
		b.WriteString("- (none)\n")
	}
	for _, f := range r.ChangedFiles {
		fmt.Fprintf(&b, "- `%s`\n", f)
	}
	b.WriteString("\n")

	b.WriteString("## Verification\n")
	if len(r.Verification) == 0 {
		b.WriteString("- _none run_\n")
	}
	for _, v := range r.Verification {
		status := "FAIL"
		if v.Passed {
			status = "PASS"
		}
		fmt.Fprintf(&b, "- [%s] `%s` — %s\n", status, v.Command, v.Summary)
	}
	b.WriteString("\n")

	if strings.TrimSpace(r.ResidualRisk) != "" {
		fmt.Fprintf(&b, "## Residual risk\n%s\n", r.ResidualRisk)
	}
	return b.String()
}
