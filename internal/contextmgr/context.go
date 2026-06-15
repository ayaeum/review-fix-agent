package contextmgr

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/review-fix-agent/rfa/internal/permission"
	"github.com/review-fix-agent/rfa/internal/verify"
)

// Scope describes what the agent should work on for this run.
type Scope struct {
	Mode   permission.Mode
	Base   string   // diff base ref (optional)
	Commit string   // Review Mode: commit to review (optional)
	Files  []string // explicit focus files (optional)
	Issue  string   // Fix Mode: the known issue
	Focus  string   // Review Mode: optional focus prompt
}

// Built is the assembled context handed to the loop.
type Built struct {
	System      string
	InitialUser string
	Diff        string
	Changed     []ChangedFile
}

// Manager assembles context around the scope (diff + rules + system state).
type Manager struct {
	Cwd string
}

// NewManager constructs a context manager rooted at cwd.
func NewManager(cwd string) *Manager { return &Manager{Cwd: cwd} }

// Build assembles the system prompt and the initial user message.
func (m *Manager) Build(ctx context.Context, scope Scope) (Built, error) {
	diff, err := RunGitDiff(ctx, m.Cwd, scope.Base, scope.Commit)
	if err != nil && strings.TrimSpace(scope.Commit) != "" {
		return Built{}, fmt.Errorf("collect diff: %w", err)
	}
	changed := ParseUnifiedDiff(diff)
	rules := LoadRuleFiles(ctx, m.Cwd)

	var sys string
	switch scope.Mode {
	case permission.ModeFix:
		sys = systemPromptFix
	default:
		sys = systemPromptReview
	}
	sys += "\n\n" + m.systemState(ctx)

	user := m.initialUser(scope, diff, changed, rules)
	return Built{System: sys, InitialUser: user, Diff: diff, Changed: changed}, nil
}

// systemState appends environment facts to the system prompt.
func (m *Manager) systemState(ctx context.Context) string {
	var b strings.Builder
	b.WriteString("# 环境\n")
	fmt.Fprintf(&b, "- 工作目录：%s\n", m.Cwd)
	if st := gitStatus(ctx, m.Cwd); st != "" {
		fmt.Fprintf(&b, "- Git 状态：\n%s\n", indent(st, "    "))
	}
	return b.String()
}

