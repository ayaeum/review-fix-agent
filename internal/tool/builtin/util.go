// Package builtin implements the agent's built-in tools (read/grep/glob/bash/
// edit/write) plus the review/fix finalizers. Each tool satisfies tool.Tool and
// is mode/permission-gated by the orchestrator, never by the tool itself.
package builtin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxResultBytes = 60 * 1024 // preview cap for tool results

// resolvePath turns a possibly-relative path into an absolute one rooted at cwd.
func resolvePath(cwd, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(cwd, p))
}

// relTo returns path relative to cwd for compact display, falling back to abs.
func relTo(cwd, abs string) string {
	if r, err := filepath.Rel(cwd, abs); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return abs
}

// strInput reads a required string field from decoded tool input.
func strInput(input map[string]any, key string) (string, error) {
	v, ok := input[key]
	if !ok {
		return "", fmt.Errorf("missing required field %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q must be a string", key)
	}
	return s, nil
}

// intInput reads an optional integer field (JSON numbers decode as float64).
func intInput(input map[string]any, key string, def int) int {
	v, ok := input[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return def
}

// truncate caps a string to maxResultBytes with a trailing marker, mirroring the
// doc's "preview large results" guidance.
func truncate(s string) string {
	if len(s) <= maxResultBytes {
		return s
	}
	return s[:maxResultBytes] + fmt.Sprintf("\n\n[... truncated %d bytes; refine your query or read a narrower range ...]", len(s)-maxResultBytes)
}

// modUnix returns the file modification time in unix seconds, or 0.
func modUnix(abs string) int64 {
	fi, err := os.Stat(abs)
	if err != nil {
		return 0
	}
	return fi.ModTime().Unix()
}

// isProbablyBinary reports whether the first chunk of data looks non-textual.
func isProbablyBinary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return true
		}
	}
	return false
}

// skipDir reports whether a directory should be excluded from search/glob.
func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", ".next", "target",
		".idea", ".vscode", "__pycache__", ".venv", "venv", ".cache":
		return true
	}
	return false
}
