package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/review-fix-agent/rfa/internal/message"
	"github.com/review-fix-agent/rfa/internal/permission"
	"github.com/review-fix-agent/rfa/internal/tool"
)

// --- fake tools ---

type baseTool struct {
	name        string
	readOnly    bool
	concurrent  bool
	validateErr error
}

func (b baseTool) Name() string                        { return b.name }
func (b baseTool) Description() string                 { return "" }
func (b baseTool) InputSchema() map[string]any         { return map[string]any{} }
func (b baseTool) ReadOnly(map[string]any) bool        { return b.readOnly }
func (b baseTool) ConcurrencySafe(map[string]any) bool { return b.concurrent }
func (b baseTool) Validate(map[string]any) error       { return b.validateErr }

type okTool struct{ baseTool }

func (o okTool) Call(_ context.Context, in map[string]any, _ *tool.Context) (tool.Result, error) {
	tag, _ := in["tag"].(string)
	return tool.Result{Text: "ok:" + tag}, nil
}

type errTool struct{ baseTool }

func (e errTool) Call(context.Context, map[string]any, *tool.Context) (tool.Result, error) {
	return tool.Result{}, errors.New("kaboom")
}

type panicTool struct{ baseTool }

func (p panicTool) Call(context.Context, map[string]any, *tool.Context) (tool.Result, error) {
	panic("explode")
}

func newOrch(mode permission.Mode, tools ...tool.Tool) *Orchestrator {
	return &Orchestrator{
		Reg:  tool.NewRegistry(tools...),
		Perm: &permission.Engine{Mode: mode, Cwd: "/repo"},
	}
}

func uses(specs ...message.Block) []message.Block { return specs }

func TestPairingInvariant(t *testing.T) {
	orch := newOrch(permission.ModeReview,
		okTool{baseTool{name: "ok", readOnly: true, concurrent: false}},
		errTool{baseTool{name: "boom", readOnly: true}},
		panicTool{baseTool{name: "panic", readOnly: true}},
		okTool{baseTool{name: "badinput", readOnly: true, validateErr: errors.New("bad")}},
		okTool{baseTool{name: "edit_file"}}, // writer -> denied in review
	)

	in := uses(
		message.ToolUse("u1", "ok", map[string]any{"tag": "a"}),
		message.ToolUse("u2", "ghost", nil),                             // unknown tool
		message.ToolUse("u3", "boom", nil),                              // returns error
		message.ToolUse("u4", "panic", nil),                             // panics
		message.ToolUse("u5", "badinput", nil),                          // validation fails
		message.ToolUse("u6", "edit_file", map[string]any{"path": "x"}), // permission denied
	)

	out := orch.RunTools(context.Background(), in, &tool.Context{Cwd: "/repo"}, nil)

	if len(out.Content) != len(in) {
		t.Fatalf("expected %d results, got %d", len(in), len(out.Content))
	}
	for i, b := range out.Content {
		if b.Type != message.BlockToolResult {
			t.Errorf("result[%d] is not a tool_result", i)
		}
		if b.ToolUseID != in[i].ToolUseID {
			t.Errorf("result[%d] paired to %q, want %q", i, b.ToolUseID, in[i].ToolUseID)
		}
	}

	// u1 succeeds; the rest are errors.
	if out.Content[0].IsError {
		t.Errorf("u1 should succeed, got error: %s", out.Content[0].ResultText)
	}
	if out.Content[0].ResultText != "ok:a" {
		t.Errorf("u1 text = %q, want ok:a", out.Content[0].ResultText)
	}
	for _, i := range []int{1, 2, 3, 4, 5} {
		if !out.Content[i].IsError {
			t.Errorf("result[%d] should be an error result", i)
		}
	}
}

func TestConcurrentBatchPreservesOrder(t *testing.T) {
	orch := newOrch(permission.ModeReview,
		okTool{baseTool{name: "ok", readOnly: true, concurrent: true}},
	)
	var in []message.Block
	for i := 0; i < 20; i++ {
		in = append(in, message.ToolUse(fmt.Sprintf("u%d", i), "ok", map[string]any{"tag": fmt.Sprintf("%d", i)}))
	}
	out := orch.RunTools(context.Background(), in, &tool.Context{Cwd: "/repo"}, nil)
	if len(out.Content) != 20 {
		t.Fatalf("expected 20 results, got %d", len(out.Content))
	}
	for i, b := range out.Content {
		want := fmt.Sprintf("ok:%d", i)
		if b.ResultText != want {
			t.Errorf("result[%d] = %q, want %q (order not preserved)", i, b.ResultText, want)
		}
		if b.ToolUseID != fmt.Sprintf("u%d", i) {
			t.Errorf("result[%d] paired to %q, want u%d", i, b.ToolUseID, i)
		}
	}
}
