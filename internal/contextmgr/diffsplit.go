package contextmgr

import "strings"

// SplitDiffByFile splits a unified diff into per-file chunks, each beginning at a
// "diff --git " line. Any preamble before the first such line is returned as its
// own leading chunk (callers that key on a file path simply get "" for it). This
// is the single canonical implementation shared by the agent and review packages.
func SplitDiffByFile(diff string) []string {
	var files []string
	var cur strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") && cur.Len() > 0 {
			files = append(files, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		files = append(files, cur.String())
	}
	return files
}

// DiffChunkPath extracts the new-file path from a single diff chunk's "+++ "
// header, stripping a leading "b/". It returns "" when the chunk has no "+++ "
// line or the new side is /dev/null (a deletion).
func DiffChunkPath(chunk string) string {
	for _, line := range strings.Split(chunk, "\n") {
		if strings.HasPrefix(line, "+++ ") {
			p := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			if strings.HasPrefix(p, "b/") {
				return p[2:]
			}
			if p != "/dev/null" {
				return p
			}
		}
	}
	return ""
}
