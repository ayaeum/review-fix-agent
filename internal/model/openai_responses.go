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

// OpenAIResponses is a ModelClient for the OpenAI **Responses API**
// (POST /v1/responses), the wire protocol selected by `wire_api = "responses"`.
// It differs from Chat Completions: tools are flat function items, the model
// emits `function_call` / text as separate output items, and tool results are
// fed back as `function_call_output` items. Works against api.openai.com and
// OpenAI-compatible gateways.
type OpenAIResponses struct {
	APIKey          string
	BaseURL         string // normalized to end at /v1
	Model           string
	ReasoningEffort string // optional: low | medium | high
	// MaxOutputTokens, when > 0, is sent as max_output_tokens. Default 0 (omit):
	// some Responses gateways reject the parameter, so it is opt-in.
	MaxOutputTokens int
	HTTPClient      *http.Client
}

// NewOpenAIResponses builds a client. baseURL may omit the /v1 suffix (it is
// added automatically), so "https://ai-gw.mjclouds.com" works.
func NewOpenAIResponses(apiKey, baseURL, model string) *OpenAIResponses {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1") && !strings.Contains(baseURL, "/v1/") {
		baseURL += "/v1"
	}
	if model == "" {
		model = "gpt-5.5"
	}
	return &OpenAIResponses{
		APIKey:     apiKey,
		BaseURL:    baseURL,
		Model:      model,
		HTTPClient: newStreamingHTTPClient(),
	}
}

// Name identifies the provider.
func (o *OpenAIResponses) Name() string { return "openai-responses" }

