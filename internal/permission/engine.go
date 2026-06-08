// Package permission implements the two-stage permission model from the
// architecture doc: (1) filter tool visibility before the model ever sees them,
// and (2) validate the concrete input before execution. Review Mode is read-only;
// Fix Mode may write within scope; destructive shell actions are always denied.
package permission

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/review-fix-agent/rfa/internal/tool"
)

// Mode is the operating mode of the agent.
type Mode string

const (
	ModeReview Mode = "review"
	ModeFix    Mode = "fix"
)

// Decision is the outcome of a permission check.
type Decision int

const (
	Allow Decision = iota
	Ask
	Deny
)

// Result is a decision with a human-readable reason.
type Result struct {
	Decision Decision
	Reason   string
}

// Asker resolves an Ask decision interactively. Headless runs supply a policy
// (auto-approve or auto-deny) instead.
type Asker func(toolName, summary, reason string) bool

// Engine holds mode-specific policy and answers permission questions.
type Engine struct {
	Mode Mode
	Cwd  string
	// AutoApprove turns Ask decisions into Allow (headless "trusted" runs).
	AutoApprove bool
	// Ask, if set, is consulted for Ask decisions (interactive TTY).
	Ask Asker
}

// writerTools are tools that mutate the working tree. They are hidden from the
// model entirely in Review Mode.
var writerTools = map[string]bool{
	"edit_file":  true,
	"write_file": true,
}

// VisibleTools filters the tool list to what the model may see in the current
// mode. Hiding writers in Review Mode prevents the model from even attempting a
// mutation — the doc's "filter visibility before execution" principle.
func (e *Engine) VisibleTools(tools []tool.Tool) []tool.Tool {
	var out []tool.Tool
	for _, t := range tools {
		if e.Mode == ModeReview && writerTools[t.Name()] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Check decides whether a concrete tool invocation may run.
func (e *Engine) Check(t tool.Tool, input map[string]any) Result {
	name := t.Name()

	// Hard block: writers in review mode (defence-in-depth behind VisibleTools).
	if e.Mode == ModeReview && writerTools[name] {
		return Result{Deny, "Review Mode is read-only; code changes are not permitted"}
	}

	// Read-only invocations are always allowed.
	if t.ReadOnly(input) {
		return Result{Allow, "read-only"}
	}

	switch name {
	case "edit_file", "write_file":
		// Fix Mode writers are allowed within the working directory without a
		// prompt — that is the point of Fix Mode. Out-of-scope paths are denied.
		path, _ := input["path"].(string)
		if !e.withinCwd(path) {
			return Result{Deny, fmt.Sprintf("path %q is outside the working directory", path)}
		}
		return Result{Allow, "write within Fix Mode scope"}
	case "run_command", "bash":
		cmd, _ := input["command"].(string)
		switch ClassifyCommand(cmd) {
		case ClassReadOnly:
			return Result{Allow, "read-only command"}
		case ClassDestructive:
			return Result{Deny, "destructive or outward-facing command is blocked; report it as residual risk"}
		default:
			if e.Mode == ModeReview {
				return Result{Deny, "Review Mode only permits read-only commands"}
			}
			return e.resolveAsk(name, cmd, "mutating command requires confirmation")
		}
	}

	// Unknown non-read-only tool: be conservative.
	if e.Mode == ModeReview {
		return Result{Deny, "Review Mode is read-only"}
	}
	return e.resolveAsk(name, name, "non-read-only tool requires confirmation")
}

// resolveAsk converts an Ask into a concrete decision using the configured
// policy: explicit asker, else auto-approve flag, else deny.
func (e *Engine) resolveAsk(toolName, summary, reason string) Result {
	if e.AutoApprove {
		return Result{Allow, reason + " (auto-approved)"}
	}
	if e.Ask != nil {
		if e.Ask(toolName, summary, reason) {
			return Result{Allow, "approved interactively"}
		}
		return Result{Deny, "declined by user"}
	}
	return Result{Deny, "no approval channel; denied by default"}
}

// withinCwd reports whether path resolves inside the engine's working directory.
func (e *Engine) withinCwd(path string) bool {
	if path == "" {
		return false
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(e.Cwd, path)
	}
	abs = filepath.Clean(abs)
	root := filepath.Clean(e.Cwd)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
