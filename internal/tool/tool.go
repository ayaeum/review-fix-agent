// Package tool defines the unified Tool abstraction. Every capability the model
// can invoke — file reads, search, shell, edits, and the review/fix finalizers —
// implements this one interface. MCP tools (not yet wired) would adapt onto the
// same surface, per the architecture doc's "all tools converge on one Tool
// interface" principle.
package tool

import (
	"context"
	"sync"
)

// Result is the outcome of a single tool call.
type Result struct {
	// Text is the model-visible result string placed into the tool_result block.
	Text string
	// IsError marks the tool_result as an error (the pairing is still produced).
	IsError bool
	// Meta carries structured payloads from finalizer tools (report_findings /
	// report_fix) so the loop can detect completion without parsing free text.
	Meta map[string]any
}

// Context is the per-call execution environment handed to every tool.
type Context struct {
	Cwd       string
	Mode      string // "review" | "fix"
	ReadState *ReadState
	Sink      *Sink
	// Progress, if set, receives human-readable progress lines.
	Progress func(string)
}

// Tool is the unified interface implemented by all capabilities.
type Tool interface {
	Name() string
	Description() string
	// InputSchema returns a JSON Schema object describing the tool input. It is
	// sent verbatim to the provider as the tool's input_schema.
	InputSchema() map[string]any
	// ReadOnly reports whether this invocation only observes state.
	ReadOnly(input map[string]any) bool
	// ConcurrencySafe reports whether this invocation can run in a parallel batch
	// alongside other concurrency-safe calls.
	ConcurrencySafe(input map[string]any) bool
	// Validate checks the decoded input before permission/execution.
	Validate(input map[string]any) error
	// Call performs the work. Returning a non-nil error is converted by the
	// orchestrator into an error tool_result so the pairing invariant holds.
	Call(ctx context.Context, input map[string]any, tc *Context) (Result, error)
}

// ReadState tracks which files the agent has read, so the edit tool can require
// a prior read and detect external modifications between read and write.
type ReadState struct {
	mu    sync.Mutex
	files map[string]ReadRecord
}

// ReadRecord remembers the content+mtime observed at read time.
type ReadRecord struct {
	Content string
	ModUnix int64
}

// NewReadState constructs an empty read-state cache.
func NewReadState() *ReadState { return &ReadState{files: map[string]ReadRecord{}} }

// Record stores the latest read observation for a path.
func (r *ReadState) Record(path string, rec ReadRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[path] = rec
}

// Get returns the last read observation and whether one exists.
func (r *ReadState) Get(path string) (ReadRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.files[path]
	return rec, ok
}

// Sink captures finalizer payloads (the structured review/fix report). The loop
// inspects it to decide whether the run may terminate.
type Sink struct {
	mu       sync.Mutex
	Findings map[string]any // report_findings payload, if emitted
	FixR     map[string]any // report_fix payload, if emitted
}

// NewSink constructs an empty finalizer sink.
func NewSink() *Sink { return &Sink{} }

// SetFindings stores the review finalizer payload.
func (s *Sink) SetFindings(m map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Findings = m
}

// SetFix stores the fix finalizer payload.
func (s *Sink) SetFix(m map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FixR = m
}

// HasFindings reports whether a review report has been emitted.
func (s *Sink) HasFindings() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Findings != nil
}

// HasFix reports whether a fix report has been emitted.
func (s *Sink) HasFix() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.FixR != nil
}
