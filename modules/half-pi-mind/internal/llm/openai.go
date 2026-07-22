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

// ── OpenAI 请求体结构 ──

// openaiRequestBody 对应 POST /chat/completions 的请求体。
type openaiRequestBody struct {
	Model         string               `json:"model"`
	Messages      []openaiMessage      `json:"messages"`
	Tools         []openaiTool         `json:"tools,omitempty"`
	Temperature   float32              `json:"temperature,omitempty"`
	MaxTokens     int                  `json:"max_tokens,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	StreamOptions *openaiStreamOptions `json:"stream_options,omitempty"`
}

type openaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openaiMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
}

type openaiTool struct {
	Type     string         `json:"type"`
	Function openaiFunction `json:"function"`
}

type openaiFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// ── OpenAI 响应体结构 ──

type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
	Usage   *openaiUsage   `json:"usage,omitempty"`
}

type openaiChoice struct {
	Message openaiResponseMessage `json:"message"`
}

type openaiResponseMessage struct {
	Content   string           `json:"content"`
	ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
}

type openaiToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openaiToolCallFunc `json:"function"`
}

type openaiToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type openaiStreamResponse struct {
	Choices []openaiStreamChoice `json:"choices"`
	Usage   *openaiUsage         `json:"usage,omitempty"`
}

type openaiStreamChoice struct {
	Delta openaiStreamDelta `json:"delta"`
}

type openaiStreamDelta struct {
	Content   string                 `json:"content"`
	ToolCalls []openaiStreamToolCall `json:"tool_calls,omitempty"`
}

type openaiStreamToolCall struct {
	Index    int                      `json:"index"`
	ID       string                   `json:"id,omitempty"`
	Function openaiStreamToolCallFunc `json:"function"`
}

type openaiStreamToolCallFunc struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// openaiProvider 适配 OpenAI 兼容 API（deepseek、groq、openrouter 等）。
type openaiProvider struct {
	BaseURL string
	APIKey  string
	Model   string
}

// NewOpenAI 创建一个 OpenAI 兼容的 provider。
func NewOpenAI(baseURL, apiKey, model string) Provider {
	return &openaiProvider{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
	}
}

// Chat 向 OpenAI 兼容 API 发送请求并返回解析后的响应。
func (p *openaiProvider) Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	// 构造请求体
	body := p.buildRequestBody(req)
	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize request: %w", err)
	}

	// 创建 HTTP 请求
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.BaseURL+"/chat/completions", bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)

	// 发送请求
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := readHTTPResponse(resp, req.ResponseByteLimit)
	if err != nil {
		return nil, err
	}

	// 解析响应
	return p.parseResponse(respBytes)
}

// ChatStream 使用 OpenAI compatible SSE 接口返回文本增量并组装工具调用。
func (p *openaiProvider) ChatStream(ctx context.Context, req *LLMRequest, onDelta TextDeltaFunc) (*LLMResponse, error) {
	body := p.buildRequestBody(req)
	body.Stream = true
	body.StreamOptions = &openaiStreamOptions{IncludeUsage: true}
	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/chat/completions", bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
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
	tools := make(map[int]*openaiStreamToolBuilder)
	done := false
	err = decodeSSE(resp.Body, func(event sseEvent) error {
		if event.Data == "[DONE]" {
			done = true
			return nil
		}
		var chunk openaiStreamResponse
		if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
			return fmt.Errorf("failed to parse stream JSON: %w", err)
		}
		if chunk.Usage != nil {
			result.Usage = Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				if onDelta != nil {
					if err := onDelta(choice.Delta.Content); err != nil {
						return err
					}
				}
				result.Content += choice.Delta.Content
			}
			for _, fragment := range choice.Delta.ToolCalls {
				builder := tools[fragment.Index]
				if builder == nil {
					builder = &openaiStreamToolBuilder{}
					tools[fragment.Index] = builder
				}
				if fragment.ID != "" {
					if builder.id != "" && builder.id != fragment.ID {
						return fmt.Errorf("tool call %d changed id", fragment.Index)
					}
					builder.id = fragment.ID
				}
				builder.name.WriteString(fragment.Function.Name)
				builder.args.WriteString(fragment.Function.Arguments)
				if builder.name.Len()+builder.args.Len() > maxStreamToolArgs {
					return fmt.Errorf("tool call %d exceeds %d bytes", fragment.Index, maxStreamToolArgs)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, fmt.Errorf("OpenAI stream ended before [DONE]")
	}
	indices := make([]int, 0, len(tools))
	for index := range tools {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		builder := tools[index]
		if builder.id == "" || builder.name.Len() == 0 {
			return nil, fmt.Errorf("tool call %d is incomplete", index)
		}
		args := builder.args.String()
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			return nil, fmt.Errorf("tool call %d arguments are invalid JSON", index)
		}
		result.ToolCalls = append(result.ToolCalls, ToolCall{ID: builder.id, Name: builder.name.String(), Args: args})
	}
	return result, nil
}

type openaiStreamToolBuilder struct {
	id   string
	name strings.Builder
	args strings.Builder
}

// buildRequestBody 将内部 LLMRequest 转换为 OpenAI 格式的请求体。
func (p *openaiProvider) buildRequestBody(req *LLMRequest) *openaiRequestBody {
	body := &openaiRequestBody{
		Model:       p.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	// system prompt 作为 messages 的第一条
	if req.System != "" {
		body.Messages = append(body.Messages, openaiMessage{
			Role:    "system",
			Content: req.System,
		})
	}

	// 逐条映射对话消息
	for _, msg := range req.Messages {
		om := openaiMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
		if msg.Role == RoleTool {
			om.ToolCallID = msg.ToolID
		}
		if msg.Role == RoleAssistant && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				om.ToolCalls = append(om.ToolCalls, openaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: openaiToolCallFunc{
						Name:      tc.Name,
						Arguments: tc.Args,
					},
				})
			}
		}
		body.Messages = append(body.Messages, om)
	}

	// 映射工具定义
	for _, tool := range req.Tools {
		body.Tools = append(body.Tools, openaiTool{
			Type: "function",
			Function: openaiFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}

	return body
}

// parseResponse 将 OpenAI 格式的响应体解析为内部 LLMResponse。
func (p *openaiProvider) parseResponse(body []byte) (*LLMResponse, error) {
	var resp openaiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response JSON: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("response contains no choices")
	}

	choice := resp.Choices[0]

	result := &LLMResponse{
		Content: choice.Message.Content,
	}

	// 映射工具调用
	for _, tc := range choice.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}

	// 映射 token 用量
	if resp.Usage != nil {
		result.Usage = Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		}
	}

	return result, nil
}
