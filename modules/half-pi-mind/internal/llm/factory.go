package llm

import "fmt"

// New 根据 adapter 名称创建对应的 Provider。
// adapter 支持 "openai"、"gemini"、"anthropic"。
// endpoint 是 API 基础地址，各适配器自行拼接具体路径。
func New(adapter, endpoint, apiKey, model string) (Provider, error) {
	switch adapter {
	case "openai":
		return NewOpenAI(endpoint, apiKey, model), nil
	case "gemini":
		return NewGemini(endpoint, apiKey, model), nil
	case "anthropic":
		return NewAnthropic(endpoint, apiKey, model), nil
	default:
		return nil, fmt.Errorf("unknown adapter: %s", adapter)
	}
}
