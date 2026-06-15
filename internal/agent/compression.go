package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/review-fix-agent/rfa/internal/message"
	"github.com/review-fix-agent/rfa/internal/model"
)

const (
	compressTokenThreshold = 40000
	keepRecentMessages     = 8
	minCompressZone        = 4
	maxCompressInputChars  = 120000
)

const compressionSystemPrompt = `你是一个上下文压缩助手。将提供的对话历史压缩为简洁摘要。`

const compressionUserTemplate = `以下是代码审查/修复 agent 的对话历史（XML 格式）。请将其压缩为简洁摘要，保留以下关键信息：
1. 已检查的文件和代码区域
2. 发现的关键问题或结论
3. 已执行的修改（如果有）
4. 重要的工具调用结果摘要

直接输出摘要文本，不要使用 markdown 代码块。不超过 2000 字符。

<conversation>
%s
</conversation>`

func estimateTokens(msgs []message.Message) int {
	total := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			switch b.Type {
			case message.BlockText:
				total += len(b.Text)
			case message.BlockToolResult:
				total += len(b.ResultText)
			case message.BlockToolUse:
				total += 100
				for k, v := range b.Input {
					total += len(k) + len(fmt.Sprint(v))
				}
			case message.BlockThinking:
				total += len(b.Text)
			}
		}
	}
	return total / 4
}

func buildCompressXML(msgs []message.Message) string {
	var sb strings.Builder
	for i, m := range msgs {
		fmt.Fprintf(&sb, "<msg id=\"%d\" role=\"%s\">\n", i, m.Role)
		for _, b := range m.Content {
			switch b.Type {
			case message.BlockText:
				text := b.Text
				if len(text) > 2000 {
					text = text[:2000] + "...[truncated]"
				}
				fmt.Fprintf(&sb, "  <text>%s</text>\n", text)
			case message.BlockToolUse:
				fmt.Fprintf(&sb, "  <tool_use name=\"%s\"/>\n", b.ToolName)
			case message.BlockToolResult:
				result := b.ResultText
				if len(result) > 500 {
					result = result[:500] + "...[truncated]"
				}
				fmt.Fprintf(&sb, "  <tool_result>%s</tool_result>\n", result)
			}
		}
		sb.WriteString("</msg>\n")
	}
	return sb.String()
}

func (l *Loop) maybeCompress(ctx context.Context, state []message.Message, emit func(Event)) []message.Message {
	est := estimateTokens(state)
	if est < compressTokenThreshold || len(state) <= 1+keepRecentMessages {
		return state
	}

	cutoff := len(state) - keepRecentMessages
	if cutoff <= 1 {
		return state
	}
	for cutoff > 1 && state[cutoff].Role != message.RoleAssistant {
		cutoff--
	}
	if cutoff <= 1 {
		return state
	}

	compressZone := state[1:cutoff]
	if len(compressZone) < minCompressZone {
		return state
	}

	xml := buildCompressXML(compressZone)
	if len(xml) > maxCompressInputChars {
		xml = xml[:maxCompressInputChars] + "\n...[truncated]"
	}

	summary, err := l.compressViaLLM(ctx, xml)
	if err != nil {
		emitEvent(emit, Event{Kind: EvNotice, Text: fmt.Sprintf("上下文压缩失败: %v", err)})
		return state
	}
	if strings.TrimSpace(summary) == "" {
		return state
	}

	origText := stripPreviousSummary(state[0].Text())
	newInitial := message.NewUserText(origText + "\n\n<previous_context_summary>\n" + summary + "\n</previous_context_summary>")

	rebuilt := make([]message.Message, 0, 1+len(state)-cutoff)
	rebuilt = append(rebuilt, newInitial)
	rebuilt = append(rebuilt, state[cutoff:]...)

	newEst := estimateTokens(rebuilt)
	emitEvent(emit, Event{Kind: EvNotice, Text: fmt.Sprintf("上下文压缩: %d 条消息 → %d 条 (%d→%d est. tokens)", len(state), len(rebuilt), est, newEst)})
	return rebuilt
}

func stripPreviousSummary(text string) string {
	const marker = "\n\n<previous_context_summary>"
	if idx := strings.Index(text, marker); idx >= 0 {
		return text[:idx]
	}
	return text
}

func (l *Loop) compressViaLLM(ctx context.Context, conversationXML string) (string, error) {
	userContent := fmt.Sprintf(compressionUserTemplate, conversationXML)

	req := model.Request{
		System:    compressionSystemPrompt,
		Messages:  []message.Message{message.NewUserText(userContent)},
		Model:     l.Cfg.Model,
		MaxTokens: 2048,
	}

	assistant, _, err := l.Client.Stream(ctx, req, nil)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(assistant.Text()), nil
}
