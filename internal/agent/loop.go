package agent

import (
	"context"
	"fmt"

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

		emitEvent(emit, Event{Kind: EvTurnStart})

		req := model.Request{
			System:      l.Cfg.System,
			Messages:    state,
			Tools:       schemas,
			Model:       l.Cfg.Model,
			MaxTokens:   l.Cfg.MaxTokens,
			Temperature: l.Cfg.Temperature,
		}

		assistant, usage, err := l.Client.Stream(ctx, req, func(se model.StreamEvent) {
			switch se.Kind {
			case model.StreamText:
				emitEvent(emit, Event{Kind: EvText, Text: se.Text})
			case model.StreamThinking:
				emitEvent(emit, Event{Kind: EvThinking, Text: se.Text})
			}
		})
		if err != nil {
			emitEvent(emit, Event{Kind: EvError, Text: err.Error(), IsError: true})
			return state, fmt.Errorf("model call failed: %w", err)
		}

		state = append(state, assistant)
		l.Transcript.Append("message", assistant)
		emitEvent(emit, Event{Kind: EvAssistant, Text: assistant.Text(), Usage: usage})

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
			emitEvent(emit, Event{Kind: EvDone})
			return state, nil
		}

		toolMsg := l.Orch.RunTools(ctx, uses, l.ToolCtx, emit)
		state = append(state, toolMsg)
		l.Transcript.Append("message", toolMsg)
	}

	emitEvent(emit, Event{Kind: EvNotice, Text: fmt.Sprintf("reached max turns (%d); stopping", maxTurns)})
	emitEvent(emit, Event{Kind: EvDone})
	return state, nil
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
			"You stopped without submitting the review. Call report_findings now with your evidence-bound findings " +
				"(file, line, evidence, impact for each). If you found no issues, call it with an empty findings array " +
				"and explain reviewed_scope.")
	case permission.ModeFix:
		if l.ToolCtx.Sink != nil && l.ToolCtx.Sink.HasFix() {
			return false, message.Message{}
		}
		return true, message.NewUserText(
			"You stopped without submitting the fix report. Call report_fix now with summary, changed_files, and " +
				"verification outcomes. If you could not fix the issue, still call it and explain the residual risk.")
	}
	return false, message.Message{}
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
