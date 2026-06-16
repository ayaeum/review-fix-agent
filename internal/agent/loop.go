package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/review-fix-agent/rfa/internal/message"
	"github.com/review-fix-agent/rfa/internal/model"
	"github.com/review-fix-agent/rfa/internal/permission"
	"github.com/review-fix-agent/rfa/internal/tool"
	"github.com/review-fix-agent/rfa/internal/transcript"
)

// Config holds per-run loop configuration.
type Config struct {
	Model       string
	MaxTokens   int
	Temperature float64
	MaxTurns    int
	Mode        permission.Mode
	System      string
	Tools       []tool.Tool // already visibility-filtered for the mode
}

// Loop is the single agentic loop. It is the explicit state machine described in
// the architecture doc: preprocess context, call the model, run tools, append
// tool_result, repeat until a clean terminal turn that satisfies the stop hook.
type Loop struct {
	Client     model.Client
	Orch       *Orchestrator
	ToolCtx    *tool.Context
	Cfg        Config
	Transcript *transcript.Store
}

// maxFinalizerReminders bounds how many times the stop hook nudges the model to
// emit its structured report before the loop gives up (prevents runaways).
const maxFinalizerReminders = 3

// deadlineNudgeTurns is the number of remaining turns at which the loop injects
// a hidden message urging the model to stop exploring and submit its report.
const deadlineNudgeTurns = 5

// ErrMissingFinalizer is returned when the loop stops without the required
// structured report being recorded in the sink.
var ErrMissingFinalizer = errors.New("agent finished without submitting the required report")

// Run drives the loop to completion and returns the full message history.
func (l *Loop) Run(ctx context.Context, initial []message.Message, emit func(Event)) ([]message.Message, error) {
	state := append([]message.Message(nil), initial...)
	for _, m := range initial {
		l.Transcript.Append("message", m)
	}

	schemas := toSchemas(l.Cfg.Tools)
	maxTurns := l.Cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 40
	}
	remindersLeft := maxFinalizerReminders

	for turn := 0; turn < maxTurns; turn++ {
		select {
		case <-ctx.Done():
			return state, ctx.Err()
		default:
		}

		// Inject a deadline nudge when approaching max turns so the model
		// converges on its final report instead of continuing to explore.
		if remaining := maxTurns - turn; remaining == deadlineNudgeTurns && !l.hasRequiredFinalizer() {
			nudge := message.NewUserText(fmt.Sprintf(
				"⚠ 你还剩 %d 轮就会被强制停止。请立即停止探索，根据已有信息汇总审查结果并调用对应的 report 工具提交最终报告。"+
					"未提交报告将导致本次审查失败。", remaining))
			state = append(state, nudge)
			l.Transcript.Append("message", nudge)
			emitEvent(emit, Event{Kind: EvNotice, Text: fmt.Sprintf("deadline nudge: %d turns remaining", remaining)})
		}

		state = l.maybeCompress(ctx, state, emit)

		emitEvent(emit, Event{Kind: EvTurnStart})

		req := model.Request{
			System:      l.Cfg.System,
			Messages:    compactState(state),
			Tools:       schemas,
			Model:       l.Cfg.Model,
			MaxTokens:   l.Cfg.MaxTokens,
			Temperature: l.Cfg.Temperature,
		}

		llmStart := time.Now()
		assistant, usage, err := l.Client.Stream(ctx, req, func(se model.StreamEvent) {
			switch se.Kind {
			case model.StreamText:
				emitEvent(emit, Event{Kind: EvText, Text: se.Text})
			case model.StreamThinking:
				emitEvent(emit, Event{Kind: EvThinking, Text: se.Text})
			}
		})
		llmDur := time.Since(llmStart)
		if err != nil {
			emitEvent(emit, Event{Kind: EvError, Text: err.Error(), IsError: true, Duration: llmDur})
			return state, fmt.Errorf("model call failed: %w", err)
		}

		state = append(state, assistant)
		l.Transcript.Append("message", assistant)
		emitEvent(emit, Event{Kind: EvAssistant, Text: assistant.Text(), Usage: usage, Duration: llmDur})

		uses := assistant.ToolUses()
		if len(uses) == 0 {
			// Terminal candidate: consult the stop hook.
			if cont, hidden := l.stopHook(remindersLeft); cont {
				remindersLeft--
				emitEvent(emit, Event{Kind: EvNotice, Text: hidden.Text()})
				state = append(state, hidden)
				l.Transcript.Append("message", hidden)
				continue
			}
			if !l.hasRequiredFinalizer() {
				return state, ErrMissingFinalizer
			}
			emitEvent(emit, Event{Kind: EvDone})
			return state, nil
		}

		toolMsg := l.Orch.RunTools(ctx, uses, l.ToolCtx, emit)
		state = append(state, toolMsg)
		l.Transcript.Append("message", toolMsg)
	}

	emitEvent(emit, Event{Kind: EvNotice, Text: fmt.Sprintf("reached max turns (%d); stopping", maxTurns)})
	if !l.hasRequiredFinalizer() {
		return state, ErrMissingFinalizer
	}
	emitEvent(emit, Event{Kind: EvDone})
	return state, nil
}

