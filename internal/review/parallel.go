package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/review-fix-agent/rfa/internal/contextmgr"
	"github.com/review-fix-agent/rfa/internal/message"
	"github.com/review-fix-agent/rfa/internal/model"
)

const parallelFileThreshold = 5

const perFileSystemPrompt = `你是一个专业代码审查员。你的任务是审查一个文件的 diff 变更。
请只关注此文件的变更，输出结构化的 findings。

输出格式为 JSON 数组，每个元素包含：
- severity: "high" | "medium" | "low" | "info"
- file: 文件路径
- line: 行号（新文件中的行号）
- title: 问题标题（简短）
- evidence: 具体证据
- impact: 影响说明
- suggested_fix: 建议修复（可选）
- confidence: "high" | "medium" | "low"

如果没有发现问题，返回空数组 []。
只返回 JSON 数组，不要其他文本。`

type ParallelConfig struct {
	Client      model.Client
	ModelID     string
	MaxTokens   int
	Concurrency int
}

func ShouldParallelReview(changed []contextmgr.ChangedFile) bool {
	reviewable := 0
	for _, c := range changed {
		if !c.Binary && c.Path() != "" {
			reviewable++
		}
	}
	return reviewable >= parallelFileThreshold
}

func ParallelReview(ctx context.Context, cfg ParallelConfig, changed []contextmgr.ChangedFile, diffByFile map[string]string, focus string) Report {
	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	type fileResult struct {
		findings []Finding
		file     string
	}

	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	var allFindings []Finding

	var wg sync.WaitGroup
	for _, cf := range changed {
		if cf.Binary {
			continue
		}
		path := cf.Path()
		fileDiff, ok := diffByFile[path]
		if !ok || strings.TrimSpace(fileDiff) == "" {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(p, d string) {
			defer wg.Done()
			defer func() { <-sem }()

			findings := reviewSingleFile(ctx, cfg, p, d, focus)
			if len(findings) > 0 {
				mu.Lock()
				allFindings = append(allFindings, findings...)
				mu.Unlock()
			}
		}(path, fileDiff)
	}
	wg.Wait()

	return Report{
		Findings:      allFindings,
		ReviewedScope: reviewedPaths(changed),
	}
}

func reviewSingleFile(ctx context.Context, cfg ParallelConfig, path, fileDiff, focus string) []Finding {
	var userMsg strings.Builder
	fmt.Fprintf(&userMsg, "## 文件: %s\n\n", path)
	if focus != "" {
		fmt.Fprintf(&userMsg, "审查关注点: %s\n\n", focus)
	}
	userMsg.WriteString("```diff\n")
	userMsg.WriteString(fileDiff)
	userMsg.WriteString("\n```\n")

	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	req := model.Request{
		System: perFileSystemPrompt,
		Messages: []message.Message{
			message.NewUserText(userMsg.String()),
		},
		Model:     cfg.ModelID,
		MaxTokens: maxTokens,
	}

	resp, _, err := cfg.Client.Stream(ctx, req, func(model.StreamEvent) {})
	if err != nil {
		return nil
	}

	return parseFileFindings(resp.Text(), path)
}

func parseFileFindings(text, filePath string) []Finding {
	text = strings.TrimSpace(text)
	text = stripJSONFences(text)

	var raw []struct {
		Severity     string `json:"severity"`
		File         string `json:"file"`
		Line         int    `json:"line"`
		Title        string `json:"title"`
		Evidence     string `json:"evidence"`
		Impact       string `json:"impact"`
		SuggestedFix string `json:"suggested_fix"`
		Confidence   string `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil
	}

	var out []Finding
	for _, r := range raw {
		f := Finding{
			Severity:     r.Severity,
			File:         r.File,
			Line:         r.Line,
			Title:        r.Title,
			Evidence:     r.Evidence,
			Impact:       r.Impact,
			SuggestedFix: r.SuggestedFix,
			Confidence:   r.Confidence,
		}
		if f.File == "" {
			f.File = filePath
		}
		if f.Severity == "" {
			f.Severity = "info"
		}
		if f.Line < 1 {
			f.Line = 1
		}
		if f.Title == "" || f.Evidence == "" || f.Impact == "" {
			continue
		}
		out = append(out, f)
	}
	return out
}

func reviewedPaths(changed []contextmgr.ChangedFile) []string {
	var out []string
	for _, c := range changed {
		if !c.Binary {
			if p := c.Path(); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func BuildDiffByFile(changed []contextmgr.ChangedFile, fullDiff string) map[string]string {
	chunks := splitDiffByFile(fullDiff)
	byFile := make(map[string]string, len(chunks))
	for _, chunk := range chunks {
		path := extractPathFromDiffChunk(chunk)
		if path != "" {
			byFile[path] = chunk
		}
	}
	for _, c := range changed {
		p := c.Path()
		if _, ok := byFile[p]; ok {
			continue
		}
		if p != "" && !c.Binary {
			byFile[p] = renderChangedFile(c)
		}
	}
	return byFile
}

func splitDiffByFile(diff string) []string {
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

func extractPathFromDiffChunk(chunk string) string {
	for _, line := range strings.Split(chunk, "\n") {
		if strings.HasPrefix(line, "+++ ") {
			p := strings.TrimPrefix(line, "+++ ")
			p = strings.TrimSpace(p)
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

func renderChangedFile(c contextmgr.ChangedFile) string {
	var b strings.Builder
	for _, h := range c.Hunks {
		b.WriteString(h.Header)
		b.WriteByte('\n')
		for _, l := range h.Lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
