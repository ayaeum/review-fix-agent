package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFileTool(t *testing.T, content string, input map[string]any) string {
	t.Helper()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "f.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"path": "f.txt"}
	for k, v := range input {
		in[k] = v
	}
	res, err := ReadTool{}.Call(context.Background(), in, newReadCtx(cwd))
	if err != nil {
		t.Fatal(err)
	}
	return res.Text
}

// TestReadTrailingNewlineNoPhantomLine: a file ending in "\n" must not report an
// extra blank line or an inflated total count.
func TestReadTrailingNewlineNoPhantomLine(t *testing.T) {
	out := readFileTool(t, "a\nb\nc\n", nil)
	if !strings.Contains(out, "(3 lines total)") {
		t.Errorf("expected 3 lines total, got:\n%s", out)
	}
	if strings.Contains(out, "     4\t") {
		t.Errorf("phantom line 4 present:\n%s", out)
	}
}

// TestReadNoTrailingNewline: a file without a final newline keeps its last line.
func TestReadNoTrailingNewline(t *testing.T) {
	out := readFileTool(t, "a\nb", nil)
	if !strings.Contains(out, "(2 lines total)") {
		t.Errorf("expected 2 lines total, got:\n%s", out)
	}
	if !strings.Contains(out, "     2\tb") {
		t.Errorf("last line missing:\n%s", out)
	}
}

// TestReadBlankLineBeforeEOF preserves a genuine empty line that precedes the
// trailing newline.
func TestReadBlankLineBeforeEOF(t *testing.T) {
	out := readFileTool(t, "a\n\n", nil) // line1="a", line2="" then trailing \n
	if !strings.Contains(out, "(2 lines total)") {
		t.Errorf("expected 2 lines total, got:\n%s", out)
	}
}
