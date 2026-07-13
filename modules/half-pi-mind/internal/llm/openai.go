package llm

import "context"

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

// Chat 发送请求并返回 LLM 响应。
func (p *openaiProvider) Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	return nil, nil
}
