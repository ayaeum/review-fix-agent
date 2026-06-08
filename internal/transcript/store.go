// Package transcript persists the session as JSONL. The transcript is the
// recovery boundary for the main conversation, per the architecture doc.
package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store appends newline-delimited JSON entries to a per-session file.
type Store struct {
	mu   sync.Mutex
	f    *os.File
	enc  *json.Encoder
	path string
}

// Entry is one transcript record.
type Entry struct {
	TS      string `json:"ts"`
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

// New opens (creating dirs as needed) a transcript file for a session.
func New(dir, sessionID string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Store{f: f, enc: json.NewEncoder(f), path: path}, nil
}

// Path returns the transcript file path.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Append writes one entry. Safe for concurrent use. Nil store is a no-op.
func (s *Store) Append(typ string, payload any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.enc.Encode(Entry{TS: time.Now().UTC().Format(time.RFC3339Nano), Type: typ, Payload: payload})
}

// Close flushes and closes the underlying file. Nil store is a no-op.
func (s *Store) Close() error {
	if s == nil || s.f == nil {
		return nil
	}
	return s.f.Close()
}
