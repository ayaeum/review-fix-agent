package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/review-fix-agent/rfa/internal/message"
)

// Anthropic is a ModelClient for the Anthropic Messages API (streaming SSE).
type Anthropic struct {
	APIKey     string
	BaseURL    string // default https://api.anthropic.com
	Version    string // anthropic-version header
	Model      string
	HTTPClient *http.Client
}

// NewAnthropic builds a client from explicit config, applying defaults.
func NewAnthropic(apiKey, baseURL, model string) *Anthropic {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	return &Anthropic{
		APIKey:     apiKey,
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Version:    "2023-06-01",
		Model:      model,
		HTTPClient: &http.Client{},
	}
}

// Name identifies the provider.
func (a *Anthropic) Name() string { return "anthropic" }

// Stream sends the request and consumes the SSE stream into a single assistant
// message, forwarding incremental events to onEvent.
func (a *Anthropic) Stream(ctx context.Context, req Request, onEvent func(StreamEvent)) (message.Message, message.Usage, error) {
	model := req.Model
	if model == "" {
		model = a.Model
	}
	body := map[string]any{
		"model":      model,
		"max_tokens": req.MaxTokens,
		"stream":     true,
		"messages":   convertMessages(req.Messages),
	}
	if req.System != "" {
		body["system"] = req.System
	}
	if len(req.Tools) > 0 {
		body["tools"] = convertTools(req.Tools)
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return message.Message{}, message.Usage{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.BaseURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return message.Message{}, message.Usage{}, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", a.APIKey)
	httpReq.Header.Set("anthropic-version", a.Version)
	httpReq.Header.Set("accept", "text/event-stream")

	resp, err := a.HTTPClient.Do(httpReq)
	if err != nil {
		return message.Message{}, message.Usage{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return message.Message{}, message.Usage{}, fmt.Errorf("anthropic API %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	return aggregateSSE(resp.Body, onEvent)
}

// blockBuilder accumulates a single streaming content block by index.
type blockBuilder struct {
	typ     string
	text    strings.Builder
	id      string
	name    string
	jsonBuf strings.Builder
}

// aggregateSSE parses the SSE stream into one assistant message + usage.
func aggregateSSE(r io.Reader, onEvent func(StreamEvent)) (message.Message, message.Usage, error) {
	builders := map[int]*blockBuilder{}
	var usage message.Usage

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var evt sseEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			continue // tolerate ping/unknown payloads
		}
		switch evt.Type {
		case "message_start":
			if evt.Message != nil {
				usage.Add(message.Usage{
					InputTokens:         evt.Message.Usage.InputTokens,
					OutputTokens:        evt.Message.Usage.OutputTokens,
					CacheReadTokens:     evt.Message.Usage.CacheReadInputTokens,
					CacheCreationTokens: evt.Message.Usage.CacheCreationInputTokens,
				})
			}
		case "content_block_start":
			b := &blockBuilder{typ: evt.ContentBlock.Type, id: evt.ContentBlock.ID, name: evt.ContentBlock.Name}
			if evt.ContentBlock.Text != "" {
				b.text.WriteString(evt.ContentBlock.Text)
			}
			builders[evt.Index] = b
		case "content_block_delta":
			b := builders[evt.Index]
			if b == nil {
				b = &blockBuilder{}
				builders[evt.Index] = b
			}
			switch evt.Delta.Type {
			case "text_delta":
				b.text.WriteString(evt.Delta.Text)
				if onEvent != nil {
					onEvent(StreamEvent{Kind: StreamText, Text: evt.Delta.Text})
				}
			case "thinking_delta":
				b.text.WriteString(evt.Delta.Thinking)
				if onEvent != nil {
					onEvent(StreamEvent{Kind: StreamThinking, Text: evt.Delta.Thinking})
				}
			case "input_json_delta":
				b.jsonBuf.WriteString(evt.Delta.PartialJSON)
				if onEvent != nil {
					onEvent(StreamEvent{Kind: StreamToolInput, Text: evt.Delta.PartialJSON})
				}
			}
		case "message_delta":
			usage.OutputTokens += evt.Usage.OutputTokens
		case "message_stop", "content_block_stop", "ping":
			// no-op
		case "error":
			return message.Message{}, usage, fmt.Errorf("stream error: %s", string(data))
		}
	}
	if err := scanner.Err(); err != nil {
		return message.Message{}, usage, fmt.Errorf("read stream: %w", err)
	}

	msg := message.Message{Role: message.RoleAssistant}
	indices := make([]int, 0, len(builders))
	for i := range builders {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	for _, i := range indices {
		b := builders[i]
		switch b.typ {
		case "text":
			msg.Content = append(msg.Content, message.Block{Type: message.BlockText, Text: b.text.String()})
		case "thinking":
			msg.Content = append(msg.Content, message.Block{Type: message.BlockThinking, Text: b.text.String()})
		case "tool_use":
			input := map[string]any{}
			if s := strings.TrimSpace(b.jsonBuf.String()); s != "" {
				_ = json.Unmarshal([]byte(s), &input)
			}
			msg.Content = append(msg.Content, message.ToolUse(b.id, b.name, input))
		}
	}
	return msg, usage, nil
}

// --- wire types ---

type sseEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage wireUsage `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Text string `json:"text"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage wireUsage `json:"usage"`
}

type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// convertMessages translates internal messages into Anthropic wire content.
func convertMessages(msgs []message.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		content := make([]map[string]any, 0, len(m.Content))
		for _, b := range m.Content {
			switch b.Type {
			case message.BlockText:
				if strings.TrimSpace(b.Text) == "" {
					continue
				}
				content = append(content, map[string]any{"type": "text", "text": b.Text})
			case message.BlockToolUse:
				input := b.Input
				if input == nil {
					input = map[string]any{}
				}
				content = append(content, map[string]any{
					"type": "tool_use", "id": b.ToolUseID, "name": b.ToolName, "input": input,
				})
			case message.BlockToolResult:
				content = append(content, map[string]any{
					"type": "tool_result", "tool_use_id": b.ToolUseID,
					"content": b.ResultText, "is_error": b.IsError,
				})
			}
		}
		if len(content) == 0 {
			continue
		}
		out = append(out, map[string]any{"role": string(m.Role), "content": content})
	}
	return out
}

// convertTools translates internal tool schemas into Anthropic wire tools.
func convertTools(tools []ToolSchema) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name": t.Name, "description": t.Description, "input_schema": t.InputSchema,
		})
	}
	return out
}
