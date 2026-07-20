package agentcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

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

// ChatHooks 接收不依赖 debug 开关的结构化 Chat 生命周期回调。
// 回调必须快速返回，且不得重新进入同一个 Core。
type ChatHooks struct {
	RequestID         string
	TextDelta         func(ChatTextDelta) error
	ResponseCompleted func(ChatResponse) error
	ToolCalled        func(ChatToolCall)
	ToolCompleted     func(ChatToolResult)
}

type chatHooksKey struct{}

// WithChatHooks 返回携带结构化 Chat 回调的子 context。
func WithChatHooks(ctx context.Context, hooks ChatHooks) context.Context {
	return context.WithValue(ctx, chatHooksKey{}, hooks)
}

func chatHooksFromContext(ctx context.Context) ChatHooks {
	hooks, _ := ctx.Value(chatHooksKey{}).(ChatHooks)
	return hooks
}

func toolArgsDigest(args []byte) string {
	sum := sha256.Sum256(args)
	return "sha256:" + hex.EncodeToString(sum[:])
}
