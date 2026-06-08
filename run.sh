#!/usr/bin/env bash
#
# Build the latest rfa and run it from your current directory.
#
#   /path/to/review-fix-agent/run.sh review --base main
#   /path/to/review-fix-agent/run.sh fix "config nil deref panics on startup"
#
# It (1) loads local config (your token) from env.openai.sh/.env.local/.env in
# the repo, (2) builds the binary incrementally (usually sub-second), and
# (3) runs rfa in the directory you invoked the script from — so it reviews/fixes
# *your* project, not this repo.
#
# Tip: alias it for convenience:
#   alias rfa="$HOME/study/review-fix-agent/run.sh"
#
# Env:
#   RFA_INSTALL=1   also `go install` to refresh the global `rfa` on PATH
#   RFA_MOCK=1      use the offline mock model (no token / network needed)
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CALLER_PWD="$PWD"

# 1. Load local config (exports OPENAI_API_KEY etc.). Stdout is silenced so a
#    config banner can't pollute rfa output (e.g. --json); errors still show.
for f in env.openai.sh .env.local .env; do
  if [ -f "$REPO/$f" ]; then
    set -a
    # shellcheck disable=SC1090
    . "$REPO/$f" >/dev/null
    set +a
    break
  fi
done

# 2. Build the latest binary.
( cd "$REPO" && go build -o "$REPO/rfa" ./cmd/rfa )

# 3. Optionally refresh the global install so `rfa` on PATH matches.
if [ "${RFA_INSTALL:-0}" = "1" ]; then
  ( cd "$REPO" && go install ./cmd/rfa )
  echo "↻ refreshed global rfa: $(command -v rfa 2>/dev/null || echo "\$GOPATH/bin/rfa")" >&2
fi

# 4. Preflight: warn if no credentials (unless using the offline mock).
if [ "${RFA_MOCK:-0}" != "1" ] && [ -z "${OPENAI_API_KEY:-}" ] && [ -z "${ANTHROPIC_API_KEY:-}" ]; then
  echo "warning: no OPENAI_API_KEY set — put your token in $REPO/env.openai.sh" >&2
fi

# 5. Run rfa in the directory you invoked the script from.
cd "$CALLER_PWD"
exec "$REPO/rfa" "$@"
