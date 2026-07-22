package agentcore

// ChatToolCall 是一次脱敏的 Chat 工具调用观察数据。
type ChatToolCall struct {
	Tool       string
	ArgsDigest string
}

// ChatToolResult 是一次 Chat 工具调用的终态观察数据。
type ChatToolResult struct {
	Tool    string
	Success bool
}

// ChatTextDelta 是一次 provider response 中的用户可见文本增量。
type ChatTextDelta struct {
	ResponseIndex int
	Delta         string
}

// ChatResponse 是一次完整 provider response 的可见边界。
type ChatResponse struct {
	ResponseIndex int
	Content       string
	HasToolCalls  bool
}

// ChatTransport 是 Core 与调用方之间显式的流式传输适配器。
// 它只负责当前 Chat 的传输背压和兼容 Face 事件，不是 lifecycle Hook，
// 不能注册安全策略或改变冻结后的 Action。
type ChatTransport struct {
	TextDelta         func(ChatTextDelta) error
	ResponseCompleted func(ChatResponse) error
	ToolCalled        func(ChatToolCall)
	ToolCompleted     func(ChatToolResult)
}
