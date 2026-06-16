package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildRFA compiles the CLI once into a temp dir and returns the binary path.
func buildRFA(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	bin := filepath.Join(t.TempDir(), "rfa")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build rfa: %v\n%s", err, out)
	}
	return bin
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func newGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "a@b.c")
	git(t, dir, "config", "user.name", "test")
	return dir
}

// mockEnv returns the test environment: offline mock model, and RFA_TRACE_DIR
// cleared so transcripts land under <cwd>/.rfa/sessions (the documented default).
func mockEnv() []string {
	env := append([]string{}, os.Environ()...)
	out := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, "RFA_TRACE_DIR=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, "RFA_MOCK=1")
}

// TestReviewMockEndToEnd exercises the full CLI wiring offline: flag parsing,
// scope/diff assembly, the agentic loop driven by the mock model, report
// printing, and transcript writing to <cwd>/.rfa/sessions.
func TestReviewMockEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary; skipped in -short")
	}
	bin := buildRFA(t)
	repo := newGitRepo(t)

	mustWrite(t, filepath.Join(repo, "a.go"), "package main\nfunc main(){}\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "base")
	// Introduce an uncommitted change for review to pick up.
	mustWrite(t, filepath.Join(repo, "a.go"), "package main\nfunc main(){ _ = []int{1}[3] }\n")

	cmd := exec.Command(bin, "review", "--max-turns", "4")
	cmd.Dir = repo
	cmd.Env = mockEnv()
	out, _ := cmd.CombinedOutput() // exit code may be non-zero (mock emits no report)

	got := string(out)
	// The mock drives the loop to completion; with no finalizer it prints the
	// "no review report" branch. Either way the report stage must be reached.
	if !strings.Contains(got, "review report") {
		t.Errorf("expected review report output, got:\n%s", got)
	}

	// Transcript must be written under the repo's .rfa/sessions (round-1 fix:
	// default is <cwd>/.rfa/sessions, not a hardcoded absolute path).
	matches, _ := filepath.Glob(filepath.Join(repo, ".rfa", "sessions", "*.jsonl"))
	if len(matches) == 0 {
		t.Errorf("expected a transcript under %s/.rfa/sessions, found none\noutput:\n%s", repo, got)
	}
}

// TestUnknownCommandExits verifies an unknown subcommand fails with a usage error.
func TestUnknownCommandExits(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary; skipped in -short")
	}
	bin := buildRFA(t)
	cmd := exec.Command(bin, "frobnicate")
	cmd.Env = mockEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("unknown command should exit non-zero")
	}
	if !strings.Contains(string(out), "unknown command") {
		t.Errorf("expected 'unknown command' message, got:\n%s", out)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
