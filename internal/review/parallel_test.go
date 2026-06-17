package review

import (
	"testing"

	"github.com/review-fix-agent/rfa/internal/contextmgr"
)

func TestShouldParallelReview(t *testing.T) {
	few := make([]contextmgr.ChangedFile, 3)
	for i := range few {
		few[i] = contextmgr.ChangedFile{NewPath: "file.go"}
	}
	if ShouldParallelReview(few) {
		t.Error("expected false for <5 files")
	}

	many := make([]contextmgr.ChangedFile, 6)
	for i := range many {
		many[i] = contextmgr.ChangedFile{NewPath: "file.go"}
	}
	if !ShouldParallelReview(many) {
		t.Error("expected true for >=5 files")
	}

	binary := make([]contextmgr.ChangedFile, 6)
	for i := range binary {
		binary[i] = contextmgr.ChangedFile{NewPath: "file.png", Binary: true}
	}
	if ShouldParallelReview(binary) {
		t.Error("expected false for binary-only files")
	}
}

func TestBuildDiffByFile(t *testing.T) {
	diff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package foo
+var x = 1
diff --git a/bar.go b/bar.go
--- a/bar.go
+++ b/bar.go
@@ -1,2 +1,3 @@
 package bar
+var y = 2
`
	changed := []contextmgr.ChangedFile{
		{OldPath: "foo.go", NewPath: "foo.go"},
		{OldPath: "bar.go", NewPath: "bar.go"},
	}
	result := BuildDiffByFile(changed, diff)
	if len(result) != 2 {
		t.Fatalf("expected 2 files, got %d", len(result))
	}
	if _, ok := result["foo.go"]; !ok {
		t.Error("missing foo.go")
	}
	if _, ok := result["bar.go"]; !ok {
		t.Error("missing bar.go")
	}
}

func TestParseFileFindings(t *testing.T) {
	json := `[{"severity":"high","file":"foo.go","line":10,"title":"bug","evidence":"x","impact":"y"}]`
	findings := parseFileFindings(json, "foo.go")
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Severity != "high" {
		t.Errorf("expected high, got %s", findings[0].Severity)
	}
}

func TestParseFileFindingsEmpty(t *testing.T) {
	findings := parseFileFindings("[]", "foo.go")
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings, got %d", len(findings))
	}
}

func TestParseFileFindingsInvalid(t *testing.T) {
	findings := parseFileFindings("not json", "foo.go")
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for invalid json, got %d", len(findings))
	}
}

func TestParseFileFindingsDefaultsFile(t *testing.T) {
	json := `[{"severity":"medium","line":5,"title":"t","evidence":"e","impact":"i"}]`
	findings := parseFileFindings(json, "default.go")
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].File != "default.go" {
		t.Errorf("expected default.go, got %s", findings[0].File)
	}
}

func TestParseFileFindingsProseWrapped(t *testing.T) {
	// Model wrapped the JSON array in prose; findings must still be recovered.
	text := "Sure! Here are the findings I found:\n" +
		`[{"severity":"high","file":"x.go","line":3,"title":"bug","evidence":"e","impact":"i"}]` +
		"\nLet me know if you need more."
	findings := parseFileFindings(text, "x.go")
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding from prose-wrapped JSON, got %d", len(findings))
	}
	if findings[0].Severity != "high" || findings[0].Title != "bug" {
		t.Errorf("finding = %+v", findings[0])
	}
}

func TestExtractJSONArray(t *testing.T) {
	if got := extractJSONArray("pre [1,2] post"); got != "[1,2]" {
		t.Errorf("got %q", got)
	}
	if got := extractJSONArray("no brackets"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := extractJSONArray("]backwards["); got != "" {
		t.Errorf("expected empty for reversed brackets, got %q", got)
	}
}
