package llm

import "context"

// anthropicProvider 适配 Anthropic Claude API。
type anthropicProvider struct {
	APIKey string
	Model  string
}

// NewAnthropic 创建一个 Anthropic provider。
func NewAnthropic(apiKey, model string) Provider {
	return &anthropicProvider{
		APIKey: apiKey,
		Model:  model,
	}
}

func (p *anthropicProvider) Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	return nil, nil
}
