package review

import (
	"strings"

	"github.com/review-fix-agent/rfa/internal/contextmgr"
)

// ResolveLineNumbers adjusts Finding.Line values by matching each finding's
// evidence text against the diff hunks for the corresponding file. This
// corrects LLM-reported line numbers that are close but off by a few lines.
//
// Strategy (borrowed from open-code-review):
//  1. Try matching evidence lines in the new-side of diff hunks.
//  2. Fall back to old-side of diff hunks.
//  3. If no match, keep the original LLM-reported line number.
func ResolveLineNumbers(findings []Finding, changed []contextmgr.ChangedFile) []Finding {
	if len(findings) == 0 || len(changed) == 0 {
		return findings
	}

	byPath := make(map[string]*contextmgr.ChangedFile, len(changed))
	for i := range changed {
		c := &changed[i]
		if p := c.Path(); p != "" {
			byPath[p] = c
		}
	}

	out := make([]Finding, len(findings))
	copy(out, findings)

	for i := range out {
		f := &out[i]
		if f.File == "" || f.Evidence == "" {
			continue
		}
		cf, ok := byPath[f.File]
		if !ok {
			continue
		}
		if resolved, ok := resolveFromHunks(cf.Hunks, f.Evidence, true); ok {
			f.Line = resolved
			continue
		}
		if resolved, ok := resolveFromHunks(cf.Hunks, f.Evidence, false); ok {
			f.Line = resolved
		}
	}
	return out
}

// resolveFromHunks tries to match evidence text against hunk lines.
// When newSide is true, matches against context+added lines (new-file numbers).
// When false, matches against context+deleted lines (old-file numbers).
func resolveFromHunks(hunks []contextmgr.Hunk, evidence string, newSide bool) (int, bool) {
	targets := splitAndNormalize(evidence)
	if len(targets) == 0 {
		return 0, false
	}

	for _, h := range hunks {
		lines := extractSideLines(h, newSide)
		if start, ok := matchConsecutive(lines, targets); ok {
			return start, true
		}
	}
	return 0, false
}

type indexedLine struct {
	lineNum int
	content string
}

// extractSideLines extracts one side of the diff from a hunk with line numbers.
func extractSideLines(h contextmgr.Hunk, newSide bool) []indexedLine {
	var result []indexedLine
	oldLine := h.OldStart
	newLine := h.NewStart

	for _, l := range h.Lines {
		switch {
		case strings.HasPrefix(l, "+"):
			if newSide {
				result = append(result, indexedLine{newLine, normalizeLine(l[1:])})
			}
			newLine++
		case strings.HasPrefix(l, "-"):
			if !newSide {
				result = append(result, indexedLine{oldLine, normalizeLine(l[1:])})
			}
			oldLine++
		default: // context line (space prefix)
			content := l
			if len(content) > 0 && content[0] == ' ' {
				content = content[1:]
			}
			if newSide {
				result = append(result, indexedLine{newLine, normalizeLine(content)})
			} else {
				result = append(result, indexedLine{oldLine, normalizeLine(content)})
			}
			oldLine++
			newLine++
		}
	}
	return result
}

// matchConsecutive finds the first consecutive run of targets in sideLines.
func matchConsecutive(sideLines []indexedLine, targets []string) (int, bool) {
	if len(targets) == 0 || len(sideLines) < len(targets) {
		return 0, false
	}
	for i := 0; i <= len(sideLines)-len(targets); i++ {
		matched := true
		for j, target := range targets {
			if sideLines[i+j].content != target {
				matched = false
				break
			}
		}
		if matched {
			return sideLines[i].lineNum, true
		}
	}
	return 0, false
}

// splitAndNormalize splits text into non-empty normalized lines.
func splitAndNormalize(text string) []string {
	raw := strings.Split(text, "\n")
	var result []string
	for _, line := range raw {
		n := normalizeLine(line)
		if n == "" {
			continue
		}
		result = append(result, n)
	}
	if len(result) > 10 {
		result = result[:10]
	}
	return result
}

// normalizeLine strips whitespace and diff markers for comparison.
func normalizeLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "+")
	s = strings.TrimPrefix(s, "-")
	return strings.TrimSpace(s)
}
