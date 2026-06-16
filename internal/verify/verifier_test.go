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

func TestMakeHasTarget(t *testing.T) {
	cases := []struct {
		name   string
		mk     string
		target string
		want   bool
	}{
		{"real target", "test:\n\tgo test ./...\n", "test", true},
		{"target with deps", "test: build\n\tgo test\n", "test", true},
		{"target after other lines", "build:\n\tgo build\ntest:\n\tgo test\n", "test", true},
		{"variable assignment := is not a target", "test := go test ./...\n", "test", false},
		{"variable assignment no space", "test:=go test\n", "test", false},
		{"double-colon assignment ::=", "test ::= x\n", "test", false},
		{"prefix collision testing:", "testing:\n\techo hi\n", "test", false},
		{"absent", "build:\n\tgo build\n", "test", false},
	}
	for _, c := range cases {
		if got := makeHasTarget(c.mk, c.target); got != c.want {
			t.Errorf("%s: makeHasTarget=%v want %v", c.name, got, c.want)
		}
	}
}
