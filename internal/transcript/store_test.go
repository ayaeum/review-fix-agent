package transcript

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestAppendAndPath verifies entries are written as one JSON line each and Path
// reports the file location.
func TestAppendAndPath(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, "sess1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Path() != filepath.Join(dir, "sess1.jsonl") {
		t.Errorf("Path()=%q", s.Path())
	}
	s.Append("a", map[string]any{"k": 1})
	s.Append("b", nil)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := readLines(t, s.Path())
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], `"type":"a"`) || !strings.Contains(lines[1], `"type":"b"`) {
		t.Errorf("unexpected line content: %v", lines)
	}
}

// TestAppendAfterCloseIsNoop verifies a write after Close neither panics nor
// adds a line, and that Close is idempotent.
func TestAppendAfterCloseIsNoop(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, "sess2")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Append("first", nil)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil { // idempotent
		t.Errorf("second Close returned %v, want nil", err)
	}
	s.Append("after-close", nil) // must be a silent no-op

	lines := readLines(t, s.Path())
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (post-close append dropped), got %d: %v", len(lines), lines)
	}
}

// TestConcurrentAppend exercises the mutex with many concurrent writers; run
// with -race to catch regressions in the concurrency contract.
func TestConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, "sess3")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Append("ev", map[string]any{"i": i})
		}(i)
	}
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if lines := readLines(t, s.Path()); len(lines) != n {
		t.Fatalf("expected %d lines, got %d", n, len(lines))
	}
}

// TestNilStore verifies nil-receiver methods are safe no-ops.
func TestNilStore(t *testing.T) {
	var s *Store
	if s.Path() != "" {
		t.Errorf("nil Path()=%q", s.Path())
	}
	s.Append("x", nil) // must not panic
	if err := s.Close(); err != nil {
		t.Errorf("nil Close()=%v", err)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			out = append(out, sc.Text())
		}
	}
	return out
}
