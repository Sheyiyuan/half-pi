package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// ── Anthropic 请求体结构 ──

type anthropicRequestBody struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Temperature float32            `json:"temperature,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// ── Anthropic 响应体结构 ──

type anthropicResponse struct {
	Type    string           `json:"type"`
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
	Usage   *anthropicUsage  `json:"usage,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicStreamEvent struct {
	Type         string                  `json:"type"`
	Index        int                     `json:"index,omitempty"`
	Message      *anthropicStreamMessage `json:"message,omitempty"`
	ContentBlock *anthropicBlock         `json:"content_block,omitempty"`
	Delta        *anthropicStreamDelta   `json:"delta,omitempty"`
	Usage        *anthropicUsage         `json:"usage,omitempty"`
	Error        *anthropicStreamError   `json:"error,omitempty"`
}

type anthropicStreamMessage struct {
	Usage *anthropicUsage `json:"usage,omitempty"`
}

type anthropicStreamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type anthropicStreamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ── Provider ──

type anthropicProvider struct {
	BaseURL string
	APIKey  string
	Model   string
}

// NewAnthropic 创建一个 Anthropic provider。
func NewAnthropic(baseURL, apiKey, model string) Provider {
	return &anthropicProvider{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
	}
}

// Chat 向 Anthropic API 发送请求并返回解析后的响应。
func (p *anthropicProvider) Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	body := p.buildRequestBody(req)
	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.BaseURL+"/v1/messages", bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := readHTTPResponse(resp, req.ResponseByteLimit)
	if err != nil {
		return nil, err
	}

	return p.parseResponse(respBytes)
}

