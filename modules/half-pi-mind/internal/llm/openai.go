package llm

import "context"

type openaiProvider struct {
	BaseURL string
	APIKey  string
	Model   string
}

func NewOpenAI(baseURL, apiKey, model string) Provider {
	return &openaiProvider{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
	}
}

func (p *openaiProvider) Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	return nil, nil
}
