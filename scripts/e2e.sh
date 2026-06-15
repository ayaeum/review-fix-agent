#!/usr/bin/env bash
#
# End-to-end test for rfa. Covers: build, formatting/vet, unit tests, the offline
# mock pipeline, live review + fix against the configured provider, CI exit codes,
# the read-only invariant, and the trace web server (API + UI).
#
# Live tests need a provider token. If OPENAI_API_KEY is unset, env.openai.sh is
# sourced when present; otherwise the live sections are skipped.
#
# Usage: scripts/e2e.sh
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"
TMPLOG="$(mktemp -d)"
trap 'rm -rf "$TMPLOG"' EXIT

PASS=0; FAIL=0; SKIP=0
ok()   { printf '  \033[32m✓\033[0m %s\n' "$1"; PASS=$((PASS+1)); }
bad()  { printf '  \033[31m✗\033[0m %s\n' "$1"; FAIL=$((FAIL+1)); }
skip() { printf '  \033[2m⊘ %s (skipped)\033[0m\n' "$1"; SKIP=$((SKIP+1)); }
hdr()  { printf '\n\033[1m== %s ==\033[0m\n' "$1"; }

newrepo() { # $1=dir
  git -C "$1" init -q
  git -C "$1" config user.email a@b.c
  git -C "$1" config user.name e2e
}

# Load token if available.
if [ -z "${OPENAI_API_KEY:-}" ] && [ -f "$REPO/env.openai.sh" ]; then
  # shellcheck disable=SC1091
  . "$REPO/env.openai.sh" >/dev/null 2>&1 || true
fi
HAVE_KEY=0; [ -n "${OPENAI_API_KEY:-}" ] && HAVE_KEY=1

# ---------------------------------------------------------------------------
hdr "1. Build & static checks"
go build ./... >/dev/null 2>&1 && ok "go build ./..." || bad "go build ./..."
[ -z "$(gofmt -l . 2>/dev/null)" ] && ok "gofmt clean" || bad "gofmt clean"
go vet ./... >/dev/null 2>&1 && ok "go vet ./..." || bad "go vet ./..."

hdr "2. Unit tests"
if go test ./... >"$TMPLOG/unit.log" 2>&1; then
  ok "go test ./... ($(grep -c '^ok' "$TMPLOG/unit.log") packages)"
else
  bad "go test ./..."; tail -8 "$TMPLOG/unit.log"
fi

go build -o "$REPO/rfa" ./cmd/rfa
RFA="$REPO/rfa"

