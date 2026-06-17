package contextmgr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunGitDiffIgnoresExternalDriver verifies that a user-configured external
// diff driver does not corrupt the diff: --no-ext-diff must keep git emitting
// parseable unified output.
func TestRunGitDiffIgnoresExternalDriver(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "a@b.c")
	git("config", "user.name", "t")
	// An external diff driver that would replace the patch body with junk.
	git("config", "diff.external", "echo EXTERNAL-DIFF-DRIVER-INVOKED")

	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package a\nvar x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "base")
	if err := os.WriteFile(path, []byte("package a\nvar x = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := RunGitDiff(context.Background(), dir, "", "")
	if err != nil {
		t.Fatalf("RunGitDiff: %v", err)
	}
	if strings.Contains(out, "EXTERNAL-DIFF-DRIVER-INVOKED") {
		t.Errorf("external diff driver was invoked; --no-ext-diff missing:\n%s", out)
	}
	if !strings.Contains(out, "diff --git") {
		t.Errorf("expected a unified diff, got:\n%s", out)
	}
	if files := ParseUnifiedDiff(out); len(files) != 1 || files[0].Path() != "a.go" {
		t.Errorf("diff did not parse to a.go: %+v", files)
	}
}
