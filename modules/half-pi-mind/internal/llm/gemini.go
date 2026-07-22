package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ── Gemini 请求体结构 ──

type geminiRequestBody struct {
	SystemInstruction *geminiContent   `json:"system_instruction,omitempty"`
	Contents          []geminiContent  `json:"contents"`
	Tools             []geminiToolDecl `json:"tools,omitempty"`
	GenerationConfig  *geminiGenConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiFunctionResponse struct {
	ID       string               `json:"id,omitempty"`
	Name     string               `json:"name"`
	Response geminiFunctionResult `json:"response"`
}

type geminiFunctionResult struct {
	Result string `json:"result"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFunctionDecl `json:"functionDeclarations"`
}

type geminiFunctionDecl struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

type geminiGenConfig struct {
	Temperature float32 `json:"temperature,omitempty"`
	MaxTokens   int     `json:"maxOutputTokens,omitempty"`
}

// ── Gemini 响应体结构 ──

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsageMeta  `json:"usageMetadata,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsageMeta struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}

const syntheticGeminiToolIDPrefix = "gemini_noid_"

// ── Provider ──

type geminiProvider struct {
	BaseURL string
	APIKey  string
	Model   string
}

// NewGemini 创建一个 Gemini provider。
func NewGemini(baseURL, apiKey, model string) Provider {
	return &geminiProvider{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
	}
}

// Chat 向 Gemini API 发送请求并返回解析后的响应。
func (p *geminiProvider) Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	body := p.buildRequestBody(req)
	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", p.BaseURL, p.Model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.APIKey)

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

// ChatStream 使用 Gemini streamGenerateContent SSE 接口返回文本和函数调用。
func (p *geminiProvider) ChatStream(ctx context.Context, req *LLMRequest, onDelta TextDeltaFunc) (*LLMResponse, error) {
	body := p.buildRequestBody(req)
	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize request: %w", err)
	}
	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", p.BaseURL, p.Model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-goog-api-key", p.APIKey)
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
	seenTools := make(map[string]ToolCall)
	gotCandidate := false
	err = decodeSSE(resp.Body, func(event sseEvent) error {
		if event.Data == "[DONE]" {
			return nil
		}
		var chunk geminiResponse
		if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
			return fmt.Errorf("failed to parse stream JSON: %w", err)
		}
		if chunk.UsageMetadata != nil {
			result.Usage = Usage{
				InputTokens:  chunk.UsageMetadata.PromptTokenCount,
				OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
			}
		}
		if len(chunk.Candidates) == 0 {
			return nil
		}
		gotCandidate = true
		for _, part := range chunk.Candidates[0].Content.Parts {
			if part.Text != "" {
				if onDelta != nil {
					if err := onDelta(part.Text); err != nil {
						return err
					}
				}
				result.Content += part.Text
			}
			if part.FunctionCall == nil {
				continue
			}
			args := "{}"
			if part.FunctionCall.Args != nil {
				encoded, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return fmt.Errorf("encode Gemini function args: %w", err)
				}
				if len(encoded) > maxStreamToolArgs {
					return fmt.Errorf("Gemini function args exceed %d bytes", maxStreamToolArgs)
				}
				args = string(encoded)
			}
			key := part.FunctionCall.ID
			if key == "" {
				key = part.FunctionCall.Name + "\x00" + args
			}
			call := ToolCall{ID: part.FunctionCall.ID, Name: part.FunctionCall.Name, Args: args}
			if previous, ok := seenTools[key]; ok {
				if previous.Name != call.Name || previous.Args != call.Args {
					return fmt.Errorf("Gemini function call %q changed during stream", key)
				}
				continue
			}
			call.ID = p.toolCallID(part.FunctionCall, len(result.ToolCalls))
			seenTools[key] = call
			result.ToolCalls = append(result.ToolCalls, call)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !gotCandidate {
		return nil, fmt.Errorf("Gemini stream contains no candidates")
	}
	return result, nil
}

func (p *geminiProvider) buildRequestBody(req *LLMRequest) *geminiRequestBody {
	body := &geminiRequestBody{}

	if req.System != "" {
		body.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: req.System}},
		}
	}

	// 角色映射: assistant → "model", tool 结果作为 user 回传。
	roleMap := map[Role]string{
		RoleUser:      "user",
		RoleAssistant: "model",
		RoleTool:      "user",
		RoleSystem:    "user",
	}

	for _, msg := range req.Messages {
		gemRole := roleMap[msg.Role]
		var parts []geminiPart

		switch msg.Role {
		case RoleAssistant:
			if msg.Content != "" {
				parts = append(parts, geminiPart{Text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				var args map[string]any
				if tc.Args != "" {
					json.Unmarshal([]byte(tc.Args), &args)
				}
				if args == nil {
					args = map[string]any{}
				}
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Name,
						Args: args,
					},
				})
			}

		case RoleTool:
			toolName, responseID := p.findToolCall(req.Messages, msg.ToolID)
			parts = append(parts, geminiPart{
				FunctionResponse: &geminiFunctionResponse{
					ID:   responseID,
					Name: toolName,
					Response: geminiFunctionResult{
						Result: msg.Content,
					},
				},
			})

		default:
			parts = append(parts, geminiPart{Text: msg.Content})
		}

		if len(parts) > 0 {
			body.Contents = append(body.Contents, geminiContent{
				Role:  gemRole,
				Parts: parts,
			})
		}
	}

	if len(req.Tools) > 0 {
		decls := make([]geminiFunctionDecl, len(req.Tools))
		for i, tool := range req.Tools {
			decls[i] = geminiFunctionDecl{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			}
		}
		body.Tools = []geminiToolDecl{{FunctionDeclarations: decls}}
	}

	if req.Temperature != 0 || req.MaxTokens != 0 {
		body.GenerationConfig = &geminiGenConfig{
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
		}
	}

	return body
}

// findToolCall 从消息历史中查找 toolID 对应的工具名和真实 Gemini 调用 ID。
func (p *geminiProvider) findToolCall(messages []Message, toolID string) (string, string) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleAssistant {
			for _, tc := range messages[i].ToolCalls {
				if tc.ID == toolID {
					if strings.HasPrefix(tc.ID, syntheticGeminiToolIDPrefix) {
						return tc.Name, ""
					}
					return tc.Name, tc.ID
				}
			}
		}
	}
	return "", ""
}

func (p *geminiProvider) parseResponse(body []byte) (*LLMResponse, error) {
	var resp geminiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response JSON: %w", err)
	}

	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("response contains no candidates")
	}

	candidate := resp.Candidates[0]
	result := &LLMResponse{}

	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			result.Content += part.Text
		}
		if part.FunctionCall != nil {
			argsJSON := "{}"
			if part.FunctionCall.Args != nil {
				if b, err := json.Marshal(part.FunctionCall.Args); err == nil {
					argsJSON = string(b)
				}
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:   p.toolCallID(part.FunctionCall, len(result.ToolCalls)),
				Name: part.FunctionCall.Name,
				Args: argsJSON,
			})
		}
	}

	if resp.UsageMetadata != nil {
		result.Usage = Usage{
			InputTokens:  resp.UsageMetadata.PromptTokenCount,
			OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
		}
	}

	return result, nil
}

func (p *geminiProvider) toolCallID(call *geminiFunctionCall, index int) string {
	if call.ID != "" {
		return call.ID
	}
	return fmt.Sprintf("%s%d_%s", syntheticGeminiToolIDPrefix, index, call.Name)
}