// initialUser composes the first user message: scope + changed files + diff + rules.
func (m *Manager) initialUser(scope Scope, diff string, changed []ChangedFile, rules []RuleFile) string {
	var b strings.Builder

	if scope.Mode == permission.ModeFix {
		b.WriteString("# 待修复的已知问题\n\n")
		if strings.TrimSpace(scope.Issue) == "" {
			b.WriteString("（未提供问题描述；请根据 diff 和项目状态推断问题）\n")
		} else {
			b.WriteString(scope.Issue)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("# 代码审查请求\n\n")
		if strings.TrimSpace(scope.Focus) != "" {
			fmt.Fprintf(&b, "关注点：%s\n", scope.Focus)
		}
	}

	if len(scope.Files) > 0 {
		b.WriteString("\n## 明确关注文件\n")
		for _, f := range scope.Files {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}

	if strings.TrimSpace(scope.Commit) != "" {
		fmt.Fprintf(&b, "\n## 待审查 commit\n- %s\n", scope.Commit)
	}

	if len(changed) > 0 {
		b.WriteString("\n## 变更文件\n")
		for _, c := range changed {
			if c.Binary {
				fmt.Fprintf(&b, "- %s（二进制）\n", c.Path())
				continue
			}
			fmt.Fprintf(&b, "- %s（%d 个 hunk，%d 行新增）\n", c.Path(), len(c.Hunks), len(c.AddedLines()))
		}
	}

	if strings.TrimSpace(diff) != "" {
		if len(diff) <= largeDiffThreshold {
			b.WriteString("\n## Diff\n```diff\n")
			b.WriteString(truncateDiff(diff))
			b.WriteString("\n```\n")
		} else {
			b.WriteString("\n## Diff\n")
			b.WriteString("diff 内容较大，已省略全文。请使用 `run_command` 执行 `git diff -- <file>` 或 `read_file` 逐文件查看变更。\n")
			b.WriteString("上方「变更文件」列表是完整范围，请逐文件审查，不要遗漏。\n")
		}
	} else {
		b.WriteString("\n## Diff\n（未检测到 diff；请通过 run_command 使用 git，或直接读取文件来确定范围）\n")
	}

	if len(rules) > 0 {
		b.WriteString("\n## 项目规则\n")
		for _, r := range rules {
			fmt.Fprintf(&b, "\n### %s\n%s\n", r.Path, r.Content)
		}
	}

	fmt.Fprintf(&b, "\n日期：%s\n", time.Now().Format("2006-01-02"))

	b.WriteString("\n## 任务步骤\n")
	if scope.Mode == permission.ModeFix {
		b.WriteString(fixInstructions)
		if hints := verify.Suggest(m.Cwd, changedPaths(changed)...); len(hints) > 0 {
			b.WriteString("\n\n## 检测到的可用验证命令\n")
			for _, h := range hints {
				fmt.Fprintf(&b, "- `%s`\n", h)
			}
		}
	} else {
		b.WriteString(reviewInstructions)
	}
	return b.String()
}

func gitStatus(ctx context.Context, cwd string) string {
	cmd := exec.CommandContext(ctx, "git", "status", "--short", "--branch")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func indent(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// largeDiffThreshold is the byte size above which the full diff is NOT pushed
// into the initial user message. Instead, only the file map is sent and the
// agent is instructed to pull individual file diffs via git/read_file.
const largeDiffThreshold = 80 * 1024

func truncateDiff(diff string) string {
	const budget = 40 * 1024
	if len(diff) <= budget {
		return diff
	}
	return prioritizedTruncate(diff, budget)
}

// prioritizedTruncate keeps high-priority file diffs intact and drops
// low-priority ones (tests, generated, vendored, lock files) first.
func prioritizedTruncate(diff string, budget int) string {
	files := splitDiffByFile(diff)
	if len(files) == 0 {
		return safeSlice(diff, budget) + "\n[... diff 已截断；请读取具体文件获取完整上下文 ...]"
	}

	type entry struct {
		header string // "diff --git ..." through end of file diff
		prio   int    // lower = keep first
	}
	entries := make([]entry, len(files))
	for i, f := range files {
		entries[i] = entry{header: f, prio: filePriority(f)}
	}

	// Sort stable by priority (high-priority first).
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].prio < entries[j].prio
	})

	var kept []string
	used := 0
	dropped := 0
	for _, e := range entries {
		if used+len(e.header) <= budget {
			kept = append(kept, e.header)
			used += len(e.header)
		} else {
			dropped++
		}
	}
	result := strings.Join(kept, "\n")
	if dropped > 0 {
		result += fmt.Sprintf("\n[... 省略了 %d 个低优先级文件的 diff；请用 read_file 或 git diff -- <file> 查看 ...]", dropped)
	}
	return result
}

// splitDiffByFile splits a unified diff into per-file chunks.
func splitDiffByFile(diff string) []string {
	var files []string
	var cur strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") && cur.Len() > 0 {
			files = append(files, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		files = append(files, cur.String())
	}
	return files
}

// lowPrioSuffixes are file suffixes whose diffs are dropped first when the
// budget is tight. Order does not matter — they all get the same priority.
var lowPrioSuffixes = []string{
	"_test.go", ".test.ts", ".test.js", ".spec.ts", ".spec.js", "_test.py",
	".lock", ".sum", ".snap",
	".pb.go", ".gen.go", "_generated.go", ".generated.ts",
	".min.js", ".min.css",
}

func filePriority(chunk string) int {
	first := chunk
	if i := strings.IndexByte(first, '\n'); i > 0 {
		first = first[:i]
	}
	low := strings.ToLower(first)
	if strings.Contains(low, "vendor/") || strings.Contains(low, "node_modules/") ||
		strings.Contains(low, "generated/") || strings.Contains(low, "dist/") {
		return 3
	}
	for _, suf := range lowPrioSuffixes {
		if strings.Contains(low, suf) {
			return 2
		}
	}
	return 1
}

func safeSlice(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// changedPaths returns the display path of each changed file, used to scope
// targeted verification commands (verify.Suggest) to the affected packages.
func changedPaths(changed []ChangedFile) []string {
	var out []string
	for _, c := range changed {
		if p := c.Path(); p != "" {
			out = append(out, p)
		}
	}
	return out
}
