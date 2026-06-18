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

func TestBashCapsHugeOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a process; skipped in -short")
	}
	tc := newReadCtx(t.TempDir())
	// Emit far more than the capture cap; the tool must not buffer it all.
	res, _ := BashTool{}.Call(context.Background(), map[string]any{
		"command": "yes x | head -c 5000000", // ~5MB
	}, tc)
	if res.IsError {
		t.Fatalf("command should succeed: %s", res.Text[:min2(200, len(res.Text))])
	}
	// Result is previewed down to maxResultBytes regardless of the 5MB stream.
	if len(res.Text) > maxResultBytes+512 {
		t.Errorf("output not capped: %d bytes", len(res.Text))
	}
}

func TestCappedBufferDiscardsExcess(t *testing.T) {
	b := &cappedBuffer{limit: 10}
	n, _ := b.Write([]byte("hello"))
	if n != 5 {
		t.Errorf("Write reported %d, want 5 (full consume)", n)
	}
	n, _ = b.Write([]byte("world!!!!!")) // would overflow
	if n != 10 {
		t.Errorf("Write reported %d, want 10 (full consume even when capped)", n)
	}
	if b.buf.Len() != 10 {
		t.Errorf("buffered %d bytes, want cap 10", b.buf.Len())
	}
	if b.buf.String() != "helloworld" {
		t.Errorf("buffered = %q, want 'helloworld'", b.buf.String())
	}
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}
