package trace

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
