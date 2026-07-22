package lifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ── 类型化的生命周期 Action ──

// MessageAction 是用户消息进入历史前的 Action。
type MessageAction struct {
	Meta    Meta
	Content string
}

// FrozenMessageAction 是冻结后的消息 Action。
type FrozenMessageAction struct {
	Meta          Meta
	ContentLength int
	ContentDigest string
}

// ModelRequestAction 是模型请求发送前的 Action。
type ModelRequestAction struct {
	Meta     Meta
	System   string
	Messages any // []llm.Message，但为避免循环依赖，使用 any
	Tools    any // []llm.ToolDef
}

// FrozenModelRequestAction 是冻结后的模型请求。
type FrozenModelRequestAction struct {
	Meta         Meta
	ProviderID   string
	ModelID      string
	MessageCount int
	SystemLength int
	SystemDigest string
	HasTools     bool
}

// ModelResponseAction 是完整模型响应。
type ModelResponseAction struct {
	Meta         Meta
	Content      string
	HasToolCalls bool
}

// FrozenModelResponseAction 是冻结后的模型响应。
type FrozenModelResponseAction struct {
	Meta          Meta
	ContentLength int
	ContentDigest string
	HasToolCalls  bool
}

// ToolInvocationAction 是工具调用前的完整 Action。
type ToolInvocationAction struct {
	Meta       Meta
	Tool       string
	Args       json.RawMessage
	TargetNode string
	Background bool
}

// FrozenToolInvocation 是冻结并通过校验的工具调用。
// 审批和 Guard 只能读取此结构，不能修改。
type FrozenToolInvocation struct {
	Meta       Meta
	Tool       string
	Args       json.RawMessage
	ArgsDigest string
	TargetNode string
	Background bool
}

// ToolResultAction 是工具执行后的结果。
type ToolResultAction struct {
	Meta    Meta
	Success bool
	Output  string
	Error   string
}

// FrozenToolResultAction 是冻结后的工具结果。
type FrozenToolResultAction struct {
	Meta         Meta
	Success      bool
	OutputLength int
	OutputDigest string
	ErrorCode    string
}

// ── 生命周期事件（观察用） ──

// ChatReceivedEvent 是 chat 已通过 transport 认证的事件。
type ChatReceivedEvent struct {
	Meta
}

// MessageAdmittedEvent 是用户消息已通过输入策略的事件。
type MessageAdmittedEvent struct {
	Meta
	ContentLength int
	ContentDigest string
}

// MessagePersistedEvent 是用户消息已持久化的事件。
type MessagePersistedEvent struct {
	Meta
	ContentLength int
	ContentDigest string
}

// ModelRequestedEvent 是 provider 请求已开始的事件。
type ModelRequestedEvent struct {
	Meta
	ProviderID   string
	ModelID      string
	MessageCount int
}

// ModelDeltaEvent 是模型文本增量事件。
type ModelDeltaEvent struct {
	Meta
	ResponseIndex int
	Delta         string
}

// ModelResponseReceivedEvent 是 provider 完整响应已组装的事件。
type ModelResponseReceivedEvent struct {
	Meta
	ResponseIndex int
	ContentLength int
	ContentDigest string
	HasToolCalls  bool
}

// AssistantDeliveredEvent 是输出已交付的事件。
type AssistantDeliveredEvent struct {
	Meta
	ContentLength int
	ContentDigest string
}

// ToolProposedEvent 是工具调用被提出的事件。
type ToolProposedEvent struct {
	Meta
	Tool       string
	ArgsDigest string
}

// ToolFrozenEvent 是工具参数已冻结的事件。
type ToolFrozenEvent struct {
	Meta
	Tool       string
	ArgsDigest string
	TargetNode string
}

// ToolAuthorizedEvent 是工具已通过安全审查的事件。
type ToolAuthorizedEvent struct {
	Meta
	Tool       string
	ArgsDigest string
	TargetNode string
}

// ToolDeniedEvent 是工具被拒绝的事件。
type ToolDeniedEvent struct {
	Meta
	Tool       string
	ArgsDigest string
	Reason     string
}

// ToolStartedEvent 是工具已开始执行的事件。
type ToolStartedEvent struct {
	Meta
	Tool       string
	ArgsDigest string
	TargetNode string
}

// ToolFinishedEvent 是工具执行终态的事件。
type ToolFinishedEvent struct {
	Meta
	Tool             string
	ArgsDigest       string
	ExecutionOutcome Outcome
	DeliveryOutcome  Outcome
	OutputLength     int
	ErrorCode        string
}

// ChatFinishedEvent 是 Chat 终态的事件。
type ChatFinishedEvent struct {
	Meta
	Outcome Outcome
}

// ── 工具函数 ──

// HashDigest 返回 SHA-256 摘要的规范化表示。
func HashDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// HashString 返回字符串的 SHA-256 摘要。
func HashString(s string) string {
	return HashDigest([]byte(s))
}
