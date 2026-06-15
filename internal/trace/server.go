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
		mux.Handle("GET /", http.FileServer(http.FS(sub)))
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

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		writeErr(w, http.StatusBadRequest, "invalid session id")
		return
	}
	file := filepath.Join(s.Dir, filepath.Base(id)+".jsonl")
	if _, err := os.Stat(file); err != nil {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	detail, err := ParseSession(file)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, detail)
}

// handleStream serves an SSE stream that tails the JSONL file for a session,
// sending each new line as an event. Existing lines are replayed first, then
// new lines are polled. The stream ends when the session_end record appears
// or the client disconnects.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		writeErr(w, http.StatusBadRequest, "invalid session id")
		return
	}
	file := filepath.Join(s.Dir, filepath.Base(id)+".jsonl")

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

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", seq, line)
			seq++
			if strings.Contains(line, `"session_end"`) {
				ended = true
			}
		}
		pos, _ := f.Seek(0, io.SeekCurrent)
		offset = pos
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
