// Command rfa is the CLI for the review/fix coding agent.
//
//	rfa review [flags] [focus text...]   # read-only, evidence-based findings
//	rfa fix    [flags] <issue text...>   # minimal patch + verification
//
// Environment:
//
//	ANTHROPIC_API_KEY   API key (required unless RFA_MOCK=1)
//	ANTHROPIC_BASE_URL  override API endpoint (optional)
//	RFA_MODEL           default model id (optional)
//	RFA_MOCK=1          use the offline mock model (smoke test / CI)
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/review-fix-agent/rfa/internal/agent"
	"github.com/review-fix-agent/rfa/internal/contextmgr"
	"github.com/review-fix-agent/rfa/internal/fix"
	"github.com/review-fix-agent/rfa/internal/model"
	"github.com/review-fix-agent/rfa/internal/permission"
	"github.com/review-fix-agent/rfa/internal/review"
	"github.com/review-fix-agent/rfa/internal/trace"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	var mode permission.Mode
	switch sub {
	case "review":
		mode = permission.ModeReview
	case "fix":
		mode = permission.ModeFix
	case "trace":
		runTrace(os.Args[2:])
		return
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", sub)
		usage()
		os.Exit(2)
	}

	fs := flag.NewFlagSet(sub, flag.ExitOnError)
	base := fs.String("base", "", "diff base ref (e.g. main); default shows uncommitted changes vs HEAD")
	commit := fs.String("commit", "", "review a single commit by SHA/ref (review mode only)")
	filesCSV := fs.String("files", "", "comma-separated focus files")
	modelID := fs.String("model", envOr("RFA_MODEL", ""), "model id (default: provider default)")
	provider := fs.String("provider", envOr("RFA_PROVIDER", ""), "model provider: openai | anthropic (default: openai)")
	maxTurns := fs.Int("max-turns", 40, "maximum agentic turns")
	maxTokens := fs.Int("max-tokens", 8192, "max output tokens per model call")
	jsonOut := fs.Bool("json", false, "print the structured report as JSON")
	yes := fs.Bool("yes", false, "auto-approve mutating commands (fix mode)")
	quiet := fs.Bool("quiet", false, "suppress streaming output; print only the final report")
	_ = fs.Parse(os.Args[2:])

	task := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if mode == permission.ModeFix && task == "" {
		fmt.Fprintln(os.Stderr, "fix requires an issue description: rfa fix \"config nil deref crashes startup\"")
		os.Exit(2)
	}
	if *commit != "" {
		if mode != permission.ModeReview {
			fmt.Fprintln(os.Stderr, "--commit is only supported in review mode")
			os.Exit(2)
		}
		if *base != "" {
			fmt.Fprintln(os.Stderr, "--commit and --base are mutually exclusive")
			os.Exit(2)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	var scope contextmgr.Scope
	scope.Base = *base
	scope.Commit = *commit
	if *filesCSV != "" {
		for _, f := range strings.Split(*filesCSV, ",") {
			if f = strings.TrimSpace(f); f != "" {
				scope.Files = append(scope.Files, f)
			}
		}
	}
	if mode == permission.ModeFix {
		scope.Issue = task
	} else {
		scope.Focus = task
	}

	client, activeModel, err := buildClient(*provider, *modelID)
	if err != nil {
		fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sess := agent.NewSession(client, agent.SessionConfig{
		Cwd:         cwd,
		Mode:        mode,
		Model:       activeModel,
		MaxTokens:   *maxTokens,
		MaxTurns:    *maxTurns,
		AutoApprove: *yes,
		Ask:         interactiveAsker(*yes),
		Scope:       scope,
	})

	emit := makeEmitter(*quiet)
	result, runErr := sess.Run(ctx, emit)
	if !*quiet {
		fmt.Fprintln(os.Stderr)
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "\n[run ended with error: %v]\n", runErr)
	}

	printReport(mode, result, *jsonOut)
	fmt.Fprintf(os.Stderr, "\ntranscript: %s\n", result.TranscriptPath)

	os.Exit(exitCode(mode, result, runErr))
}

// runTrace starts the trace web UI server over a sessions directory.
func runTrace(args []string) {
	fs := flag.NewFlagSet("trace", flag.ExitOnError)
	dir := fs.String("dir", "", "sessions directory (default <cwd>/.rfa/sessions)")
	port := fs.Int("port", 7777, "HTTP port")
	host := fs.String("host", "127.0.0.1", "bind address")
	_ = fs.Parse(args)
	dirExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "dir" {
			dirExplicit = true
		}
	})

	d := *dir
	if d == "" {
		cwd, _ := os.Getwd()
		d = filepath.Join(cwd, ".rfa", "sessions")
	}
	if abs, err := filepath.Abs(d); err == nil {
		d = abs
	}
	if dirExplicit {
		info, err := os.Stat(d)
		if err != nil {
			fatal(fmt.Errorf("sessions directory %q: %w", d, err))
		}
		if !info.IsDir() {
			fatal(fmt.Errorf("sessions directory %q is not a directory", d))
		}
	}
	srv := trace.NewServer(d)
	if err := srv.Serve(fmt.Sprintf("%s:%d", *host, *port)); err != nil {
		fatal(err)
	}
}

