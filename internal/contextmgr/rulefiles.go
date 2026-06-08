package contextmgr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ruleFileNames are the project rule files recognized, in lookup order per dir.
var ruleFileNames = []string{"AGENTS.md", "CLAUDE.md", "RFA.md", ".rfa/rules.md"}

// RuleFile is a discovered rule document.
type RuleFile struct {
	Path    string
	Content string
}

// LoadRuleFiles discovers project rule files from the repo root down to cwd.
// Deeper files appear later (higher priority), matching the doc's layering:
// root rules < nested directory rules.
func LoadRuleFiles(ctx context.Context, cwd string) []RuleFile {
	root := gitRoot(ctx, cwd)
	if root == "" {
		root = cwd
	}
	dirs := dirChain(root, cwd)

	var out []RuleFile
	seen := map[string]bool{}
	for _, dir := range dirs {
		for _, name := range ruleFileNames {
			p := filepath.Join(dir, name)
			if seen[p] {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			seen[p] = true
			rel, _ := filepath.Rel(cwd, p)
			if rel == "" {
				rel = p
			}
			out = append(out, RuleFile{Path: rel, Content: strings.TrimSpace(string(data))})
		}
	}
	return out
}

// gitRoot returns the repository top level, or "" if not in a git repo.
func gitRoot(ctx context.Context, cwd string) string {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// dirChain returns directories from root down to leaf (inclusive), in order.
func dirChain(root, leaf string) []string {
	root = filepath.Clean(root)
	leaf = filepath.Clean(leaf)
	rel, err := filepath.Rel(root, leaf)
	if err != nil || strings.HasPrefix(rel, "..") {
		return []string{leaf}
	}
	dirs := []string{root}
	if rel == "." {
		return dirs
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		dirs = append(dirs, cur)
	}
	return dirs
}
