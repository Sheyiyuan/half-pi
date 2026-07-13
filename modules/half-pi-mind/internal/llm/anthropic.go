package llm

import "context"

type anthropicProvider struct {
	APIKey string
	Model  string
}

func NewAnthropic(apiKey, model string) Provider {
	return &anthropicProvider{
		APIKey: apiKey,
		Model:  model,
	}
}

func (p *anthropicProvider) Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	return nil, nil
}
