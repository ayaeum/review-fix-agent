// Package contextmgr assembles the model context per the doc: system prompt +
// system state + project rule files + scope context gathered around the diff.
// It deliberately collects around the change rather than scanning the whole repo.
package contextmgr

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// Hunk is one @@ block within a changed file.
type Hunk struct {
	Header   string   // the raw @@ ... @@ line
	OldStart int      // 1-based start line in the old file
	NewStart int      // 1-based start line in the new file
	Lines    []string // hunk body lines, prefixed with ' ', '+', or '-'
}

// ChangedFile aggregates the hunks touching one file.
type ChangedFile struct {
	OldPath string
	NewPath string
	Hunks   []Hunk
	Binary  bool
}

// Path returns the most useful path to display for a changed file.
func (c ChangedFile) Path() string {
	if c.NewPath != "" && c.NewPath != "/dev/null" {
		return c.NewPath
	}
	return c.OldPath
}

// AddedLines returns the 1-based new-file line numbers added by this file's hunks.
func (c ChangedFile) AddedLines() []int {
	var out []int
	for _, h := range c.Hunks {
		ln := h.NewStart
		for _, l := range h.Lines {
			switch {
			case strings.HasPrefix(l, "+"):
				out = append(out, ln)
				ln++
			case strings.HasPrefix(l, "-"):
				// removed line: does not advance new-file counter
			default:
				ln++
			}
		}
	}
	return out
}

// ParseUnifiedDiff parses `git diff` output into changed files. It is tolerant:
// unknown lines are skipped, and it never errors on malformed input.
func ParseUnifiedDiff(diff string) []ChangedFile {
	var files []ChangedFile
	var cur *ChangedFile
	var curHunk *Hunk

	flushHunk := func() {
		if cur != nil && curHunk != nil {
			cur.Hunks = append(cur.Hunks, *curHunk)
			curHunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			files = append(files, *cur)
			cur = nil
		}
	}

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			cur = &ChangedFile{}
		case strings.HasPrefix(line, "Binary files "):
			if cur != nil {
				cur.Binary = true
			}
		case strings.HasPrefix(line, "--- "):
			if cur != nil {
				cur.OldPath = stripDiffPath(strings.TrimPrefix(line, "--- "))
			}
		case strings.HasPrefix(line, "+++ "):
			if cur != nil {
				cur.NewPath = stripDiffPath(strings.TrimPrefix(line, "+++ "))
			}
		case strings.HasPrefix(line, "@@"):
			flushHunk()
			h := Hunk{Header: line}
			h.OldStart, h.NewStart = parseHunkHeader(line)
			curHunk = &h
		default:
			if curHunk != nil && (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ")) {
				curHunk.Lines = append(curHunk.Lines, line)
			}
		}
	}
	flushFile()
	return files
}

// stripDiffPath removes the a/ or b/ prefix from a diff path header.
func stripDiffPath(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// parseHunkHeader extracts old/new start lines from "@@ -a,b +c,d @@".
func parseHunkHeader(h string) (oldStart, newStart int) {
	// h looks like: @@ -12,7 +12,9 @@ optional context
	fields := strings.Fields(h)
	for _, f := range fields {
		if strings.HasPrefix(f, "-") {
			oldStart = parseStart(f[1:])
		} else if strings.HasPrefix(f, "+") {
			newStart = parseStart(f[1:])
		}
	}
	return oldStart, newStart
}

func parseStart(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, _ := strconv.Atoi(s)
	return n
}

// RunGitDiff returns the requested review diff. If commit is non-empty it shows
// that commit's patch; else if base is non-empty it diffs against that ref
// (three-dot, i.e. since the merge-base); otherwise it shows uncommitted changes
// (including staged) against HEAD.
func RunGitDiff(ctx context.Context, cwd, base, commit string) (string, error) {
	var args []string
	if commit != "" {
		args = []string{"show", "--format=", "--no-ext-diff", "--no-color", commit, "--"}
	} else if base != "" {
		args = []string{"diff", "--no-color", base + "...", "--"}
	} else {
		args = []string{"diff", "--no-color", "HEAD", "--"}
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), nil
	}
	// Only the HEAD case has a meaningful fallback: a fresh repo without a HEAD
	// commit can still have unstaged changes worth diffing. For an explicit
	// commit or base ref, a failure means the requested ref is bad — surface it
	// rather than silently reviewing a different (working-tree) diff.
	if commit != "" || base != "" {
		return string(out), err
	}
	fb := exec.CommandContext(ctx, "git", "diff", "--no-color")
	fb.Dir = cwd
	if out2, err2 := fb.CombinedOutput(); err2 == nil {
		return string(out2), nil
	}
	return string(out), err
}
