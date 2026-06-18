package verify

import (
	"os"
	"path/filepath"
	"testing"
)

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

func writePkg(t *testing.T, dir, json string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestSuggestNodePlainTest(t *testing.T) {
	dir := t.TempDir()
	writePkg(t, dir, `{"scripts":{"test":"jest","lint":"eslint ."}}`)
	got := Suggest(dir)
	if !contains(got, "npm test") {
		t.Errorf("expected 'npm test', got %v", got)
	}
	if !contains(got, "npm run lint") {
		t.Errorf("expected 'npm run lint', got %v", got)
	}
}

func TestSuggestNodeNamespacedTestFallback(t *testing.T) {
	dir := t.TempDir()
	// No plain "test" script — only namespaced ones.
	writePkg(t, dir, `{"scripts":{"test:unit":"jest unit","test:e2e":"playwright"}}`)
	got := Suggest(dir)
	if !contains(got, "npm run test:unit") || !contains(got, "npm run test:e2e") {
		t.Errorf("expected namespaced test scripts, got %v", got)
	}
	if contains(got, "npm test") {
		t.Errorf("must not suggest plain 'npm test' when absent: %v", got)
	}
}

func TestSuggestNodeDependencyNamedTestNotMatched(t *testing.T) {
	dir := t.TempDir()
	// A dependency literally named "test" must NOT be taken as a test script.
	writePkg(t, dir, `{"dependencies":{"test":"^1.0.0"},"scripts":{"build":"tsc"}}`)
	got := Suggest(dir)
	if contains(got, "npm test") {
		t.Errorf("dependency named 'test' wrongly treated as a script: %v", got)
	}
}

func TestSuggestBunUsesRunForTestScript(t *testing.T) {
	dir := t.TempDir()
	writePkg(t, dir, `{"scripts":{"test":"vitest"}}`)
	if err := os.WriteFile(filepath.Join(dir, "bun.lock"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Suggest(dir)
	if !contains(got, "bun run test") {
		t.Errorf("bun should run the test script via 'bun run test', got %v", got)
	}
	if contains(got, "bun test") {
		t.Errorf("'bun test' runs bun's own runner, not the script: %v", got)
	}
}
