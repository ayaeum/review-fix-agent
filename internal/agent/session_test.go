package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/review-fix-agent/rfa/internal/contextmgr"
	"github.com/review-fix-agent/rfa/internal/message"
	"github.com/review-fix-agent/rfa/internal/model"
	"github.com/review-fix-agent/rfa/internal/permission"
	"github.com/review-fix-agent/rfa/internal/review"
)

func findingsPayload() map[string]any {
	return map[string]any{
		"findings": []any{
			map[string]any{
				"severity": "high", "file": "svc.go", "line": float64(2),
				"title": "nil deref", "evidence": "caller passes nil", "impact": "panic",
			},
		},
		"reviewed_scope": []any{"svc.go"},
		"verification":   "not run; review-only mode",
	}
}

func newReviewSession(t *testing.T, client model.Client) *Session {
	t.Helper()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "svc.go"), []byte("package svc\nfunc Start() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewSession(client, SessionConfig{
		Cwd:           cwd,
		Mode:          permission.ModeReview,
		MaxTurns:      10,
		TranscriptDir: t.TempDir(),
		Scope:         contextmgr.Scope{Mode: permission.ModeReview},
	})
}

// TestReviewHappyPath drives a full review: read a file, then submit findings.
func TestReviewHappyPath(t *testing.T) {
	client := &model.Mock{Responder: func(_ model.Request, turn int) message.Message {
		switch turn {
		case 0:
			return message.Message{Role: message.RoleAssistant, Content: []message.Block{
				message.ToolUse("t1", "read_file", map[string]any{"path": "svc.go"}),
			}}
		case 1:
			return message.Message{Role: message.RoleAssistant, Content: []message.Block{
				message.ToolUse("t2", "report_findings", findingsPayload()),
			}}
		default:
			return message.NewAssistantText("review complete")
		}
	}}

	sess := newReviewSession(t, client)
	res, err := sess.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.Findings == nil {
		t.Fatal("expected findings to be recorded")
	}
	rep, err := review.ParseReport(res.Findings)
	if err != nil {
		t.Fatalf("parse report: %v", err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Severity != "high" {
		t.Errorf("unexpected findings: %+v", rep.Findings)
	}

	// Every tool_use must have a paired tool_result; the read_file result must
	// not be an error and should contain the file content.
	assertPairing(t, res.Messages)
	if !hasNonErrorResultFor(res.Messages, "t1") {
		t.Error("read_file (t1) did not produce a successful tool_result")
	}
}

// TestReviewStopHookNudge verifies the loop refuses to finish until the model
// submits the structured report, injecting a hidden nudge message.
func TestReviewStopHookNudge(t *testing.T) {
	client := &model.Mock{Responder: func(_ model.Request, turn int) message.Message {
		switch turn {
		case 0:
			// Try to finish without calling report_findings.
			return message.NewAssistantText("Looks fine to me.")
		case 1:
			// After the nudge, submit an (empty) report.
			return message.Message{Role: message.RoleAssistant, Content: []message.Block{
				message.ToolUse("t9", "report_findings", map[string]any{
					"findings": []any{}, "reviewed_scope": []any{"svc.go"},
				}),
			}}
		default:
			return message.NewAssistantText("done")
		}
	}}

	sess := newReviewSession(t, client)
	res, err := sess.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.Findings == nil {
		t.Fatal("stop hook should have forced a report to be recorded")
	}
	// A hidden user message nudging report_findings must exist.
	found := false
	for _, m := range res.Messages {
		if m.Role == message.RoleUser && strings.Contains(m.Text(), "report_findings") {
			found = true
		}
	}
	if !found {
		t.Error("expected a hidden nudge user message mentioning report_findings")
	}
}

// TestReviewModeBlocksWrites confirms a write attempt in review mode is denied
// and still produces a paired (error) tool_result.
func TestReviewModeBlocksWrites(t *testing.T) {
	client := &model.Mock{Responder: func(_ model.Request, turn int) message.Message {
		switch turn {
		case 0:
			return message.Message{Role: message.RoleAssistant, Content: []message.Block{
				message.ToolUse("w1", "edit_file", map[string]any{
					"path": "svc.go", "old_string": "a", "new_string": "b",
				}),
			}}
		case 1:
			return message.Message{Role: message.RoleAssistant, Content: []message.Block{
				message.ToolUse("t2", "report_findings", findingsPayload()),
			}}
		default:
			return message.NewAssistantText("done")
		}
	}}

	sess := newReviewSession(t, client)
	res, err := sess.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	// edit_file is hidden in review mode, so the call resolves as "unknown tool";
	// either way it must be an error result, never a successful write.
	if !hasErrorResultFor(res.Messages, "w1") {
		t.Error("write attempt in review mode should have produced an error tool_result")
	}
	// Ensure svc.go was not modified.
	data, _ := os.ReadFile(filepath.Join(sess.Cfg.Cwd, "svc.go"))
	if strings.Contains(string(data), "b") && !strings.Contains(string(data), "package") {
		t.Error("file appears to have been modified in review mode")
	}
}

// --- assertions ---

func assertPairing(t *testing.T, msgs []message.Message) {
	t.Helper()
	useIDs := map[string]bool{}
	resultIDs := map[string]bool{}
	for _, m := range msgs {
		for _, b := range m.Content {
			switch b.Type {
			case message.BlockToolUse:
				useIDs[b.ToolUseID] = true
			case message.BlockToolResult:
				resultIDs[b.ToolUseID] = true
			}
		}
	}
	for id := range useIDs {
		if !resultIDs[id] {
			t.Errorf("tool_use %q has no paired tool_result", id)
		}
	}
}

func hasNonErrorResultFor(msgs []message.Message, id string) bool {
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == message.BlockToolResult && b.ToolUseID == id && !b.IsError {
				return true
			}
		}
	}
	return false
}

func hasErrorResultFor(msgs []message.Message, id string) bool {
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == message.BlockToolResult && b.ToolUseID == id && b.IsError {
				return true
			}
		}
	}
	return false
}
