// Package verify detects which verification commands already exist in a project
// so the agent can choose existing ones rather than inventing new tooling. The
// agent runs the commands itself via the shell tool; this only suggests.
package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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
		scripts := nodeScripts(cwd)
		if scripts["typecheck"] {
			out = append(out, runner+" run typecheck")
		} else if exists("tsconfig.json") {
			out = append(out, "npx tsc --noEmit")
		}
		if scripts["lint"] {
			out = append(out, runner+" run lint")
		}
		if scripts["test"] {
			// `bun test` runs Bun's built-in test runner, not the package.json
			// "test" script; `bun run test` runs the script. npm/yarn/pnpm treat
			// `<pm> test` as the script already.
			if runner == "bun" {
				out = append(out, "bun run test")
			} else {
				out = append(out, runner+" test")
			}
		} else {
			// Many projects only define namespaced test scripts (test:unit,
			// test:ci, ...). Suggest those so the agent still has a test command.
			for _, name := range namespacedTestScripts(scripts) {
				out = append(out, runner+" run "+name)
			}
		}
	case exists("pyproject.toml") || exists("setup.py") || exists("pytest.ini") || exists("tox.ini"):
		out = append(out, "pytest -q")
	}

	if exists("Makefile") {
		mk := readFile(filepath.Join(cwd, "Makefile"))
		for _, target := range []string{"test", "lint", "check", "vet"} {
			if makeHasTarget(mk, target) {
				out = append(out, "make "+target)
			}
		}
	}
	return dedupe(out)
}

// makeHasTarget reports whether a Makefile defines a real rule named target. It
// matches "target:" / "target: deps" at the start of a line, but rejects
// variable assignments like "target :=" or "target ::=" (which a naive
// substring scan would mistake for a target) and prefix collisions like
// "testing:" when looking for "test".
func makeHasTarget(mk, target string) bool {
	for _, line := range strings.Split(mk, "\n") {
		if !strings.HasPrefix(line, target) {
			continue
		}
		rest := strings.TrimLeft(line[len(target):], " \t")
		if !strings.HasPrefix(rest, ":") {
			continue
		}
		rest = strings.TrimLeft(strings.TrimLeft(rest, ":"), " \t")
		if strings.HasPrefix(rest, "=") {
			continue // ":=" / "::=" etc. — a variable assignment, not a target
		}
		return true
	}
	return false
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

// nodeScripts parses the "scripts" object of package.json. Parsing (rather than
// a substring scan of the whole file) avoids both false positives — a dependency
// literally named "test" — and false negatives, and lets callers see namespaced
// script names like "test:unit".
func nodeScripts(cwd string) map[string]bool {
	data := readFile(filepath.Join(cwd, "package.json"))
	if data == "" {
		return nil
	}
	var pkg struct {
		Scripts map[string]json.RawMessage `json:"scripts"`
	}
	if json.Unmarshal([]byte(data), &pkg) != nil {
		return nil
	}
	out := make(map[string]bool, len(pkg.Scripts))
	for name := range pkg.Scripts {
		out[name] = true
	}
	return out
}

// namespacedTestScripts returns sorted "test:*" script names (e.g. test:unit),
// used as a fallback when no plain "test" script exists.
func namespacedTestScripts(scripts map[string]bool) []string {
	var out []string
	for name := range scripts {
		if strings.HasPrefix(name, "test:") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
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
