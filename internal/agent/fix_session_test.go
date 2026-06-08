package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/review-fix-agent/rfa/internal/contextmgr"
	"github.com/review-fix-agent/rfa/internal/fix"
	"github.com/review-fix-agent/rfa/internal/message"
	"github.com/review-fix-agent/rfa/internal/model"
	"github.com/review-fix-agent/rfa/internal/permission"
)

// TestFixHappyPath drives a full fix: read, edit, verify (read-only command),
// then report_fix. It asserts the file was changed and the report recorded.
func TestFixHappyPath(t *testing.T) {
	cwd := t.TempDir()
	src := "package a\n\nfunc Add(x, y int) int { return x - y }\n" // bug: minus
	path := filepath.Join(cwd, "a.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &model.Mock{Responder: func(_ model.Request, turn int) message.Message {
		switch turn {
		case 0:
			return toolTurn("r1", "read_file", map[string]any{"path": "a.go"})
		case 1:
			return toolTurn("e1", "edit_file", map[string]any{
				"path": "a.go", "old_string": "return x - y", "new_string": "return x + y",
			})
		case 2:
			return toolTurn("v1", "run_command", map[string]any{"command": "grep -n 'x + y' a.go"})
		case 3:
			return toolTurn("f1", "report_fix", map[string]any{
				"summary":       "Add used subtraction; switched to addition.",
				"patch_scope":   "single expression in Add; nothing else touched",
				"changed_files": []any{"a.go"},
				"verification": []any{
					map[string]any{"command": "grep -n 'x + y' a.go", "passed": true, "summary": "addition present"},
				},
				"residual_risk": "none",
			})
		default:
			return message.NewAssistantText("fix complete")
		}
	}}

	sess := NewSession(client, SessionConfig{
		Cwd:           cwd,
		Mode:          permission.ModeFix,
		MaxTurns:      10,
		TranscriptDir: t.TempDir(),
		Scope:         contextmgr.Scope{Mode: permission.ModeFix, Issue: "Add subtracts instead of adds"},
	})

	res, err := sess.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "return x + y") {
		t.Errorf("file not patched: %q", string(data))
	}
	if res.Fix == nil {
		t.Fatal("expected fix report to be recorded")
	}
	rep, err := fix.ParseReport(res.Fix)
	if err != nil {
		t.Fatalf("parse fix report: %v", err)
	}
	if !rep.AllPassed() {
		t.Error("expected verification to be marked passed")
	}
	if len(rep.ChangedFiles) != 1 || rep.ChangedFiles[0] != "a.go" {
		t.Errorf("changed files = %v, want [a.go]", rep.ChangedFiles)
	}
	assertPairing(t, res.Messages)
}

// TestFixBlocksDestructiveCommand ensures a destructive verification command is
// denied (paired error) and does not run.
func TestFixBlocksDestructiveCommand(t *testing.T) {
	cwd := t.TempDir()
	os.WriteFile(filepath.Join(cwd, "a.go"), []byte("package a\n"), 0o644)

	client := &model.Mock{Responder: func(_ model.Request, turn int) message.Message {
		switch turn {
		case 0:
			return toolTurn("d1", "run_command", map[string]any{"command": "rm -rf a.go"})
		case 1:
			return toolTurn("f1", "report_fix", map[string]any{
				"summary": "n/a", "changed_files": []any{}, "verification": []any{},
				"residual_risk": "blocked destructive command",
			})
		default:
			return message.NewAssistantText("done")
		}
	}}

	sess := NewSession(client, SessionConfig{
		Cwd: cwd, Mode: permission.ModeFix, MaxTurns: 10, TranscriptDir: t.TempDir(),
		Scope: contextmgr.Scope{Mode: permission.ModeFix},
	})
	res, err := sess.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !hasErrorResultFor(res.Messages, "d1") {
		t.Error("destructive command should produce an error tool_result")
	}
	if _, err := os.Stat(filepath.Join(cwd, "a.go")); err != nil {
		t.Error("a.go should still exist; destructive command must not have run")
	}
}

func toolTurn(id, name string, input map[string]any) message.Message {
	return message.Message{Role: message.RoleAssistant, Content: []message.Block{
		message.ToolUse(id, name, input),
	}}
}
