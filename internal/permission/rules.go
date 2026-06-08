package permission

import (
	"regexp"
	"strings"
)

// CommandClass categorizes a shell command for permission decisions.
type CommandClass int

const (
	// ClassReadOnly is safe to run in any mode (inspection / verification).
	ClassReadOnly CommandClass = iota
	// ClassMutating writes to the working tree or environment but is not
	// inherently dangerous (e.g. running a formatter). Requires confirmation.
	ClassMutating
	// ClassDestructive is irreversible or outward-facing (delete, push, reset).
	ClassDestructive
)

// readOnlyPrefixes are command leaders that only observe state. Verification
// commands (test/lint/typecheck/build) live here because they don't mutate the
// tree and their results are a first-class output of the agent.
var readOnlyPrefixes = []string{
	"git diff", "git log", "git show", "git status", "git blame", "git ls-files",
	"git rev-parse", "git branch", "git remote -v", "git stash list",
	"ls", "cat", "head", "tail", "wc", "find", "tree", "pwd", "echo", "stat", "nl",
	"grep", "rg", "ag", "fd", "which", "file", "du", "df",
	"sed -n", "awk", "sort", "uniq",
	"go test", "go vet", "go build", "go run", "go list", "gofmt -l", "golangci-lint run",
	"npm test", "npm run test", "yarn test", "pnpm test", "bun test", "bun run test",
	"pytest", "python -m pytest", "go doc", "tsc --noemit", "tsc --noEmit",
	"make test", "make lint", "make check", "make vet",
	"cargo test", "cargo check", "cargo clippy",
}

// destructivePatterns match irreversible or outward-facing actions. These are
// denied outright; the model must surface them as residual risk instead.
var destructivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(^|[;&|]\s*)rm\s+(-[a-zA-Z]*\s+)*`),
	regexp.MustCompile(`\bgit\s+push\b`),
	regexp.MustCompile(`\bgit\s+commit\b`),
	regexp.MustCompile(`\bgit\s+reset\s+--hard\b`),
	regexp.MustCompile(`\bgit\s+clean\b`),
	regexp.MustCompile(`\bgit\s+checkout\s+--\s`),
	regexp.MustCompile(`\bgit\s+restore\b`),
	regexp.MustCompile(`\bgit\s+rebase\b`),
	regexp.MustCompile(`\bgit\s+filter-branch\b`),
	regexp.MustCompile(`>\s*/`), // redirect to absolute path
	regexp.MustCompile(`\bdd\b`),
	regexp.MustCompile(`\bmkfs\b`),
	regexp.MustCompile(`\bchmod\s+-R\b`),
	regexp.MustCompile(`\bchown\s+-R\b`),
	regexp.MustCompile(`\bcurl\b.*\|\s*(sh|bash)\b`),
	regexp.MustCompile(`\bsudo\b`),
	regexp.MustCompile(`\bkill(all)?\b`),
	regexp.MustCompile(`\bnpm\s+publish\b`),
	regexp.MustCompile(`\bshutdown\b|\breboot\b`),
}

// fileRedirect matches output redirection to a file (`> name`, `>> name`), while
// ignoring fd redirections like `2>&1`. Any file-writing redirect makes a
// command mutating even if its leader is read-only.
var fileRedirect = regexp.MustCompile(`(^|[^0-9&])>>?\s*[A-Za-z0-9._/~"'-]`)

// ClassifyCommand returns the safety class of a shell command string. The
// classification is conservative: a command counts as read-only only when every
// segment (split on shell operators) starts with a known read-only prefix and
// performs no file-writing redirection.
func ClassifyCommand(cmd string) CommandClass {
	c := strings.TrimSpace(cmd)
	if hasDestructivePattern(c) {
		return ClassDestructive
	}
	segments := splitShellSegments(c)
	normalized := make([]string, 0, len(segments))
	for _, seg := range segments {
		normalized = append(normalized, normalizeCommandSegment(seg))
	}
	if hasDestructivePattern(strings.Join(normalized, " && ")) {
		return ClassDestructive
	}
	if fileRedirect.MatchString(c) {
		return ClassMutating
	}
	if mutatingFilterCommand(c) {
		return ClassMutating
	}
	allReadOnly := len(segments) > 0
	for _, seg := range segments {
		seg = normalizeCommandSegment(seg)
		if seg == "" {
			continue
		}
		if !hasReadOnlyPrefix(seg) {
			allReadOnly = false
			break
		}
	}
	if allReadOnly {
		return ClassReadOnly
	}
	return ClassMutating
}

func mutatingFilterCommand(cmd string) bool {
	low := strings.ToLower(cmd)
	return strings.Contains(low, "sed -i") || strings.Contains(low, "perl -i")
}

func hasDestructivePattern(cmd string) bool {
	for _, re := range destructivePatterns {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

func normalizeCommandSegment(seg string) string {
	seg = strings.TrimSpace(seg)
	fields := strings.Fields(seg)
	if len(fields) == 0 || strings.ToLower(fields[0]) != "git" {
		return seg
	}

	i := 1
	for i < len(fields) {
		f := fields[i]
		switch {
		case f == "-C" || f == "-c" || f == "--git-dir" || f == "--work-tree" || f == "--namespace":
			if i+1 >= len(fields) {
				return seg
			}
			i += 2
		case strings.HasPrefix(f, "-C") && len(f) > 2:
			i++
		case strings.HasPrefix(f, "-c") && len(f) > 2:
			i++
		case strings.HasPrefix(f, "--git-dir=") || strings.HasPrefix(f, "--work-tree=") || strings.HasPrefix(f, "--namespace="):
			i++
		case f == "-p" || f == "-P" || f == "--paginate" || f == "--no-pager" || f == "--bare" || f == "--literal-pathspecs" || f == "--no-optional-locks":
			i++
		default:
			if strings.HasPrefix(f, "-") {
				return seg
			}
			return "git " + strings.Join(fields[i:], " ")
		}
	}
	return seg
}

func hasReadOnlyPrefix(seg string) bool {
	low := strings.ToLower(seg)
	for _, p := range readOnlyPrefixes {
		if low == p || strings.HasPrefix(low, p+" ") {
			return true
		}
	}
	return false
}

// splitShellSegments breaks a command on top-level pipe/and/or/semicolon
// operators so each sub-command can be classified independently.
func splitShellSegments(cmd string) []string {
	var segs []string
	var cur strings.Builder
	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case ';':
			segs = append(segs, cur.String())
			cur.Reset()
		case '|', '&':
			// collapse "||" and "&&" into one split
			if i+1 < len(runes) && runes[i+1] == r {
				i++
			}
			segs = append(segs, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		segs = append(segs, cur.String())
	}
	return segs
}
