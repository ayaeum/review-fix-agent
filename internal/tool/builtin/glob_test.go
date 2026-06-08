package builtin

import "testing"

func TestGlobToRegexp(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		match   bool
	}{
		{"**/*.go", "c.go", true},
		{"**/*.go", "a/b/c.go", true},
		{"**/*.go", "a/b/c.txt", false},
		{"*.go", "main.go", true},
		{"*.go", "pkg/main.go", false},
		{"internal/**/*_test.go", "internal/a/b_test.go", true},
		{"internal/**/*_test.go", "internal/a/b.go", false},
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},
		{"cmd/rfa/main.go", "cmd/rfa/main.go", true},
	}
	for _, c := range cases {
		re, err := globToRegexp(c.pattern)
		if err != nil {
			t.Fatalf("globToRegexp(%q) error: %v", c.pattern, err)
		}
		if got := re.MatchString(c.path); got != c.match {
			t.Errorf("glob %q vs %q = %v, want %v (re=%s)", c.pattern, c.path, got, c.match, re.String())
		}
	}
}
