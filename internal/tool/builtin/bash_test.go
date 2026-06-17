package builtin

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBashNormalCommand(t *testing.T) {
	tc := newReadCtx(t.TempDir())
	res, err := BashTool{}.Call(context.Background(), map[string]any{"command": "echo hi"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(res.Text, "hi") {
		t.Errorf("echo hi = %+v", res)
	}
	if recs := tc.Sink.CommandRecords(); len(recs) != 1 || !recs[0].Passed {
		t.Errorf("expected one passing command record, got %+v", recs)
	}
}

func TestBashNonZeroExitIsError(t *testing.T) {
	tc := newReadCtx(t.TempDir())
	res, _ := BashTool{}.Call(context.Background(), map[string]any{"command": "exit 3"}, tc)
	if !res.IsError || !strings.Contains(res.Text, "exit code 3") {
		t.Errorf("exit 3 = %+v", res)
	}
}

// TestBashTimeoutCancels verifies a long command is cancelled near its timeout
// (rather than running to completion), exercising the process-group kill path.
func TestBashTimeoutCancels(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test; skipped in -short")
	}
	tc := newReadCtx(t.TempDir())
	start := time.Now()
	// sleep spawned under `sh -c`; its own child must also be killed on timeout.
	res, _ := BashTool{}.Call(context.Background(), map[string]any{
		"command": "sleep 30", "timeout_seconds": 1,
	}, tc)
	elapsed := time.Since(start)
	if !res.IsError || !strings.Contains(res.Text, "timed out") {
		t.Errorf("expected timeout error, got %+v", res)
	}
	if elapsed > 10*time.Second {
		t.Errorf("command was not cancelled promptly: took %s", elapsed)
	}
}
