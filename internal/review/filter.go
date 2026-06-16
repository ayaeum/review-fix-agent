package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/review-fix-agent/rfa/internal/message"
	"github.com/review-fix-agent/rfa/internal/model"
)

const filterSystemPrompt = `你是一个代码审查质量审核员。你的任务是检查一组审查 finding 是否成立。`

const filterUserTemplate = `以下是一组代码审查 finding（JSON 格式）和对应的 diff。
请逐条检查每个 finding：如果 finding 明显错误（例如引用了不存在的代码、行号完全不在 diff 范围内、
逻辑推断有误、或是纯风格问题被标为 high），将其 id 加入应移除列表。

diff:
%s

findings:
%s

请仅返回一个 JSON 数组，包含应移除的 finding ID（字符串）。如果全部保留，返回空数组 []。
不要使用 markdown 代码块。`

type filterEntry struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Evidence string `json:"evidence"`
}

// FilterFindings uses an LLM call to remove false-positive findings. It returns
// the filtered report and a nil error on success. On a model/client failure it
// returns the original report UNFILTERED together with a non-nil error, so the
// caller can surface that the filter was skipped (the report — and any exit code
// derived from it — still reflects unfiltered findings). A nothing-to-do case
// (no findings or no diff) returns the input with a nil error.
func FilterFindings(ctx context.Context, client model.Client, modelID string, r Report, diff string) (Report, error) {
	if len(r.Findings) == 0 || diff == "" {
		return r, nil
	}

	entries := make([]filterEntry, len(r.Findings))
	for i, f := range r.Findings {
		entries[i] = filterEntry{
			ID:       fmt.Sprintf("f-%d", i),
			File:     f.File,
			Line:     f.Line,
			Severity: f.Severity,
			Title:    f.Title,
			Evidence: f.Evidence,
		}
	}
	findingsJSON, _ := json.Marshal(entries)

	truncDiff := diff
	if len(truncDiff) > 60000 {
		truncDiff = truncDiff[:60000] + "\n...[truncated]"
	}

	userContent := fmt.Sprintf(filterUserTemplate, truncDiff, string(findingsJSON))
	req := model.Request{
		System:    filterSystemPrompt,
		Messages:  []message.Message{message.NewUserText(userContent)},
		Model:     modelID,
		MaxTokens: 1024,
	}

	assistant, _, err := client.Stream(ctx, req, nil)
	if err != nil {
		return r, fmt.Errorf("filter model call failed: %w", err)
	}

	removeIDs := parseFilterIDs(strings.TrimSpace(assistant.Text()), len(r.Findings))
	if len(removeIDs) == 0 {
		return r, nil
	}

	out := Report{
		ReviewedScope: r.ReviewedScope,
		NotReviewed:   r.NotReviewed,
		Verification:  r.Verification,
	}
	for i, f := range r.Findings {
		if _, remove := removeIDs[i]; !remove {
			out.Findings = append(out.Findings, f)
		}
	}
	return out, nil
}

func parseFilterIDs(raw string, total int) map[int]struct{} {
	raw = stripJSONFences(raw)
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil
	}
	result := make(map[int]struct{})
	for _, id := range ids {
		var idx int
		if _, err := fmt.Sscanf(id, "f-%d", &idx); err == nil && idx >= 0 && idx < total {
			result[idx] = struct{}{}
		}
	}
	return result
}

func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
	}
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}
