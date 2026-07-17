package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ── Anthropic 请求体结构 ──

type anthropicRequestBody struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Temperature float32            `json:"temperature,omitempty"`
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

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	return p.parseResponse(respBytes)
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
