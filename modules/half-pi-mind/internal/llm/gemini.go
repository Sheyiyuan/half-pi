package llm

import "context"

type geminiProvider struct {
	APIKey string
	Model  string
}

func NewGemini(apiKey, model string) Provider {
	return &geminiProvider{
		APIKey: apiKey,
		Model:  model,
	}
}

func (p *geminiProvider) Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	return nil, nil
}