func (l *Loop) hasRequiredFinalizer() bool {
	if l.ToolCtx == nil || l.ToolCtx.Sink == nil {
		return false
	}
	switch l.Cfg.Mode {
	case permission.ModeReview:
		return l.ToolCtx.Sink.HasFindings()
	case permission.ModeFix:
		return l.ToolCtx.Sink.HasFix()
	default:
		return true
	}
}

// stopHook implements the doc's StopHook: if the required structured report has
// not been emitted, return a hidden user message asking the model to emit it and
// continue the loop. Returns (continue, hiddenMessage).
func (l *Loop) stopHook(remindersLeft int) (bool, message.Message) {
	if remindersLeft <= 0 {
		return false, message.Message{}
	}
	switch l.Cfg.Mode {
	case permission.ModeReview:
		if l.ToolCtx.Sink != nil && l.ToolCtx.Sink.HasFindings() {
			return false, message.Message{}
		}
		return true, message.NewUserText(
			"你还没有提交审查结果就停止了。现在请调用 report_findings，提交有证据绑定的 findings，" +
				"每个 finding 都要包含 file、line、evidence 和 impact。如果没有发现问题，请使用空 findings 数组，" +
				"并说明 reviewed_scope。所有面向人的文本都使用与用户请求相同的自然语言；用户请求是中文时必须使用中文。")
	case permission.ModeFix:
		if l.ToolCtx.Sink != nil && l.ToolCtx.Sink.HasFix() {
			return false, message.Message{}
		}
		return true, message.NewUserText(
			"你还没有提交修复报告就停止了。现在请调用 report_fix，提交 summary、changed_files 和 verification outcomes。 " +
				"如果无法修复，也必须调用 report_fix 并说明 residual_risk。所有面向人的文本都使用与用户请求相同的自然语言；" +
				"用户请求是中文时必须使用中文。")
	}
	return false, message.Message{}
}

// compactState returns a view of the message history where old, large
// tool_result blocks are truncated to a short summary. The most recent
// keepRecent messages are always preserved in full so the model retains
// immediate working memory. This bounds the context growth that otherwise
// scales linearly with turn count (H1).
func compactState(state []message.Message) []message.Message {
	const keepRecent = 6
	const maxOldResult = 1024

	if len(state) <= keepRecent {
		return state
	}

	out := make([]message.Message, len(state))
	cutoff := len(state) - keepRecent
	copy(out[cutoff:], state[cutoff:])

	for i := 0; i < cutoff; i++ {
		m := state[i]
		needsCopy := false
		for _, b := range m.Content {
			if b.Type == message.BlockToolResult && len(b.ResultText) > maxOldResult {
				needsCopy = true
				break
			}
		}
		if !needsCopy {
			out[i] = m
			continue
		}
		blocks := make([]message.Block, len(m.Content))
		for j, b := range m.Content {
			if b.Type == message.BlockToolResult && len(b.ResultText) > maxOldResult {
				b.ResultText = b.ResultText[:maxOldResult] + "\n[... compacted ...]"
			}
			blocks[j] = b
		}
		out[i] = message.Message{Role: m.Role, Content: blocks}
	}
	return out
}

// toSchemas converts tools into provider-facing schemas.
func toSchemas(tools []tool.Tool) []model.ToolSchema {
	out := make([]model.ToolSchema, 0, len(tools))
	for _, t := range tools {
		out = append(out, model.ToolSchema{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return out
}
