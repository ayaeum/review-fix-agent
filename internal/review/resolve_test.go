package review

import (
	"testing"

	"github.com/review-fix-agent/rfa/internal/contextmgr"
)

func TestResolveLineNumbers_MatchInNewSide(t *testing.T) {
	changed := []contextmgr.ChangedFile{{
		NewPath: "main.go",
		Hunks: []contextmgr.Hunk{{
			OldStart: 10,
			NewStart: 10,
			Lines: []string{
				" func foo() {",
				"-\treturn nil",
				"+\treturn fmt.Errorf(\"bad\")",
				" }",
			},
		}},
	}}
	findings := []Finding{{
		File:     "main.go",
		Line:     99, // wrong line from LLM
		Evidence: "return fmt.Errorf(\"bad\")",
	}}
	resolved := ResolveLineNumbers(findings, changed)
	if resolved[0].Line != 11 {
		t.Fatalf("expected line 11, got %d", resolved[0].Line)
	}
}

func TestResolveLineNumbers_NoMatch_KeepsOriginal(t *testing.T) {
	changed := []contextmgr.ChangedFile{{
		NewPath: "main.go",
		Hunks: []contextmgr.Hunk{{
			OldStart: 1,
			NewStart: 1,
			Lines:    []string{"+\tnewline"},
		}},
	}}
	findings := []Finding{{
		File:     "main.go",
		Line:     42,
		Evidence: "something completely different",
	}}
	resolved := ResolveLineNumbers(findings, changed)
	if resolved[0].Line != 42 {
		t.Fatalf("expected original line 42, got %d", resolved[0].Line)
	}
}

func TestResolveLineNumbers_EmptyInputs(t *testing.T) {
	if got := ResolveLineNumbers(nil, nil); got != nil {
		t.Fatal("expected nil for nil inputs")
	}
	findings := []Finding{{File: "a.go", Line: 1}}
	got := ResolveLineNumbers(findings, nil)
	if len(got) != 1 || got[0].Line != 1 {
		t.Fatal("expected unchanged findings for nil changed")
	}
}

func TestSplitAndNormalize(t *testing.T) {
	got := splitAndNormalize("  + foo  \n  - bar  \n\n  baz  ")
	if len(got) != 3 || got[0] != "foo" || got[1] != "bar" || got[2] != "baz" {
		t.Fatalf("unexpected: %v", got)
	}
}

func TestSplitAndNormalize_Limit(t *testing.T) {
	long := ""
	for i := 0; i < 20; i++ {
		long += "line\n"
	}
	got := splitAndNormalize(long)
	if len(got) != 10 {
		t.Fatalf("expected max 10 lines, got %d", len(got))
	}
}
