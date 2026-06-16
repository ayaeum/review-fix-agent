package contextmgr

import "testing"

const twoFileDiff = `diff --git a/foo.go b/foo.go
index 111..222 100644
--- a/foo.go
+++ b/foo.go
@@ -1,2 +1,3 @@
 package foo
+var x = 1
diff --git a/bar.go b/bar.go
--- a/bar.go
+++ b/bar.go
@@ -1 +1,2 @@
 package bar
+var y = 2
`

func TestSplitDiffByFile(t *testing.T) {
	chunks := SplitDiffByFile(twoFileDiff)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %q", len(chunks), chunks)
	}
	if got := DiffChunkPath(chunks[0]); got != "foo.go" {
		t.Errorf("chunk[0] path = %q, want foo.go", got)
	}
	if got := DiffChunkPath(chunks[1]); got != "bar.go" {
		t.Errorf("chunk[1] path = %q, want bar.go", got)
	}
}

func TestSplitDiffByFileEmpty(t *testing.T) {
	if chunks := SplitDiffByFile(""); len(chunks) != 0 {
		t.Errorf("empty diff should yield no chunks, got %d", len(chunks))
	}
}

func TestSplitDiffByFilePreamble(t *testing.T) {
	// Leading text before the first "diff --git" becomes its own chunk with no path.
	d := "warning: something\n" + twoFileDiff
	chunks := SplitDiffByFile(d)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks (preamble + 2 files), got %d", len(chunks))
	}
	if got := DiffChunkPath(chunks[0]); got != "" {
		t.Errorf("preamble chunk path = %q, want empty", got)
	}
}

func TestDiffChunkPath(t *testing.T) {
	if got := DiffChunkPath("+++ b/internal/x.go\n"); got != "internal/x.go" {
		t.Errorf("b/ strip = %q", got)
	}
	if got := DiffChunkPath("+++ /dev/null\n"); got != "" {
		t.Errorf("/dev/null should yield empty, got %q", got)
	}
	if got := DiffChunkPath("no header here\n"); got != "" {
		t.Errorf("no +++ should yield empty, got %q", got)
	}
}
