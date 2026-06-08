# rfa — Code Review / Code Fix Agent

A focused coding agent that does two things well:

- **Review Mode** — read-only, evidence-based code review. Every finding binds to a file and line and carries concrete evidence and impact. No code is modified.
- **Fix Mode** — applies the *smallest safe patch* to a known issue, then runs existing verification (tests / vet / lint / typecheck) and reports the outcome honestly.

It is built in **Go** with **zero runtime dependencies** (only the standard library) and talks to the Anthropic Messages API. The design follows the architecture in `docs/internals/coding-agent-architecture.md` of the reference project: a single agentic loop organized as an explicit state machine, with the model deciding, the tools acting, the context layer informing, and the runtime guaranteeing protocol correctness.

---

## Quick start

```bash
# build
go build -o rfa ./cmd/rfa

# configure the provider — OpenAI Responses API is the DEFAULT
export OPENAI_API_KEY=...        # gateway token; that's all you need
# defaults: base=https://ai-gw.mjclouds.com  model=gpt-5.5
# override with OPENAI_BASE_URL / OPENAI_MODEL; optional OPENAI_REASONING_EFFORT=medium

# …or use Anthropic instead
#   export RFA_PROVIDER=anthropic
#   export ANTHROPIC_API_KEY=sk-ant-...

# review the uncommitted changes in the current repo
./rfa review

# review a branch against its merge-base with main
./rfa review --base main

# review specific files with a focus
./rfa review --files internal/agent/loop.go "focus on the stop-hook logic"

# fix a known issue (writes within the working dir; --yes auto-approves mutating commands)
./rfa fix --yes "config nil deref panics on startup when the env is absent"

# machine-readable output
./rfa review --json
```

No API key handy? Run the pipeline offline against the built-in mock model:

```bash
RFA_MOCK=1 ./rfa review "smoke test"
```

Exit codes are CI-friendly: `review` exits `1` if any **high**-severity finding exists; `fix` exits `1` if verification did not fully pass; `2` on a run error.

---

## What it does

### Review Mode

```
Input: diff / branch / files
  → collect changed files + surrounding context
  → trace callers / callees / types as needed (read-only)
  → reason about failure paths
  → emit a structured finding only when there is evidence
  → final report (Markdown or JSON)
```

A finding is only reported if the agent can point to a concrete failing path. Style nits are dropped unless they affect correctness, maintainability, or security. Output schema:

```json
{
  "findings": [
    {
      "severity": "high",
      "file": "internal/svc/server.go",
      "line": 42,
      "title": "Nil config dereferenced before guard",
      "evidence": "Start() reads cfg.Port before the nil check added at line 38",
      "impact": "panic on startup when config is absent",
      "suggested_fix": "move the nil guard above the first cfg dereference"
    }
  ],
  "reviewed_scope": ["internal/svc/server.go"],
  "not_reviewed": ["generated/*"],
  "verification": "not run; review-only mode"
}
```

### Fix Mode

```
Input: known issue
  → localize the minimal code region
  → read related context (read before write is enforced)
  → apply the smallest safe patch
  → run existing verification
  → report patch scope, changed files, verification outcome, residual risk
```

Fix Mode never refactors, renames broadly, or fixes unrelated problems it notices — those become *residual risk* in the report. Verification results are a first-class output.

---

## Architecture

A single agentic loop (`internal/agent`) is the center. The model decides; tools act; the context layer informs; the runtime guarantees the message protocol.

```
cmd/rfa                         CLI: arg parsing, streaming UI, report rendering, exit codes
internal/
  agent/                        the runtime
    session.go      SessionEngine   cross-turn shell: assemble tools, perms, context; harvest report
    loop.go         LoopEngine      the single agentic loop + StopHook (enforces the finalizer)
    orchestrator.go ToolOrchestrator concurrency batching + tool_use/tool_result pairing invariant
    events.go                       runtime event stream (UI/SDK/logging)
  model/                        ModelClient
    client.go                       provider-agnostic interface
    anthropic.go                    Anthropic Messages API, streaming SSE aggregation
    openai_responses.go             OpenAI Responses API (wire_api = "responses")
    mock.go                         deterministic offline client for tests / smoke runs
  tool/                         the unified Tool abstraction
    tool.go, registry.go            interface, read-state cache, finalizer sink, registry
    builtin/                        read_file, grep, glob, run_command, edit_file, write_file,
                                    report_findings, report_fix
  permission/                   PermissionEngine
    engine.go                       2-stage model: filter visibility, then validate input
    rules.go                        shell command classifier (read-only / mutating / destructive)
  contextmgr/                   ContextManager
    diff.go                         unified-diff parser + DiffContextCollector
    rulefiles.go                    AGENTS.md / CLAUDE.md / RFA.md layered loading
    context.go, prompts.go          system prompt + scope assembly (collects around the diff)
  review/  fix/  verify/        structured outputs + verification-command detection
  transcript/                   JSONL session persistence (the recovery boundary)
  trace/                        transcript parser + embedded web UI (rfa trace)
```

### Design principles (from the reference doc)