// ChatStream 使用 Anthropic Messages SSE 接口返回可见文本并组装 tool_use。
func (p *anthropicProvider) ChatStream(ctx context.Context, req *LLMRequest, onDelta TextDeltaFunc) (*LLMResponse, error) {
	body := p.buildRequestBody(req)
	body.Stream = true
	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/v1/messages", bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", p.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, err := readHTTPResponse(resp, 0)
		return nil, err
	}

	result := &LLMResponse{}
	tools := make(map[int]*anthropicStreamToolBuilder)
	stopped := false
	err = decodeSSE(resp.Body, func(raw sseEvent) error {
		var event anthropicStreamEvent
		if err := json.Unmarshal([]byte(raw.Data), &event); err != nil {
			return fmt.Errorf("failed to parse stream JSON: %w", err)
		}
		if event.Type == "" {
			event.Type = raw.Type
		}
		switch event.Type {
		case "message_start":
			if event.Message != nil && event.Message.Usage != nil {
				result.Usage.InputTokens = event.Message.Usage.InputTokens
				result.Usage.OutputTokens = event.Message.Usage.OutputTokens
			}
		case "content_block_start":
			if event.ContentBlock == nil || event.ContentBlock.Type != "tool_use" {
				return nil
			}
			builder := &anthropicStreamToolBuilder{id: event.ContentBlock.ID, name: event.ContentBlock.Name}
			if len(event.ContentBlock.Input) > 0 {
				encoded, err := json.Marshal(event.ContentBlock.Input)
				if err != nil {
					return fmt.Errorf("encode initial tool input: %w", err)
				}
				builder.initialArgs = string(encoded)
			}
			tools[event.Index] = builder
		case "content_block_delta":
			if event.Delta == nil {
				return nil
			}
			switch event.Delta.Type {
			case "text_delta":
				if event.Delta.Text == "" {
					return nil
				}
				if onDelta != nil {
					if err := onDelta(event.Delta.Text); err != nil {
						return err
					}
				}
				result.Content += event.Delta.Text
			case "input_json_delta":
				builder := tools[event.Index]
				if builder == nil {
					return fmt.Errorf("tool input delta for unknown block %d", event.Index)
				}
				builder.sawDelta = true
				builder.args.WriteString(event.Delta.PartialJSON)
				if builder.args.Len() > maxStreamToolArgs {
					return fmt.Errorf("tool call %d exceeds %d bytes", event.Index, maxStreamToolArgs)
				}
			}
		case "message_delta":
			if event.Usage != nil {
				if event.Usage.InputTokens != 0 {
					result.Usage.InputTokens = event.Usage.InputTokens
				}
				result.Usage.OutputTokens = event.Usage.OutputTokens
			}
		case "message_stop":
			stopped = true
		case "error":
			if event.Error == nil {
				return fmt.Errorf("Anthropic stream returned an error")
			}
			return fmt.Errorf("Anthropic stream error %s: %s", event.Error.Type, event.Error.Message)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !stopped {
		return nil, fmt.Errorf("Anthropic stream ended before message_stop")
	}
	indices := make([]int, 0, len(tools))
	for index := range tools {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		builder := tools[index]
		if builder.id == "" || builder.name == "" {
			return nil, fmt.Errorf("tool call %d is incomplete", index)
		}
		args := builder.initialArgs
		if builder.sawDelta {
			args = builder.args.String()
		}
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			return nil, fmt.Errorf("tool call %d arguments are invalid JSON", index)
		}
		result.ToolCalls = append(result.ToolCalls, ToolCall{ID: builder.id, Name: builder.name, Args: args})
	}
	return result, nil
}

type anthropicStreamToolBuilder struct {
	id          string
	name        string
	initialArgs string
	sawDelta    bool
	args        strings.Builder
}

func (p *anthropicProvider) buildRequestBody(req *LLMRequest) *anthropicRequestBody {
	body := &anthropicRequestBody{
		Model:       p.Model,
		MaxTokens:   req.MaxTokens,
		System:      req.System,
		Temperature: req.Temperature,
	}

	if body.MaxTokens <= 0 {
		body.MaxTokens = 4096
	}

	for i := 0; i < len(req.Messages); i++ {
		msg := req.Messages[i]
		var blocks []anthropicBlock

		switch msg.Role {
		case RoleAssistant:
			if msg.Content != "" {
				blocks = append(blocks, anthropicBlock{
					Type: "text",
					Text: msg.Content,
				})
			}
			for _, tc := range msg.ToolCalls {
				var input map[string]any
				if tc.Args != "" {
					json.Unmarshal([]byte(tc.Args), &input)
				}
				if input == nil {
					input = map[string]any{}
				}
				blocks = append(blocks, anthropicBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}

		case RoleTool:
			for ; i < len(req.Messages) && req.Messages[i].Role == RoleTool; i++ {
				toolMsg := req.Messages[i]
				blocks = append(blocks, anthropicBlock{
					Type:      "tool_result",
					ToolUseID: toolMsg.ToolID,
					Content:   toolMsg.Content,
				})
			}
			i--

		default:
			blocks = append(blocks, anthropicBlock{
				Type: "text",
				Text: msg.Content,
			})
		}

		if len(blocks) > 0 {
			role := "user"
			if msg.Role == RoleAssistant {
				role = "assistant"
			}
			body.Messages = append(body.Messages, anthropicMessage{
				Role:    role,
				Content: blocks,
			})
		}
	}

	for _, tool := range req.Tools {
		body.Tools = append(body.Tools, anthropicTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.Parameters,
		})
	}

	return body
}

func (p *anthropicProvider) parseResponse(bodyData []byte) (*LLMResponse, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(bodyData, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response JSON: %w", err)
	}

	result := &LLMResponse{}

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "tool_use":
			argsJSON := "{}"
			if block.Input != nil {
				if b, err := json.Marshal(block.Input); err == nil {
					argsJSON = string(b)
				}
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:   block.ID,
				Name: block.Name,
				Args: argsJSON,
			})
		}
	}

	if resp.Usage != nil {
		result.Usage = Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		}
	}

	return result, nil
}
