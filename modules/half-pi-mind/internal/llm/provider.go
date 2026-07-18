// Package llm 是 LLM 提供者的抽象层。
// 内部类型是 agentcore 唯一认识的格式；
// 每个适配器负责与对应厂商的 API 格式互转。
package llm

import "context"

// Role 表示对话中的参与者角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 是对话中的一轮。
type Message struct {
	Role      Role
	Content   string     // 纯文本，暂不支持多模态
	ToolID    string     // 当 Role == RoleTool 时设置
	ToolCalls []ToolCall // 当 Role == RoleAssistant 且 LLM 返回了工具调用时设置
}

// ToolDef 描述 LLM 可调用的工具。
type ToolDef struct {
	Name        string
	Description string
	// Parameters 是 JSON Schema 定义。
	// 后续选定了 schema 库后改用 *jsonschema.Schema。
	Parameters any
}

// ToolCall 是 LLM 返回的工具调用。
type ToolCall struct {
	ID   string
	Name string
	Args string // JSON 字符串
}

// LLMRequest 是内部请求格式。
type LLMRequest struct {
	System      string // soul.md 内容（拼成 system prompt）
	Messages    []Message
	Tools       []ToolDef
	Temperature float32
	MaxTokens   int
}

// LLMResponse 是内部响应格式。
type LLMResponse struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
}

// Usage 记录 token 消耗。
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Provider 向 LLM 发请求并返回响应。
// 每个实现负责与对应厂商的 wire format 互转。
type Provider interface {
	Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error)
}

// TextDeltaFunc 接收一段按 provider 顺序产生的用户可见文本。
// 返回错误会立即终止流式请求。
type TextDeltaFunc func(delta string) error

// StreamingProvider 是支持原生增量响应的可选 Provider 能力。
type StreamingProvider interface {
	Provider
	ChatStream(ctx context.Context, req *LLMRequest, onDelta TextDeltaFunc) (*LLMResponse, error)
}

// ChatWithStreaming 优先使用原生流；同步 Provider 成功后产生一个完整文本增量。
func ChatWithStreaming(ctx context.Context, provider Provider, req *LLMRequest, onDelta TextDeltaFunc) (*LLMResponse, error) {
	if streaming, ok := provider.(StreamingProvider); ok {
		return streaming.ChatStream(ctx, req, onDelta)
	}
	response, err := provider.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if response.Content != "" && onDelta != nil {
		if err := onDelta(response.Content); err != nil {
			return nil, err
		}
	}
	return response, nil
}
