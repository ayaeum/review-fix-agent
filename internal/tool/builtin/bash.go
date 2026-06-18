package builtin

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/review-fix-agent/rfa/internal/permission"
	"github.com/review-fix-agent/rfa/internal/tool"
)

// BashTool runs a shell command. It is the agent's verification surface: tests,
// linters, typecheckers and git inspection all flow through it. Read-only vs
// mutating vs destructive classification is delegated to the permission rules so
// the orchestrator can gate execution consistently.
type BashTool struct{}

func (BashTool) Name() string { return "run_command" }

func (BashTool) Description() string {
	return "在工作目录中运行 shell 命令。用于 git 检查（git diff/log/show）以及验证命令" +
		"（go test、go vet、npm test 等）。只读命令可直接运行；会修改状态的命令需要批准；" +
		"破坏性命令（rm、git push/commit/reset、sudo）会被阻止，请将这类需求记录为残余风险。"
}

func (BashTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":         map[string]any{"type": "string", "description": "要执行的 shell 命令。"},
			"timeout_seconds": map[string]any{"type": "integer", "description": "超时时间，单位秒（可选，默认 120，最大 600）。"},
		},
		"required": []string{"command"},
	}
}

// ReadOnly classifies the command; verification commands count as read-only.
func (BashTool) ReadOnly(input map[string]any) bool {
	cmd, _ := input["command"].(string)
	return permission.ClassifyCommand(cmd) == permission.ClassReadOnly
}

// ConcurrencySafe only when the command is read-only.
func (b BashTool) ConcurrencySafe(input map[string]any) bool { return b.ReadOnly(input) }

func (BashTool) Validate(input map[string]any) error {
	cmd, err := strInput(input, "command")
	if err != nil {
		return err
	}
	if strings.TrimSpace(cmd) == "" {
		return fmt.Errorf("command must not be empty")
	}
	return nil
}

func (BashTool) Call(ctx context.Context, input map[string]any, tc *tool.Context) (tool.Result, error) {
	cmd, _ := strInput(input, "command")
	timeout := intInput(input, "timeout_seconds", 120)
	if timeout <= 0 {
		timeout = 120
	}
	if timeout > 600 {
		timeout = 600
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	c := exec.CommandContext(runCtx, "sh", "-c", cmd)
	c.Dir = tc.Cwd
	// Run the command in its own process group so a timeout can kill the whole
	// tree. The default CommandContext cancel only SIGKILLs the direct child
	// (sh), orphaning descendants like `go test` and the compiler it spawns.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process != nil {
			// Negative pid = the process group whose pgid is the child's pid.
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	// Bound how long we wait for the group to die after cancellation.
	c.WaitDelay = 5 * time.Second
	// Capture combined output with a hard cap so a command with massive output
	// (e.g. cat of a large file, a very verbose test run) cannot buffer hundreds
	// of MB into memory before the result is truncated. Using one writer for both
	// streams makes os/exec serialize them through a single pipe (no race).
	out := &cappedBuffer{limit: maxCommandOutputBytes}
	c.Stdout = out
	c.Stderr = out
	err := c.Run()

	var b strings.Builder
	fmt.Fprintf(&b, "$ %s\n", cmd)
	b.Write(out.buf.Bytes())
	record := func(passed bool, summary string) {
		if tc != nil && tc.Sink != nil {
			tc.Sink.RecordCommand(tool.CommandRecord{
				Command: cmd,
				Passed:  passed,
				Summary: summary,
			})
		}
	}
	if runCtx.Err() == context.DeadlineExceeded {
		fmt.Fprintf(&b, "\n[command timed out after %ds]", timeout)
		record(false, fmt.Sprintf("timed out after %ds", timeout))
		return tool.Result{Text: truncate(b.String()), IsError: true}, nil
	}
	if err != nil {
		var exitErr *exec.ExitError
		if ee, ok := err.(*exec.ExitError); ok {
			exitErr = ee
			fmt.Fprintf(&b, "\n[exit code %d]", exitErr.ExitCode())
			record(false, fmt.Sprintf("exit code %d", exitErr.ExitCode()))
		} else {
			fmt.Fprintf(&b, "\n[error: %v]", err)
			record(false, err.Error())
		}
		// A non-zero exit is normal for failing tests; report as error so the
		// model can distinguish pass/fail, but keep the output.
		return tool.Result{Text: truncate(b.String()), IsError: true}, nil
	}
	if out.buf.Len() == 0 {
		b.WriteString("[no output; exit code 0]")
	}
	record(true, "exit code 0")
	return tool.Result{Text: truncate(b.String())}, nil
}

// maxCommandOutputBytes caps how much command output bash buffers in memory.
// The result is previewed down to maxResultBytes anyway, so capturing more than
// this only risks an OOM on a command that emits a huge stream.
const maxCommandOutputBytes = 1 << 20 // 1 MiB

// cappedBuffer accumulates up to limit bytes and silently discards the rest,
// while reporting every write as fully consumed so the writing process is not
// killed by a short write.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if remain := c.limit - c.buf.Len(); remain > 0 {
		if len(p) <= remain {
			c.buf.Write(p)
		} else {
			c.buf.Write(p[:remain])
		}
	}
	return len(p), nil
}
