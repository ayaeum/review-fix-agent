package trace

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSessionFile writes a JSONL transcript matching the on-disk rawEntry shape.
func writeSessionFile(t *testing.T, dir, id string, lines []map[string]any) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			t.Fatal(err)
		}
	}
}

func sampleSession() []map[string]any {
	return []map[string]any{
		{"ts": "2026-06-17T03:00:00Z", "type": "session_start", "payload": map[string]any{"mode": "fix", "model": "mock", "provider": "mock"}},
		{"ts": "2026-06-17T03:00:01Z", "type": "message", "payload": map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": "looking"},
				{"type": "tool_use", "tool_name": "read_file", "tool_use_id": "t1", "input": map[string]any{"path": "a.go"}},
			},
		}},
		{"ts": "2026-06-17T03:00:02Z", "type": "event", "payload": map[string]any{"kind": "tool_start", "tool": "read_file", "tool_use_id": "t1"}},
		{"ts": "2026-06-17T03:00:03Z", "type": "session_end", "payload": map[string]any{"has_fix": true}},
	}
}

func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	writeSessionFile(t, dir, "fix-20260617-030000", sampleSession())
	ts := httptest.NewServer(NewServer(dir).Handler())
	t.Cleanup(ts.Close)
	return ts, "fix-20260617-030000"
}

func getBody(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestServerListSessions(t *testing.T) {
	ts, _ := newTestServer(t)
	code, body := getBody(t, ts.URL+"/api/sessions")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var metas []SessionMeta
	if err := json.Unmarshal([]byte(body), &metas); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 session, got %d", len(metas))
	}
	if metas[0].Mode != "fix" || metas[0].ToolCalls != 1 || metas[0].Turns != 1 {
		t.Errorf("meta = %+v, want mode=fix tool_calls=1 turns=1", metas[0])
	}
}

func TestServerSessionDetail(t *testing.T) {
	ts, id := newTestServer(t)
	code, body := getBody(t, ts.URL+"/api/sessions/"+id)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var detail SessionDetail
	if err := json.Unmarshal([]byte(body), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	foundToolUse := false
	for _, e := range detail.Entries {
		for _, b := range e.Blocks {
			if b.Type == "tool_use" && b.ToolName == "read_file" {
				foundToolUse = true
			}
		}
	}
	if !foundToolUse {
		t.Errorf("detail missing tool_use block:\n%s", body)
	}
}

func TestServerDetailNotFound(t *testing.T) {
	ts, _ := newTestServer(t)
	code, _ := getBody(t, ts.URL+"/api/sessions/does-not-exist")
	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

// TestServerDetailRejectsTraversal ensures the id path cannot escape the dir.
func TestServerDetailRejectsTraversal(t *testing.T) {
	ts, _ := newTestServer(t)
	code, _ := getBody(t, ts.URL+"/api/sessions/..%2f..%2fetc%2fpasswd")
	if code == http.StatusOK {
		t.Errorf("path traversal id was served (status %d)", code)
	}
}

// TestServerStream replays a finished session: it must deliver the records and
// terminate with an event: done (the session_end record is present).
func TestServerStream(t *testing.T) {
	ts, id := newTestServer(t)
	code, body := getBody(t, ts.URL+"/api/sessions/"+id+"/stream")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !strings.Contains(body, `"type":"session_start"`) {
		t.Errorf("stream missing session_start:\n%s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Errorf("finished session stream must end with event: done:\n%s", body)
	}
}

func TestServerServesUI(t *testing.T) {
	ts, _ := newTestServer(t)
	code, body := getBody(t, ts.URL+"/")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !strings.Contains(strings.ToLower(body), "<!doctype html>") {
		t.Errorf("GET / did not serve HTML UI")
	}
}
