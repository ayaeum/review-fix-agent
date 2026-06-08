package trace

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// rawEntry is the on-disk transcript line shape.
type rawEntry struct {
	TS      string          `json:"ts"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// ListSessions parses every *.jsonl transcript in dir and returns their
// metadata, newest first.
func ListSessions(dir string) ([]SessionMeta, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	metas := make([]SessionMeta, 0, len(files))
	for _, f := range files {
		d, err := ParseSession(f)
		if err != nil {
			continue // skip unreadable/partial files
		}
		metas = append(metas, d.Meta)
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].StartedAt > metas[j].StartedAt })
	return metas, nil
}

// ParseSession reads and decodes a single transcript file.
func ParseSession(file string) (SessionDetail, error) {
	f, err := os.Open(file)
	if err != nil {
		return SessionDetail{}, err
	}
	defer f.Close()

	id := strings.TrimSuffix(filepath.Base(file), ".jsonl")
	meta := SessionMeta{ID: id, File: file, Running: true}
	var entries []Entry

	dec := json.NewDecoder(f)
	seq := 0
	for {
		var raw rawEntry
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF || (len(entries) > 0 && err == io.ErrUnexpectedEOF) {
				break
			}
			return SessionDetail{}, err
		}
		e := Entry{Seq: seq, TS: raw.TS, Type: raw.Type}
		seq++

		switch raw.Type {
		case "session_start":
			var p struct {
				Mode     string `json:"mode"`
				Model    string `json:"model"`
				Provider string `json:"provider"`
			}
			_ = json.Unmarshal(raw.Payload, &p)
			meta.Mode, meta.Model, meta.Provider = p.Mode, p.Model, p.Provider
			if meta.StartedAt == "" {
				meta.StartedAt = raw.TS
			}
		case "message":
			var p struct {
				Role    string  `json:"role"`
				Content []Block `json:"content"`
			}
			_ = json.Unmarshal(raw.Payload, &p)
			e.Role, e.Blocks = p.Role, p.Content
			if p.Role == "assistant" {
				meta.Turns++
				for _, b := range p.Content {
					if b.Type == "tool_use" {
						meta.ToolCalls++
					}
				}
			}
		case "event":
			var p struct {
				Kind      string         `json:"kind"`
				Tool      string         `json:"tool"`
				ToolUseID string         `json:"tool_use_id"`
				Input     map[string]any `json:"input"`
				Text      string         `json:"text"`
				IsError   bool           `json:"is_error"`
				Usage     *Usage         `json:"usage"`
			}
			_ = json.Unmarshal(raw.Payload, &p)
			e.Kind, e.Tool, e.ToolUseID = p.Kind, p.Tool, p.ToolUseID
			e.Input, e.Text, e.IsError, e.Usage = p.Input, p.Text, p.IsError, p.Usage
			if p.Usage != nil {
				meta.InputTokens += p.Usage.InputTokens
				meta.OutputTokens += p.Usage.OutputTokens
			}
		case "session_end":
			var p struct {
				HasFindings *bool `json:"has_findings"`
				HasFix      *bool `json:"has_fix"`
			}
			_ = json.Unmarshal(raw.Payload, &p)
			meta.HasFindings, meta.HasFix = p.HasFindings, p.HasFix
			meta.EndedAt = raw.TS
			meta.Running = false
		default:
			var extra map[string]any
			_ = json.Unmarshal(raw.Payload, &extra)
			e.Extra = extra
		}

		if meta.StartedAt == "" {
			meta.StartedAt = raw.TS
		}
		entries = append(entries, e)
	}

	meta.DurationMS = durationMS(meta.StartedAt, lastNonEmpty(meta.EndedAt, lastTS(entries)))
	return SessionDetail{Meta: meta, Entries: entries}, nil
}

func lastTS(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[len(entries)-1].TS
}

func lastNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func durationMS(start, end string) int64 {
	if start == "" || end == "" {
		return 0
	}
	ts, err1 := time.Parse(time.RFC3339Nano, start)
	te, err2 := time.Parse(time.RFC3339Nano, end)
	if err1 != nil || err2 != nil {
		return 0
	}
	d := te.Sub(ts).Milliseconds()
	if d < 0 {
		return 0
	}
	return d
}