hdr "3. Offline mock pipeline"
M="$(mktemp -d)"; newrepo "$M"
printf 'package main\nfunc main(){}\n' > "$M/a.go"
( cd "$M" && git add -A && git commit -qm base )
printf 'package main\nfunc main(){ _ = []int{1}[3] }\n' > "$M/a.go"
( cd "$M" && RFA_MOCK=1 "$RFA" review --max-turns 4 >"$TMPLOG/mock.log" 2>&1 )
grep -q "review report\|no review report" "$TMPLOG/mock.log" && ok "mock review drives the loop" || bad "mock review"
ls "$M"/.rfa/sessions/*.jsonl >/dev/null 2>&1 && ok "mock writes a transcript" || bad "mock transcript"
rm -rf "$M"

if [ "$HAVE_KEY" = 0 ]; then
  hdr "Live tests"
  skip "live review/fix/trace (no OPENAI_API_KEY)"
  printf '\n\033[1m==== RESULT: %d passed, %d failed, %d skipped ====\033[0m\n' "$PASS" "$FAIL" "$SKIP"
  [ "$FAIL" = 0 ]; exit
fi

# ---------------------------------------------------------------------------
hdr "4. Live REVIEW (provider: ${OPENAI_MODEL:-default})"
R="$(mktemp -d)"; newrepo "$R"
printf 'package main\n\nimport "fmt"\n\nfunc main() {\n\tports := []int{8080}\n\tfmt.Println(ports[0])\n}\n' > "$R/srv.go"
( cd "$R" && git add -A && git commit -qm base )
# inject an out-of-range bug (uncommitted)
printf 'package main\n\nimport "fmt"\n\nfunc main() {\n\tports := []int{8080}\n\tfmt.Println(ports[0])\n\tfmt.Println(ports[5])\n}\n' > "$R/srv.go"
BEFORE="$(cksum "$R/srv.go")"
( cd "$R" && "$RFA" review --quiet --json --max-turns 8 >"$TMPLOG/review.json" 2>"$TMPLOG/review.err" )
RC=$?
grep -q '"severity"' "$TMPLOG/review.json" && ok "review emits findings" || { bad "review emits findings"; tail -3 "$TMPLOG/review.err"; }
grep -q '"line"' "$TMPLOG/review.json" && ok "findings bind to a line" || bad "findings bind to a line"
grep -q 'srv.go' "$TMPLOG/review.json" && ok "finding references the changed file" || bad "finding references file"
[ "$RC" = 1 ] && ok "exit code 1 on high-severity finding" || bad "exit code on high finding (got $RC)"
[ "$BEFORE" = "$(cksum "$R/srv.go")" ] && ok "review did not modify code (read-only)" || bad "review modified code!"
rm -rf "$R"

hdr "5. Live FIX (provider: ${OPENAI_MODEL:-default})"
F="$(mktemp -d)"; newrepo "$F"
printf 'module demo\n\ngo 1.25\n' > "$F/go.mod"
printf 'package demo\n\nfunc Add(a, b int) int { return a - b }\n' > "$F/add.go"
printf 'package demo\n\nimport "testing"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatalf("got %%d want 5", Add(2, 3))\n\t}\n}\n' > "$F/add_test.go"
( cd "$F" && git add -A && git commit -qm base )
( cd "$F" && "$RFA" fix --yes --quiet --max-turns 10 "Add subtracts instead of adds; TestAdd fails" >"$TMPLOG/fix.log" 2>"$TMPLOG/fix.err" )
RC=$?
grep -q 'return a + b' "$F/add.go" && ok "fix applied the minimal patch" || { bad "fix patch"; cat "$F/add.go"; }
( cd "$F" && go test ./... >/dev/null 2>&1 ) && ok "project tests pass after fix" || bad "tests after fix"
grep -qE 'Fix Report|修复报告' "$TMPLOG/fix.log" && ok "fix report emitted" || bad "fix report"
grep -qE 'PASS|通过' "$TMPLOG/fix.log" && ok "verification recorded as passing" || bad "verification recorded"
[ "$RC" = 0 ] && ok "exit code 0 on passing verification" || bad "fix exit code (got $RC)"

hdr "6. Trace server (API + UI)"
PORT=7798
"$RFA" trace --dir "$F/.rfa/sessions" --port "$PORT" >/dev/null 2>&1 &
SRV=$!
curl -s --retry 25 --retry-connrefused --retry-delay 0 "http://127.0.0.1:$PORT/api/sessions" >"$TMPLOG/sessions.json" 2>/dev/null
grep -q '"mode":"fix"' "$TMPLOG/sessions.json" && ok "GET /api/sessions lists the run" || bad "trace list"
grep -q '"tool_calls"' "$TMPLOG/sessions.json" && ok "session meta includes tool_calls" || bad "session meta"
SID="$(grep -o '"id":"[^"]*"' "$TMPLOG/sessions.json" | head -1 | cut -d'"' -f4)"
curl -s "http://127.0.0.1:$PORT/api/sessions/$SID" >"$TMPLOG/detail.json" 2>/dev/null
grep -q '"tool_use"' "$TMPLOG/detail.json" && ok "GET /api/sessions/{id} has tool calls" || bad "trace detail tool calls"
grep -q '"kind":"tool_start"' "$TMPLOG/detail.json" && ok "detail includes tool timing events" || bad "trace timing events"
curl -s "http://127.0.0.1:$PORT/" 2>/dev/null | grep -qi '<!doctype html>' && ok "GET / serves the web UI" || bad "trace UI"
kill "$SRV" 2>/dev/null
wait "$SRV" 2>/dev/null || true
rm -rf "$F"

# ---------------------------------------------------------------------------
printf '\n\033[1m==== RESULT: %d passed, %d failed, %d skipped ====\033[0m\n' "$PASS" "$FAIL" "$SKIP"
[ "$FAIL" = 0 ]
