package contextmgr

import "testing"

const sampleDiff = `diff --git a/internal/svc/server.go b/internal/svc/server.go
index 1111111..2222222 100644
--- a/internal/svc/server.go
+++ b/internal/svc/server.go
@@ -10,6 +10,9 @@ func Start(cfg *Config) error {
 	listener := net.Listen()
 	defer listener.Close()
+	if cfg == nil {
+		return errors.New("nil config")
+	}
 	return serve(listener, cfg.Port)
diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,2 +1,2 @@
-old title
+new title
 body
`

func TestParseUnifiedDiff(t *testing.T) {
	files := ParseUnifiedDiff(sampleDiff)
	if len(files) != 2 {
		t.Fatalf("expected 2 changed files, got %d", len(files))
	}

	if got := files[0].Path(); got != "internal/svc/server.go" {
		t.Errorf("file[0] path = %q, want internal/svc/server.go", got)
	}
	if len(files[0].Hunks) != 1 {
		t.Fatalf("file[0] expected 1 hunk, got %d", len(files[0].Hunks))
	}
	h := files[0].Hunks[0]
	if h.OldStart != 10 || h.NewStart != 10 {
		t.Errorf("hunk starts = old %d new %d, want 10/10", h.OldStart, h.NewStart)
	}

	// The added guard occupies new-file lines 12,13,14.
	added := files[0].AddedLines()
	if len(added) != 3 {
		t.Fatalf("expected 3 added lines, got %d (%v)", len(added), added)
	}
	if added[0] != 12 || added[2] != 14 {
		t.Errorf("added line numbers = %v, want [12 13 14]", added)
	}

	if got := files[1].Path(); got != "README.md" {
		t.Errorf("file[1] path = %q, want README.md", got)
	}
}

func TestParseUnifiedDiffEmpty(t *testing.T) {
	if files := ParseUnifiedDiff(""); len(files) != 0 {
		t.Errorf("empty diff should yield no files, got %d", len(files))
	}
}

func TestParseHunkHeader(t *testing.T) {
	old, neu := parseHunkHeader("@@ -42,7 +50,9 @@ func X() {")
	if old != 42 || neu != 50 {
		t.Errorf("parseHunkHeader = %d/%d, want 42/50", old, neu)
	}
	old, neu = parseHunkHeader("@@ -1 +1 @@")
	if old != 1 || neu != 1 {
		t.Errorf("single-line hunk = %d/%d, want 1/1", old, neu)
	}
}
