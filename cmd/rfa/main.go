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
	"strings"
	"syscall"

	"github.com/review-fix-agent/rfa/internal/agent"
	"github.com/review-fix-agent/rfa/internal/contextmgr"
	"github.com/review-fix-agent/rfa/internal/fix"
	"github.com/review-fix-agent/rfa/internal/model"
	"github.com/review-fix-agent/rfa/internal/permission"
	"github.com/review-fix-agent/rfa/internal/review"
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
	filesCSV := fs.String("files", "", "comma-separated focus files")
	modelID := fs.String("model", envOr("RFA_MODEL", ""), "model id (default: provider default)")
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

	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	var scope contextmgr.Scope
	scope.Base = *base
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

	client, err := buildClient(*modelID)
	if err != nil {
		fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sess := agent.NewSession(client, agent.SessionConfig{
		Cwd:         cwd,
		Mode:        mode,
		Model:       *modelID,
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

// buildClient selects the model provider from the environment.
func buildClient(modelID string) (model.Client, error) {
	if os.Getenv("RFA_MOCK") == "1" {
		return &model.Mock{}, nil
	}
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set (or set RFA_MOCK=1 for an offline smoke test)")
	}
	return model.NewAnthropic(key, os.Getenv("ANTHROPIC_BASE_URL"), modelID), nil
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
			if r, err := review.ParseReport(result.Findings); err == nil && r.Counts()["high"] > 0 {
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

Flags:
  --base <ref>       Diff base ref (e.g. main). Default: uncommitted changes vs HEAD.
  --files a,b,c      Comma-separated focus files.
  --model <id>       Model id (default: provider default or $RFA_MODEL).
  --max-turns <n>    Maximum agentic turns (default 40).
  --max-tokens <n>   Max output tokens per model call (default 8192).
  --json             Print the structured report as JSON.
  --yes              Auto-approve mutating commands (fix mode).
  --quiet            Suppress streaming; print only the final report.

Environment:
  ANTHROPIC_API_KEY  API key (required unless RFA_MOCK=1).
  ANTHROPIC_BASE_URL Override API endpoint.
  RFA_MODEL          Default model id.
  RFA_MOCK=1         Use the offline mock model.

Examples:
  rfa review --base main
  rfa review --files internal/agent/loop.go "focus on the stop-hook logic"
  rfa fix --yes "config nil deref panics on startup when env is absent"
`)
}