1. Review only reports issues with evidence.
2. Fix prefers the smallest change; no drive-by refactors.
3. The main loop is an explicit state machine.
4. **Tool protocol correctness is guaranteed by the runtime** — every `tool_use` gets exactly one paired `tool_result`, even on unknown tool, validation failure, permission denial, tool error, or panic.
5. All tools share one abstraction; MCP would be just an adapter.
6. Permissions filter *visibility* first, then validate *input* at execution time.
7. Context is collected around the diff/issue, not by scanning the whole repo.
8. Verification results are a first-class output.

### Permission model

| Mode   | Allowed by default                                   | Denied / requires approval                                  |
| ------ | ---------------------------------------------------- | ----------------------------------------------------------- |
| Review | read_file, grep, glob, read-only `run_command` (git diff/log, tests) | edit/write (hidden entirely), mutating/destructive commands |
| Fix    | the above + edit/write **within the working dir**    | out-of-scope writes, destructive commands (rm, git push/commit/reset, sudo) |

Writers are *hidden from the model* in Review Mode (visibility filtering), and re-checked at execution time (defence-in-depth). Destructive shell commands are always blocked; the agent must surface them as residual risk instead.

### Project rules

`rfa` loads project rule files from the repo root down to the working directory, deeper files taking priority: `AGENTS.md`, `CLAUDE.md`, `RFA.md`, `.rfa/rules.md`. Use them to encode review conventions and project-specific constraints.

---

## Trace viewer (debugging)

Every run is written to a JSONL transcript under `.rfa/sessions/`, including runtime events (tool timing, token usage, permission denials, stop-hook nudges). `rfa trace` serves a zero-dependency web UI to observe those traces — live, while a run is in progress, or after the fact.

```bash
rfa trace                       # serve <cwd>/.rfa/sessions on http://127.0.0.1:7777
rfa trace --dir path/.rfa/sessions --port 7777
```

The UI shows a session list (mode, model, turns, tool calls, tokens, running indicator) and, per session, a turn-by-turn timeline: user/assistant messages, each tool call folded into a card with its input, output, duration, and status, plus the final report. It auto-refreshes every 2s, so you can watch an agent work in real time. Open another terminal, start a `review`/`fix`, and refresh the trace page.

API (if you want to build your own view): `GET /api/sessions`, `GET /api/sessions/{id}`.

## Model providers

`rfa` speaks to providers through one `ModelClient` interface (`internal/model`). Select with `--provider` or `RFA_PROVIDER`.

| Provider | Wire protocol | Key env vars |
| --- | --- | --- |
| `openai` **(default)** | **Responses API** (`/v1/responses`), `wire_api = "responses"` | `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL`, `OPENAI_REASONING_EFFORT`, `OPENAI_MAX_OUTPUT_TOKENS` |
| `anthropic` | Messages API, streaming SSE | `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL` |

OpenAI is the default provider, defaulting to `https://ai-gw.mjclouds.com` and `gpt-5.5` — so you only need to export `OPENAI_API_KEY`.

The OpenAI adapter targets the Responses API specifically (not Chat Completions): tools are flat `function` items, the model emits text and `function_call` as separate output items, and tool results are fed back as `function_call_output` items keyed by `call_id`. `OPENAI_BASE_URL` may omit the `/v1` suffix — it is appended automatically. The adapter is tuned for gateways that deviate from the OpenAI spec: it always sends `instructions`, always sends `input` as an array, and omits `max_output_tokens` unless `OPENAI_MAX_OUTPUT_TOKENS` is set. Each provider adapter only handles protocol differences; permissions, context, and the loop are provider-agnostic.

```bash
export OPENAI_API_KEY=...     # base + model already default to the gateway / gpt-5.5
rfa review --base main
```

## Development

```bash
go build ./...        # compile
go test ./...         # run the test suite (no API key required)
go vet ./...          # static checks
gofmt -l .            # formatting
```

The test suite covers the load-bearing logic without a network: the unified-diff parser, the shell-command classifier, the permission engine (review blocks writes; fix scopes writes; destructive blocked), glob matching, the orchestrator pairing invariant and concurrency ordering, the edit tool's read-before-write/staleness guards, and full review/fix sessions driven by the mock model (including the stop-hook nudge).

### Implementation status vs. the roadmap

The reference doc lays out a 6-phase MVP path. This repo implements:

- ✅ **Phase 1 — Review-only agent**: session/loop/model, message schema, streaming, JSONL transcript, read-only tools, structured findings.
- ✅ **Phase 2 — Fix agent**: edit/write, scoped writes, minimal-patch prompting, fix report.
- ✅ **Phase 3 — Verifier**: shell tool, project verification-command detection, verification as first-class output, large-result preview.
- ✅ **Phase 4 — Context scope collector**: diff-hunk parser, changed-file/added-line tracking, rule-file injection, read-state dedup.
- ⏳ **Phase 5 — Controlled subagents** and **Phase 6 — Long-context (compaction, ToolSearch, persistent memory)**: out of scope for this version; the module boundaries are in place to add them.

## License

MIT — see [LICENSE](LICENSE).