// Default OpenAI provider settings (OpenAI is the default provider).
const (
	defaultOpenAIBaseURL = "https://ai-gw.mjclouds.com"
	defaultOpenAIModel   = "gpt-5.5"
)

// buildClient selects the model provider from flags/environment.
// buildClient returns the provider client and the resolved model id (so the
// transcript/trace records the real model, not the empty CLI default).
func buildClient(provider, modelID string) (model.Client, string, error) {
	if os.Getenv("RFA_MOCK") == "1" {
		return &model.Mock{}, "mock", nil
	}
	switch strings.ToLower(provider) {
	case "", "openai", "openai-responses": // OpenAI is the default provider
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, "", fmt.Errorf("OPENAI_API_KEY is not set (or set RFA_MOCK=1 for an offline smoke test)")
		}
		base := envOr("OPENAI_BASE_URL", defaultOpenAIBaseURL)
		m := modelID
		if m == "" {
			m = envOr("OPENAI_MODEL", defaultOpenAIModel)
		}
		// Only the Responses wire protocol is implemented (wire_api = "responses").
		if w := os.Getenv("OPENAI_WIRE_API"); w != "" && strings.ToLower(w) != "responses" {
			return nil, "", fmt.Errorf("OPENAI_WIRE_API=%q is not supported; only \"responses\" is implemented", w)
		}
		c := model.NewOpenAIResponses(key, base, m)
		c.ReasoningEffort = os.Getenv("OPENAI_REASONING_EFFORT")
		if n := os.Getenv("OPENAI_MAX_OUTPUT_TOKENS"); n != "" {
			if v, err := strconv.Atoi(n); err == nil {
				c.MaxOutputTokens = v
			}
		}
		return c, c.Model, nil
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, "", fmt.Errorf("ANTHROPIC_API_KEY is not set (or set RFA_MOCK=1 for an offline smoke test)")
		}
		c := model.NewAnthropic(key, os.Getenv("ANTHROPIC_BASE_URL"), modelID)
		return c, c.Model, nil
	default:
		return nil, "", fmt.Errorf("unknown provider %q (use: openai | anthropic)", provider)
	}
}

// makeEmitter returns an event handler that streams progress to the terminal.
func makeEmitter(quiet bool) func(agent.Event) {
	return func(e agent.Event) {
		switch e.Kind {
		case agent.EvText:
			if !quiet {
				fmt.Print(e.Text)
			}
		case agent.EvToolStart:
			if !quiet {
				fmt.Fprintf(os.Stderr, "\n  \033[2m→ %s %s\033[0m\n", e.ToolName, summarizeInput(e.ToolInput))
			}
		case agent.EvToolEnd:
			if !quiet {
				status := "ok"
				if e.IsError {
					status = "error"
				}
				fmt.Fprintf(os.Stderr, "  \033[2m← %s (%s)\033[0m\n", e.ToolName, status)
			}
		case agent.EvToolDenied:
			fmt.Fprintf(os.Stderr, "\n  \033[31m⨯ %s denied: %s\033[0m\n", e.ToolName, e.Text)
		case agent.EvNotice:
			fmt.Fprintf(os.Stderr, "\n  \033[33m• %s\033[0m\n", e.Text)
		case agent.EvError:
			fmt.Fprintf(os.Stderr, "\n  \033[31m! %s\033[0m\n", e.Text)
		}
	}
}

// summarizeInput renders a one-line preview of a tool input for progress lines.
func summarizeInput(in map[string]any) string {
	for _, k := range []string{"path", "command", "pattern"} {
		if v, ok := in[k].(string); ok {
			if len(v) > 80 {
				v = v[:80] + "…"
			}
			return v
		}
	}
	return ""
}

