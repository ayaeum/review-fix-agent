package trace

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

// Server serves the trace API and the embedded web UI for a sessions directory.
type Server struct {
	Dir string
}

// NewServer builds a trace server rooted at a sessions directory.
func NewServer(dir string) *Server { return &Server{Dir: dir} }

// Handler returns the HTTP handler (API + static UI).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions", s.handleList)
	mux.HandleFunc("GET /api/sessions/{id}/stream", s.handleStream)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleDetail)

	sub, err := fs.Sub(webFS, "web")
	if err == nil {
		mux.Handle("/", http.FileServer(http.FS(sub)))
	}
	return mux
}

// Serve starts the HTTP server (blocking).
func (s *Server) Serve(addr string) error {
	fmt.Printf("rfa trace → http://%s\n", addr)
	fmt.Printf("watching sessions in: %s\n", s.Dir)
	return http.ListenAndServe(addr, s.Handler())
}

func (s *Server) handleList(w http.ResponseWriter, _ *http.Request) {
	metas, err := ListSessions(s.Dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if metas == nil {
		metas = []SessionMeta{}
	}
	writeJSON(w, metas)
}

// resolveSessionFile finds the .jsonl file for a session ID, searching both
// the top-level dir and project subdirectories.
func (s *Server) resolveSessionFile(id string) (string, bool) {
	if strings.Contains(id, "..") || strings.ContainsAny(id, "/\\") {
		return "", false
	}
	base := filepath.Base(id) + ".jsonl"
	// Top-level.
	if f := filepath.Join(s.Dir, base); fileExists(f) {
		return f, true
	}
	// Search subdirectories.
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if f := filepath.Join(s.Dir, e.Name(), base); fileExists(f) {
			return f, true
		}
	}
	return "", false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	file, ok := s.resolveSessionFile(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	detail, err := ParseSession(file)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Derive project from subdirectory relative to s.Dir.
	if rel, err := filepath.Rel(s.Dir, file); err == nil {
		if dir := filepath.Dir(rel); dir != "." {
			detail.Meta.Project = dir
		}
	}
	writeJSON(w, detail)
}

// handleStream serves an SSE stream that tails the JSONL file for a session,
// sending each new line as an event. Existing lines are replayed first, then
// new lines are polled. The stream ends when the session_end record appears
// or the client disconnects.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	file, ok := s.resolveSessionFile(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher.Flush()

	ctx := r.Context()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var offset int64
	seq := 0
	ended := false

	sendLines := func() {
		f, err := os.Open(file)
		if err != nil {
			return
		}
		defer f.Close()

		if offset > 0 {
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				return
			}
		}

		// Only consume newline-terminated lines. The agent writes this file
		// concurrently, so the final line may be a partial write with no trailing
		// newline; emitting it (and advancing past it) would ship truncated JSON
		// and lose the remainder. ReadString also has no fixed line-length cap, so
		// large tool_result records don't break the stream.
		reader := bufio.NewReader(f)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				// Incomplete trailing line: leave offset before it so the next
				// tick re-reads it once fully flushed.
				break
			}
			offset += int64(len(line))
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", seq, line)
			seq++
			// Detect end-of-session by the record's actual type, not a substring of
			// the whole line — message content can legitimately contain the text
			// "session_end" (e.g. reviewing this very codebase), which must not
			// terminate the live stream early.
			var rec struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(line), &rec) == nil && rec.Type == "session_end" {
				ended = true
			}
		}
		flusher.Flush()
	}

	sendLines()
	if ended {
		fmt.Fprintf(w, "event: done\ndata: {}\n\n")
		flusher.Flush()
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendLines()
			if ended {
				fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	w.Header().Set("cache-control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
