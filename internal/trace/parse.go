package trace

import (
	"bufio"
	"encoding/json"
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

// ListSessions parses every *.jsonl transcript in dir (and its immediate
// subdirectories, treated as project names) and returns their metadata,
// newest first.
func ListSessions(dir string) ([]SessionMeta, error) {
	var files []fileWithProject
	// Top-level .jsonl files (no project).
	if top, err := filepath.Glob(filepath.Join(dir, "*.jsonl")); err == nil {
		for _, f := range top {
			files = append(files, fileWithProject{path: f})
		}
	}
	// Subdirectories → project name.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		proj := e.Name()
		sub, err := filepath.Glob(filepath.Join(dir, proj, "*.jsonl"))
		if err != nil {
			continue
		}
		for _, f := range sub {
			files = append(files, fileWithProject{path: f, project: proj})
		}
	}

	metas := make([]SessionMeta, 0, len(files))
	for _, fp := range files {
		d, err := ParseSession(fp.path)
		if err != nil {
			continue
		}
		d.Meta.Project = fp.project
		metas = append(metas, d.Meta)
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].StartedAt > metas[j].StartedAt })
	return metas, nil
}

type fileWithProject struct {
	path    string
	project string
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

	// Parse line-by-line and tolerate a corrupt or partial line (e.g. a crashed
	// write) by skipping it, rather than aborting the whole session — one bad
	// record must not make an otherwise-good transcript unviewable. ReadString
	// has no fixed line-length cap, so large tool_result records are fine.
	reader := bufio.NewReader(f)
	seq := 0
	for {
		line, rerr := reader.ReadString('\n')
		if s := strings.TrimSpace(line); s != "" {
			var raw rawEntry
			if json.Unmarshal([]byte(s), &raw) != nil {
				if rerr != nil {
					break
				}
				continue // skip corrupt line
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
					Kind       string         `json:"kind"`
					Tool       string         `json:"tool"`
					ToolUseID  string         `json:"tool_use_id"`
					Input      map[string]any `json:"input"`
					Text       string         `json:"text"`
					IsError    bool           `json:"is_error"`
					Usage      *Usage         `json:"usage"`
					DurationMS int64          `json:"duration_ms"`
				}
				_ = json.Unmarshal(raw.Payload, &p)
				e.Kind, e.Tool, e.ToolUseID = p.Kind, p.Tool, p.ToolUseID
				e.Input, e.Text, e.IsError, e.Usage = p.Input, p.Text, p.IsError, p.Usage
				e.DurationMS = p.DurationMS
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
		if rerr != nil {
			break
		}
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