// interactiveAsker prompts on a TTY for mutating-command approval. With autoYes
// or no TTY, it returns nil so the engine uses its headless policy.
func interactiveAsker(autoYes bool) permission.Asker {
	if autoYes {
		return nil
	}
	fi, _ := os.Stdin.Stat()
	if fi == nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return nil // not a terminal: fall back to engine default (deny)
	}
	reader := bufio.NewReader(os.Stdin)
	return func(toolName, summary, reason string) bool {
		fmt.Fprintf(os.Stderr, "\n  \033[33mApprove %s: %s\033[0m (%s) [y/N] ", toolName, summary, reason)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		return line == "y" || line == "yes"
	}
}

// printReport renders the structured report (Markdown by default, JSON on flag).
func printReport(mode permission.Mode, result agent.Result, asJSON bool) {
	fmt.Println()
	switch mode {
	case permission.ModeReview:
		if result.Findings == nil {
			fmt.Println("(no review report was produced)")
			return
		}
		r, err := review.ParseReport(result.Findings)
		if err != nil {
			fmt.Printf("(could not parse review report: %v)\n", err)
			return
		}
		r = r.Filtered()
		if asJSON {
			fmt.Println(r.JSON())
		} else {
			fmt.Println(r.Markdown())
		}
	case permission.ModeFix:
		if result.Fix == nil {
			fmt.Println("(no fix report was produced)")
			return
		}
		r, err := fix.ParseReport(result.Fix)
		if err != nil {
			fmt.Printf("(could not parse fix report: %v)\n", err)
			return
		}
		if asJSON {
			fmt.Println(r.JSON())
		} else {
			fmt.Println(r.Markdown())
		}
	}
}

// exitCode maps the outcome to a process exit code so CI can gate on it:
//
//	review: 1 if any high-severity finding exists
//	fix:    1 if verification did not fully pass
func exitCode(mode permission.Mode, result agent.Result, runErr error) int {
	if runErr != nil {
		return 2
	}
	switch mode {
	case permission.ModeReview:
		if result.Findings != nil {
			if r, err := review.ParseReport(result.Findings); err == nil && r.Filtered().Counts()["high"] > 0 {
				return 1
			}
		}
	case permission.ModeFix:
		if result.Fix != nil {
			if r, err := fix.ParseReport(result.Fix); err == nil && !r.AllPassed() {
				return 1
			}
		}
	}
	return 0
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `rfa — code review / code fix coding agent

Usage:
  rfa review [flags] [focus text...]    Read-only, evidence-based code review
  rfa fix    [flags] <issue text...>    Minimal patch + verification for a known issue
  rfa trace  [--dir d] [--port 7777]    Web UI to observe/debug session traces

Flags:
  --base <ref>       Diff base ref (e.g. main). Default: uncommitted changes vs HEAD.
  --files a,b,c      Comma-separated focus files.
  --provider <p>     Model provider: openai | anthropic (default: openai, or $RFA_PROVIDER).
  --model <id>       Model id (default: provider default or $RFA_MODEL).
  --max-turns <n>    Maximum agentic turns (default 40).
  --max-tokens <n>   Max output tokens per model call (default 8192).
  --json             Print the structured report as JSON.
  --yes              Auto-approve mutating commands (fix mode).
  --quiet            Suppress streaming; print only the final report.

Environment:
  RFA_PROVIDER       Override default provider (openai | anthropic).
  RFA_MODEL          Override model id.
  RFA_MOCK=1         Use the offline mock model.

  OpenAI provider (default; Responses API, wire_api = "responses"):
    OPENAI_API_KEY          API key / gateway token (required).
    OPENAI_BASE_URL         Endpoint base (default https://ai-gw.mjclouds.com; /v1 auto-appended).
    OPENAI_MODEL            Model id (default gpt-5.5).
    OPENAI_REASONING_EFFORT Optional: low | medium | high.
    OPENAI_MAX_OUTPUT_TOKENS Optional; omitted by default (some gateways reject it).

  Anthropic provider (--provider anthropic):
    ANTHROPIC_API_KEY    API key.
    ANTHROPIC_BASE_URL   Override API endpoint.

Examples:
  # Default = OpenAI Responses gateway; just provide the token:
  export OPENAI_API_KEY=...
  rfa review --base main
  rfa review --files internal/agent/loop.go "focus on the stop-hook logic"
  rfa fix --yes "config nil deref panics on startup when env is absent"
`)
}