// Stream sends the request and aggregates the Responses SSE stream into one
// assistant message.
func (o *OpenAIResponses) Stream(ctx context.Context, req Request, onEvent func(StreamEvent)) (message.Message, message.Usage, error) {
	raw, err := json.Marshal(o.buildBody(req))
	if err != nil {
		return message.Message{}, message.Usage{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/responses", bytes.NewReader(raw))
	if err != nil {
		return message.Message{}, message.Usage{}, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")
	if o.APIKey != "" {
		httpReq.Header.Set("authorization", "Bearer "+o.APIKey)
	}

	resp, err := o.HTTPClient.Do(httpReq)
	if err != nil {
		return message.Message{}, message.Usage{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return message.Message{}, message.Usage{}, fmt.Errorf("openai responses API %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	return aggregateResponsesSSE(resp.Body, onEvent)
}

// buildBody constructs the Responses API request payload. It is split out from
// Stream so the wire shape can be unit-tested. Notes on gateway compatibility:
//   - instructions is always sent (some gateways require it).
//   - input is always an array (some gateways reject a bare string).
//   - max_output_tokens is opt-in (some gateways reject the parameter).
func (o *OpenAIResponses) buildBody(req Request) map[string]any {
	model := req.Model
	if model == "" {
		model = o.Model
	}
	instructions := req.System
	if strings.TrimSpace(instructions) == "" {
		instructions = "你是一个代码审查和代码修复助手。"
	}
	body := map[string]any{
		"model":        model,
		"instructions": instructions,
		"input":        toResponsesInput(req.Messages),
		"stream":       true,
		"store":        false,
	}
	if len(req.Tools) > 0 {
		body["tools"] = toResponsesTools(req.Tools)
	}
	if o.MaxOutputTokens > 0 {
		body["max_output_tokens"] = o.MaxOutputTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if o.ReasoningEffort != "" {
		body["reasoning"] = map[string]any{"effort": o.ReasoningEffort}
	}
	return body
}

// aggregateResponsesSSE parses the Responses SSE stream into one assistant
// message + usage. Final blocks are built from completed output items (reliable),
// while deltas drive the onEvent callback for streaming UI.
func aggregateResponsesSSE(r io.Reader, onEvent func(StreamEvent)) (message.Message, message.Usage, error) {
	blocks := map[int]message.Block{}
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
		var evt respEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "response.output_text.delta":
			if onEvent != nil {
				onEvent(StreamEvent{Kind: StreamText, Text: evt.Delta})
			}
		case "response.reasoning_summary_text.delta":
			if onEvent != nil {
				onEvent(StreamEvent{Kind: StreamThinking, Text: evt.Delta})
			}
		case "response.function_call_arguments.delta":
			if onEvent != nil {
				onEvent(StreamEvent{Kind: StreamToolInput, Text: evt.Delta})
			}
		case "response.output_item.done":
			if evt.Item == nil {
				continue
			}
			switch evt.Item.Type {
			case "message":
				if text := evt.Item.text(); text != "" {
					blocks[evt.OutputIndex] = message.Block{Type: message.BlockText, Text: text}
				}
			case "function_call":
				input := map[string]any{}
				if s := strings.TrimSpace(evt.Item.Arguments); s != "" {
					_ = json.Unmarshal([]byte(s), &input)
				}
				blocks[evt.OutputIndex] = message.ToolUse(evt.Item.CallID, evt.Item.Name, input)
			}
		case "response.completed", "response.incomplete":
			if evt.Response != nil && evt.Response.Usage != nil {
				usage.Add(message.Usage{
					InputTokens:  evt.Response.Usage.InputTokens,
					OutputTokens: evt.Response.Usage.OutputTokens,
				})
			}
		case "response.failed":
			if evt.Response != nil && evt.Response.Error != nil {
				return message.Message{}, usage, fmt.Errorf("response failed: %s", evt.Response.Error.Message)
			}
			return message.Message{}, usage, fmt.Errorf("response failed")
		case "error":
			return message.Message{}, usage, fmt.Errorf("stream error: %s", strings.TrimSpace(evt.Message+" "+data))
		}
	}
	if err := scanner.Err(); err != nil {
		return message.Message{}, usage, fmt.Errorf("read stream: %w", err)
	}

	msg := message.Message{Role: message.RoleAssistant}
	idx := make([]int, 0, len(blocks))
	for i := range blocks {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		msg.Content = append(msg.Content, blocks[i])
	}
	return msg, usage, nil
}

// --- wire types ---

type respEvent struct {
	Type        string    `json:"type"`
	Delta       string    `json:"delta"`
	OutputIndex int       `json:"output_index"`
	Item        *respItem `json:"item"`
	Response    *struct {
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
	Message string `json:"message"`
}

type respItem struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// text concatenates output_text parts of a message item.
func (it *respItem) text() string {
	var b strings.Builder
	for _, c := range it.Content {
		if c.Type == "output_text" || c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// toResponsesInput converts internal messages into Responses API input items.
// An assistant message with both text and tool_use becomes a text message item
// followed by separate function_call items; tool_result blocks become
// function_call_output items keyed by call_id.
func toResponsesInput(msgs []message.Message) []map[string]any {
	var items []map[string]any
	for _, m := range msgs {
		switch m.Role {
		case message.RoleUser:
			var texts []map[string]any
			for _, b := range m.Content {
				switch b.Type {
				case message.BlockToolResult:
					// Emit function_call_output immediately to keep it adjacent
					// to its function_call in the stream order.
					items = append(items, map[string]any{
						"type": "function_call_output", "call_id": b.ToolUseID, "output": b.ResultText,
					})
				case message.BlockText:
					if strings.TrimSpace(b.Text) != "" {
						texts = append(texts, map[string]any{"type": "input_text", "text": b.Text})
					}
				}
			}
			if len(texts) > 0 {
				items = append(items, map[string]any{"role": "user", "content": texts})
			}
		case message.RoleAssistant:
			var texts []map[string]any
			var calls []map[string]any
			for _, b := range m.Content {
				switch b.Type {
				case message.BlockText:
					if strings.TrimSpace(b.Text) != "" {
						texts = append(texts, map[string]any{"type": "output_text", "text": b.Text})
					}
				case message.BlockToolUse:
					args := "{}"
					if b.Input != nil {
						if raw, err := json.Marshal(b.Input); err == nil {
							args = string(raw)
						}
					}
					calls = append(calls, map[string]any{
						"type": "function_call", "call_id": b.ToolUseID, "name": b.ToolName, "arguments": args,
					})
				}
			}
			if len(texts) > 0 {
				items = append(items, map[string]any{"role": "assistant", "content": texts})
			}
			items = append(items, calls...)
		}
	}
	return items
}

// toResponsesTools converts internal tool schemas into Responses API function
// tools (flat shape: type/name/description/parameters at the top level).
func toResponsesTools(tools []ToolSchema) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type":        "function",
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.InputSchema,
			"strict":      false,
		})
	}
	return out
}
