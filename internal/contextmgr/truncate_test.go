package contextmgr

import (
	"strings"
	"testing"
)

// makeFileDiff builds a diff chunk for one file with body repeated to roughly
// size bytes.
func makeFileDiff(path string, size int) string {
	head := "diff --git a/" + path + " b/" + path + "\n--- a/" + path + "\n+++ b/" + path + "\n@@ -1 +1 @@\n"
	line := "+xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n"
	var b strings.Builder
	b.WriteString(head)
	for b.Len() < size {
		b.WriteString(line)
	}
	return b.String()
}

func head(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

func tail(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[len(s)-n:]
}

// TestPrioritizedTruncateSingleLargeFile ensures a single file larger than the
// budget still yields some inline diff content rather than an empty body.
func TestPrioritizedTruncateSingleLargeFile(t *testing.T) {
	budget := 4 * 1024
	diff := makeFileDiff("big.go", budget*3)

	out := prioritizedTruncate(diff, budget)
	if strings.TrimSpace(out) == "" {
		t.Fatal("expected non-empty truncated diff for a single large file")
	}
	if !strings.Contains(out, "diff --git a/big.go") {
		t.Errorf("expected the file header to be included, got start: %q", head(out, 120))
	}
	if !strings.Contains(out, "截断") {
		t.Errorf("expected a truncation notice, got tail: %q", tail(out, 120))
	}
	// The head slice must respect the budget (plus the short notice).
	if len(out) > budget+512 {
		t.Errorf("output exceeds budget: len=%d budget=%d", len(out), budget)
	}
}

// TestPrioritizedTruncateKeepsHighPriorityFirst ensures low-priority files (tests)
// are dropped before high-priority ones when the budget is tight.
func TestPrioritizedTruncateKeepsHighPriorityFirst(t *testing.T) {
	budget := 3 * 1024
	src := makeFileDiff("core.go", budget-400)
	test := makeFileDiff("core_test.go", budget-400)

	out := prioritizedTruncate(src+test, budget)
	if !strings.Contains(out, "core.go") {
		t.Errorf("high-priority core.go should be kept:\n%s", head(out, 200))
	}
	if strings.Contains(out, "core_test.go b/core_test.go") {
		t.Errorf("low-priority test file should have been dropped")
	}
}
