// Package llm provides the LLM provider abstraction layer.
// Internal types are the single format used by agentcore;
// each adapter converts to/from its vendor's API format.
package llm

import "context"

// Role represents a message participant.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a single turn in the conversation.
type Message struct {
	Role    Role
	Content string // plain text only (no multimodal)
	ToolID  string // set when Role == RoleTool
}

// ToolDef describes a tool the LLM may call.
type ToolDef struct {
	Name        string
	Description string
	// Parameters is a JSON Schema definition.
	// For now, use *jsonschema.Schema when the schema library is chosen.
	Parameters any
}

// ToolCall is a tool invocation returned by the LLM.
type ToolCall struct {
	ID   string
	Name string
	Args string // JSON string
}

// LLMRequest is the internal request format.
type LLMRequest struct {
	System      string // soul.md content (injected as system prompt)
	Messages    []Message
	Tools       []ToolDef
	Temperature float32
	MaxTokens   int
}

// LLMResponse is the internal response format.
type LLMResponse struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
}

// Usage holds token counts.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Provider sends a request to an LLM and returns a response.
// Each implementation converts to/from its vendor's wire format.
type Provider interface {
	Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error)
}
