package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/review-fix-agent/rfa/internal/contextmgr"
	"github.com/review-fix-agent/rfa/internal/message"
	"github.com/review-fix-agent/rfa/internal/model"
	"github.com/review-fix-agent/rfa/internal/permission"
	"github.com/review-fix-agent/rfa/internal/tool"
	"github.com/review-fix-agent/rfa/internal/tool/builtin"
	"github.com/review-fix-agent/rfa/internal/transcript"
)

// SessionConfig configures a single review/fix run.
type SessionConfig struct {
	Cwd           string
	Mode          permission.Mode
	Model         string
	MaxTokens     int
	MaxTurns      int
	Temperature   float64
	AutoApprove   bool
	Ask           permission.Asker
	Scope         contextmgr.Scope
	TranscriptDir string // default: <cwd>/.rfa/sessions
}

// Result is the outcome of a session.
type Result struct {
	Messages       []message.Message
	Findings       map[string]any           // report_findings payload (review mode)
	Fix            map[string]any           // report_fix payload (fix mode)
	Changed        []contextmgr.ChangedFile // parsed diff hunks for post-processing
	Diff           string                   // raw diff text for filter post-processing
	TranscriptPath string
}

// Session is the cross-turn shell that owns state and drives the loop, analogous
// to the reference QueryEngine. It assembles tools, permissions, and context,
// then runs the agentic loop and harvests the structured report.
type Session struct {
	Client model.Client
	Cfg    SessionConfig
}

// NewSession builds a session.
func NewSession(client model.Client, cfg SessionConfig) *Session {
	return &Session{Client: client, Cfg: cfg}
}

// Run executes the session end-to-end.
func (s *Session) Run(ctx context.Context, emit func(Event)) (Result, error) {
	cfg := s.Cfg
	cfg.Scope.Mode = cfg.Mode

	// 1. Assemble the per-mode tool pool.
	tools := assembleTools(cfg.Mode)

	// 2. Permission engine, then filter tool visibility (defence-in-depth).
	perm := &permission.Engine{
		Mode:        cfg.Mode,
		Cwd:         cfg.Cwd,
		AutoApprove: cfg.AutoApprove,
		Ask:         cfg.Ask,
	}
	visible := perm.VisibleTools(tools)
	reg := tool.NewRegistry(visible...)

	// 3. Build context around the scope (diff + rules + system state).
	cm := contextmgr.NewManager(cfg.Cwd)
	built, err := cm.Build(ctx, cfg.Scope)
	if err != nil {
		return Result{}, fmt.Errorf("build context: %w", err)
	}

	// 4. Transcript.
	dir := cfg.TranscriptDir
	if dir == "" {
		dir = filepath.Join(cfg.Cwd, ".rfa", "sessions")
	}
	sessionID := fmt.Sprintf("%s-%s", cfg.Mode, time.Now().Format("20060102-150405"))
	ts, err := transcript.New(dir, sessionID)
	if err != nil {
		return Result{}, fmt.Errorf("open transcript: %w", err)
	}
	defer ts.Close()
	ts.Append("session_start", map[string]any{"mode": cfg.Mode, "model": cfg.Model, "provider": s.Client.Name()})

	// 5. Tool execution context.
	sink := tool.NewSink()
	tc := &tool.Context{
		Cwd:       cfg.Cwd,
		Mode:      string(cfg.Mode),
		ReadState: tool.NewReadState(),
		Sink:      sink,
	}

	// 6. Run the loop.
	loop := &Loop{
		Client:  s.Client,
		Orch:    &Orchestrator{Reg: reg, Perm: perm},
		ToolCtx: tc,
		Cfg: Config{
			Model:       cfg.Model,
			MaxTokens:   cfg.MaxTokens,
			Temperature: cfg.Temperature,
			MaxTurns:    cfg.MaxTurns,
			Mode:        cfg.Mode,
			System:      built.System,
			Tools:       reg.All(),
		},
		Transcript: ts,
	}

	// Wrap emit so runtime events are also persisted to the transcript as a
	// trace (tool timing, usage, denials, notices) for the `rfa trace` viewer.
	traceEmit := func(e Event) {
		recordTraceEvent(ts, e)
		if emit != nil {
			emit(e)
		}
	}

	msgs, runErr := loop.Run(ctx, []message.Message{message.NewUserText(built.InitialUser)}, traceEmit)

	res := Result{
		Messages:       msgs,
		Findings:       sink.Findings,
		Fix:            sink.FixR,
		Changed:        built.Changed,
		Diff:           built.Diff,
		TranscriptPath: ts.Path(),
	}
	ts.Append("session_end", map[string]any{"has_findings": sink.HasFindings(), "has_fix": sink.HasFix()})
	return res, runErr
}

// recordTraceEvent persists a runtime event to the transcript as a trace entry.
// Streaming deltas are skipped (the full assistant text lives in the message
// entry); assistant turns record only usage to avoid duplicating text.
func recordTraceEvent(ts *transcript.Store, e Event) {
	switch e.Kind {
	case EvText, EvThinking:
		return // skip high-frequency deltas
	}
	payload := map[string]any{"kind": string(e.Kind)}
	if e.ToolName != "" {
		payload["tool"] = e.ToolName
	}
	if e.ToolUseID != "" {
		payload["tool_use_id"] = e.ToolUseID
	}
	if e.ToolInput != nil {
		payload["input"] = e.ToolInput
	}
	if e.IsError {
		payload["is_error"] = true
	}
	if e.Kind != EvAssistant && e.Text != "" {
		payload["text"] = e.Text
	}
	if e.Usage.InputTokens != 0 || e.Usage.OutputTokens != 0 {
		payload["usage"] = e.Usage
	}
	ts.Append("event", payload)
}

// assembleTools returns the tool set for a mode. Built-ins are passed to the
// registry which sorts and de-dupes them.
func assembleTools(mode permission.Mode) []tool.Tool {
	common := []tool.Tool{
		builtin.ReadTool{},
		builtin.GrepTool{},
		builtin.GlobTool{},
		builtin.BashTool{},
	}
	switch mode {
	case permission.ModeFix:
		return append(common,
			builtin.EditTool{},
			builtin.WriteTool{},
			builtin.ReportFixTool{},
		)
	default: // review
		return append(common, builtin.ReportFindingsTool{})
	}
}
