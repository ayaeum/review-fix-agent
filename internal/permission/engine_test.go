package permission

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/review-fix-agent/rfa/internal/tool"
)

// fakeTool is a minimal tool.Tool for permission tests.
type fakeTool struct {
	name     string
	readOnly func(map[string]any) bool
}

func (f fakeTool) Name() string                        { return f.name }
func (f fakeTool) Description() string                 { return "" }
func (f fakeTool) InputSchema() map[string]any         { return map[string]any{} }
func (f fakeTool) ConcurrencySafe(map[string]any) bool { return false }
func (f fakeTool) Validate(map[string]any) error       { return nil }
func (f fakeTool) Call(context.Context, map[string]any, *tool.Context) (tool.Result, error) {
	return tool.Result{}, nil
}
func (f fakeTool) ReadOnly(in map[string]any) bool {
	if f.readOnly != nil {
		return f.readOnly(in)
	}
	return false
}

var (
	readTool  = fakeTool{name: "read_file", readOnly: func(map[string]any) bool { return true }}
	editTool  = fakeTool{name: "edit_file"}
	writeTool = fakeTool{name: "write_file"}
	bashTool  = fakeTool{name: "run_command", readOnly: func(in map[string]any) bool {
		c, _ := in["command"].(string)
		return ClassifyCommand(c) == ClassReadOnly
	}}
)

func TestReviewModeDeniesWriters(t *testing.T) {
	e := &Engine{Mode: ModeReview, Cwd: "/repo"}
	if got := e.Check(editTool, map[string]any{"path": "/repo/a.go"}); got.Decision != Deny {
		t.Errorf("review edit = %v, want Deny", got.Decision)
	}
}

func TestReviewModeAllowsReadOnly(t *testing.T) {
	e := &Engine{Mode: ModeReview, Cwd: "/repo"}
	if got := e.Check(readTool, map[string]any{"path": "a.go"}); got.Decision != Allow {
		t.Errorf("review read = %v, want Allow", got.Decision)
	}
	if got := e.Check(bashTool, map[string]any{"command": "git diff"}); got.Decision != Allow {
		t.Errorf("review git diff = %v, want Allow", got.Decision)
	}
	if got := e.Check(bashTool, map[string]any{"command": "go generate ./..."}); got.Decision != Deny {
		t.Errorf("review mutating cmd = %v, want Deny", got.Decision)
	}
}

func TestFixModeWriteScope(t *testing.T) {
	cwd := mustAbs(t, ".")
	e := &Engine{Mode: ModeFix, Cwd: cwd}

	in := map[string]any{"path": filepath.Join(cwd, "x.go")}
	if got := e.Check(editTool, in); got.Decision != Allow {
		t.Errorf("fix edit in-scope = %v (%s), want Allow", got.Decision, got.Reason)
	}
	out := map[string]any{"path": "/etc/passwd"}
	if got := e.Check(writeTool, out); got.Decision != Deny {
		t.Errorf("fix write out-of-scope = %v, want Deny", got.Decision)
	}
}

func TestFixModeCommandPolicy(t *testing.T) {
	cwd := mustAbs(t, ".")
	// destructive always denied
	e := &Engine{Mode: ModeFix, Cwd: cwd}
	if got := e.Check(bashTool, map[string]any{"command": "rm -rf x"}); got.Decision != Deny {
		t.Errorf("fix destructive = %v, want Deny", got.Decision)
	}
	// mutating without approver -> deny
	if got := e.Check(bashTool, map[string]any{"command": "gofmt -w ."}); got.Decision != Deny {
		t.Errorf("fix mutating (no approver) = %v, want Deny", got.Decision)
	}
	// mutating with auto-approve -> allow
	e2 := &Engine{Mode: ModeFix, Cwd: cwd, AutoApprove: true}
	if got := e2.Check(bashTool, map[string]any{"command": "gofmt -w ."}); got.Decision != Allow {
		t.Errorf("fix mutating (auto) = %v, want Allow", got.Decision)
	}
}

func TestVisibleToolsHidesWritersInReview(t *testing.T) {
	all := []tool.Tool{readTool, editTool, writeTool, bashTool}
	e := &Engine{Mode: ModeReview, Cwd: "/repo"}
	vis := e.VisibleTools(all)
	for _, tl := range vis {
		if tl.Name() == "edit_file" || tl.Name() == "write_file" {
			t.Errorf("review mode exposed writer %q", tl.Name())
		}
	}
	if len(vis) != 2 {
		t.Errorf("expected 2 visible tools in review, got %d", len(vis))
	}

	eFix := &Engine{Mode: ModeFix, Cwd: "/repo"}
	if len(eFix.VisibleTools(all)) != 4 {
		t.Errorf("fix mode should expose all 4 tools")
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
