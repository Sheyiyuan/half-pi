package lifecycle

import "context"

type hookContextKey struct{}

// WithHookContext 标记正在执行 Hook 的 context。
// Runtime 使用该标记拒绝 Hook 对当前同步调用链的直接重入。
func WithHookContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, hookContextKey{}, true)
}

// IsHookContext 报告当前调用是否位于同步 Hook 执行链中。
func IsHookContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	active, _ := ctx.Value(hookContextKey{}).(bool)
	return active
}
