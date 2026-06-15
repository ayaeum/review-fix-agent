package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/review-fix-agent/rfa/internal/message"
	"github.com/review-fix-agent/rfa/internal/permission"
	"github.com/review-fix-agent/rfa/internal/tool"
)

// serialEmit wraps an emit callback with a mutex so parallel tool batches
// don't interleave ANSI output on the terminal.
func serialEmit(emit func(Event)) func(Event) {
	if emit == nil {
		return nil
	}
	var mu sync.Mutex
	return func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		emit(e)
	}
}

// Orchestrator executes the tool_use blocks of one assistant turn. It enforces
// the tool_use/tool_result pairing invariant (one result per use, always) and
// schedules read-only/concurrency-safe tools in parallel batches while running
// mutating tools serially.
type Orchestrator struct {
	Reg  *tool.Registry
	Perm *permission.Engine
}

// RunTools executes every tool_use block and returns a user message whose
// content holds exactly one tool_result per tool_use, in original order.
func (o *Orchestrator) RunTools(ctx context.Context, uses []message.Block, tc *tool.Context, emit func(Event)) message.Message {
	results := make([]message.Block, len(uses))
	emit = serialEmit(emit)

	i := 0
	for i < len(uses) {
		// Grow a batch of consecutive concurrency-safe calls.
		j := i
		for j < len(uses) && o.concurrencySafe(uses[j]) {
			j++
		}
		if j > i {
			o.runParallel(ctx, uses[i:j], results[i:j], tc, emit)
			i = j
			continue
		}
		// Otherwise run a single mutating/serial call.
		results[i] = o.runOne(ctx, uses[i], tc, emit)
		i++
	}

	return message.Message{Role: message.RoleUser, Content: results}
}

// concurrencySafe reports whether a tool_use can join a parallel batch: the tool
// must exist and declare itself concurrency-safe for this input.
func (o *Orchestrator) concurrencySafe(use message.Block) bool {
	t, ok := o.Reg.Get(use.ToolName)
	if !ok {
		return false // unknown tool runs serially to produce a clean error
	}
	return t.ConcurrencySafe(use.Input)
}

// runParallel executes a batch concurrently, writing results back in order.
func (o *Orchestrator) runParallel(ctx context.Context, uses []message.Block, out []message.Block, tc *tool.Context, emit func(Event)) {
	var wg sync.WaitGroup
	for k := range uses {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			out[idx] = o.runOne(ctx, uses[idx], tc, emit)
		}(k)
	}
	wg.Wait()
}

// runOne resolves, validates, permission-checks, and executes a single tool_use,
// always returning a paired tool_result block (synthetic on any failure).
func (o *Orchestrator) runOne(ctx context.Context, use message.Block, tc *tool.Context, emit func(Event)) (result message.Block) {
	id := use.ToolUseID

	// Guarantee a paired result even on panic.
	defer func() {
		if r := recover(); r != nil {
			result = message.ToolResult(id, fmt.Sprintf("tool panicked: %v", r), true)
		}
	}()

	t, ok := o.Reg.Get(use.ToolName)
	if !ok {
		return message.ToolResult(id, fmt.Sprintf("unknown tool %q", use.ToolName), true)
	}

	if err := t.Validate(use.Input); err != nil {
		return message.ToolResult(id, fmt.Sprintf("invalid input: %v", err), true)
	}

	decision := o.Perm.Check(t, use.Input)
	if decision.Decision != permission.Allow {
		emitEvent(emit, Event{Kind: EvToolDenied, ToolName: t.Name(), ToolUseID: id, ToolInput: use.Input, Text: decision.Reason, IsError: true})
		return message.ToolResult(id, "permission denied: "+decision.Reason, true)
	}

	emitEvent(emit, Event{Kind: EvToolStart, ToolName: t.Name(), ToolUseID: id, ToolInput: use.Input})

	start := time.Now()
	res, err := t.Call(ctx, use.Input, tc)
	dur := time.Since(start)
	if err != nil {
		emitEvent(emit, Event{Kind: EvToolEnd, ToolName: t.Name(), ToolUseID: id, Text: err.Error(), IsError: true, Duration: dur})
		return message.ToolResult(id, "tool error: "+err.Error(), true)
	}

	emitEvent(emit, Event{Kind: EvToolEnd, ToolName: t.Name(), ToolUseID: id, Text: res.Text, IsError: res.IsError, Duration: dur})
	return message.ToolResult(id, res.Text, res.IsError)
}

func emitEvent(emit func(Event), e Event) {
	if emit != nil {
		emit(e)
	}
}
