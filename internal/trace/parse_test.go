package trace

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleTranscript = `{"ts":"2026-06-08T10:00:00.000Z","type":"session_start","payload":{"mode":"review","model":"gpt-5.5","provider":"openai-responses"}}
{"ts":"2026-06-08T10:00:00.100Z","type":"message","payload":{"role":"user","content":[{"type":"text","text":"review this"}]}}
{"ts":"2026-06-08T10:00:01.000Z","type":"event","payload":{"kind":"tool_start","tool":"read_file","tool_use_id":"t1","input":{"path":"a.go"}}}
{"ts":"2026-06-08T10:00:01.250Z","type":"event","payload":{"kind":"tool_end","tool":"read_file","tool_use_id":"t1"}}
{"ts":"2026-06-08T10:00:01.300Z","type":"message","payload":{"role":"assistant","content":[{"type":"text","text":"looking"},{"type":"tool_use","tool_use_id":"t1","tool_name":"read_file","input":{"path":"a.go"}}]}}
{"ts":"2026-06-08T10:00:01.400Z","type":"message","payload":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","result_text":"package a","is_error":false}]}}
{"ts":"2026-06-08T10:00:02.000Z","type":"event","payload":{"kind":"assistant","usage":{"input_tokens":120,"output_tokens":40}}}
{"ts":"2026-06-08T10:00:03.000Z","type":"session_end","payload":{"has_findings":true,"has_fix":false}}
`

func writeSample(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "review-20260608-100000.jsonl"), []byte(sampleTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParseSession(t *testing.T) {
	dir := writeSample(t)
	d, err := ParseSession(filepath.Join(dir, "review-20260608-100000.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	m := d.Meta
	if m.Mode != "review" || m.Model != "gpt-5.5" || m.Provider != "openai-responses" {
		t.Errorf("meta basics wrong: %+v", m)
	}
	if m.Running {
		t.Error("session should be marked done (has session_end)")
	}
	if m.Turns != 1 {
		t.Errorf("turns = %d, want 1", m.Turns)
	}
	if m.ToolCalls != 1 {
		t.Errorf("tool_calls = %d, want 1", m.ToolCalls)
	}
	if m.InputTokens != 120 || m.OutputTokens != 40 {
		t.Errorf("tokens = %d/%d, want 120/40", m.InputTokens, m.OutputTokens)
	}
	if m.HasFindings == nil || !*m.HasFindings {
		t.Error("has_findings should be true")
	}
	if m.DurationMS != 3000 {
		t.Errorf("duration = %dms, want 3000", m.DurationMS)
	}
	if len(d.Entries) != 8 {
		t.Errorf("entries = %d, want 8", len(d.Entries))
	}
}

func TestListSessionsSortedAndRunning(t *testing.T) {
	dir := writeSample(t)
	// A second, still-running session (no session_end), newer timestamp.
	running := `{"ts":"2026-06-08T11:00:00.000Z","type":"session_start","payload":{"mode":"fix","model":"gpt-5.5"}}
`
	if err := os.WriteFile(filepath.Join(dir, "fix-20260608-110000.jsonl"), []byte(running), 0o644); err != nil {
		t.Fatal(err)
	}
	metas, err := ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(metas))
	}
	// Newest (fix, 11:00) first.
	if metas[0].Mode != "fix" || !metas[0].Running {
		t.Errorf("newest session should be the running fix one: %+v", metas[0])
	}
	if metas[1].Mode != "review" || metas[1].Running {
		t.Errorf("second should be the finished review: %+v", metas[1])
	}
}
