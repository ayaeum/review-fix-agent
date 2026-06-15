// Package verify detects which verification commands already exist in a project
// so the agent can choose existing ones rather than inventing new tooling. The
// agent runs the commands itself via the shell tool; this only suggests.
package verify

import (
	"os"
	"path/filepath"
	"strings"
)

// Suggest returns verification commands that appear to be available in the
// project at cwd, ordered cheap-to-expensive (typecheck/lint before tests).
// When changedFiles are provided, package-scoped targeted commands are suggested
// first: they run faster and isolate the fix's effect from unrelated pre-existing
// failures in the full suite.
func Suggest(cwd string, changedFiles ...string) []string {
	var out []string
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(cwd, rel))
		return err == nil
	}

	switch {
	case exists("go.mod"):
		out = append(out, targetedGoTests(changedFiles)...)
		out = append(out, "go build ./...", "go vet ./...", "go test ./...")
	case exists("Cargo.toml"):
		out = append(out, "cargo check", "cargo clippy", "cargo test")
	case exists("package.json"):
		runner := nodeRunner(cwd)
		if hasNodeScript(cwd, "typecheck") {
			out = append(out, runner+" run typecheck")
		} else if exists("tsconfig.json") {
			out = append(out, "npx tsc --noEmit")
		}
		if hasNodeScript(cwd, "lint") {
			out = append(out, runner+" run lint")
		}
		if hasNodeScript(cwd, "test") {
			out = append(out, runner+" test")
		}
	case exists("pyproject.toml") || exists("setup.py") || exists("pytest.ini") || exists("tox.ini"):
		out = append(out, "pytest -q")
	}

	if exists("Makefile") {
		mk := readFile(filepath.Join(cwd, "Makefile"))
		for _, target := range []string{"test", "lint", "check", "vet"} {
			if strings.Contains(mk, "\n"+target+":") || strings.HasPrefix(mk, target+":") {
				out = append(out, "make "+target)
			}
		}
	}
	return dedupe(out)
}

// targetedGoTests derives `go test ./<dir>/...` for each directory containing a
// changed .go file, so the agent can run the affected packages before the full
// suite. Root-package files are skipped since `go test ./...` already covers them.
func targetedGoTests(changedFiles []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range changedFiles {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		dir := filepath.Dir(f)
		if dir == "." || dir == "" {
			continue
		}
		if seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, "go test ./"+dir+"/...")
	}
	return out
}

func nodeRunner(cwd string) string {
	switch {
	case fileExists(filepath.Join(cwd, "bun.lockb")), fileExists(filepath.Join(cwd, "bun.lock")):
		return "bun"
	case fileExists(filepath.Join(cwd, "pnpm-lock.yaml")):
		return "pnpm"
	case fileExists(filepath.Join(cwd, "yarn.lock")):
		return "yarn"
	default:
		return "npm"
	}
}

func hasNodeScript(cwd, name string) bool {
	pkg := readFile(filepath.Join(cwd, "package.json"))
	return strings.Contains(pkg, "\""+name+"\"")
}

func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
