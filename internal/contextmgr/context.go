package contextmgr

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

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
	fmt.Fprintf(&b, "- 日期：%s\n", time.Now().Format("2006-01-02"))
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
		b.WriteString("\n## Diff\n```diff\n")
		b.WriteString(truncateDiff(diff))
		b.WriteString("\n```\n")
	} else {
		b.WriteString("\n## Diff\n（未检测到 diff；请通过 run_command 使用 git，或直接读取文件来确定范围）\n")
	}

	if len(rules) > 0 {
		b.WriteString("\n## 项目规则\n")
		for _, r := range rules {
			fmt.Fprintf(&b, "\n### %s\n%s\n", r.Path, r.Content)
		}
	}

	b.WriteString("\n## 任务步骤\n")
	if scope.Mode == permission.ModeFix {
		b.WriteString(fixInstructions)
		if hints := verify.Suggest(m.Cwd); len(hints) > 0 {
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

func truncateDiff(diff string) string {
	const max = 40 * 1024
	if len(diff) <= max {
		return diff
	}
	return diff[:max] + "\n[... diff 已截断；请读取具体文件获取完整上下文 ...]"
}
