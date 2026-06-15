package verify

import "testing"

func TestTargetedGoTests(t *testing.T) {
	got := targetedGoTests([]string{
		"internal/review/finding.go",
		"internal/review/finding_test.go", // same dir -> dedup
		"internal/fix/report.go",
		"main.go",   // root package -> skipped (full suite covers it)
		"README.md", // non-go -> skipped
	})
	want := []string{"go test ./internal/review/...", "go test ./internal/fix/..."}
	if len(got) != len(want) {
		t.Fatalf("targetedGoTests=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("targetedGoTests[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestSuggestVariadicNoChange(t *testing.T) {
	// Suggest must remain callable with no changedFiles (variadic, backward compatible).
	_ = Suggest(t.TempDir())
}
