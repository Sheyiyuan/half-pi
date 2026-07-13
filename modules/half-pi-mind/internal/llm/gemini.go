package llm

import "context"

// geminiProvider 适配 Google Gemini API。
type geminiProvider struct {
	APIKey string
	Model  string
}

// NewGemini 创建一个 Gemini provider。
func NewGemini(apiKey, model string) Provider {
	return &geminiProvider{
		APIKey: apiKey,
		Model:  model,
	}
}

func (p *geminiProvider) Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	return nil, nil
}
