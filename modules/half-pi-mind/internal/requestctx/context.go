// Package requestctx 在 Mind 内部传播一次 Face command 的关联标识。
package requestctx

import "context"

type requestIDKey struct{}

// WithRequestID 返回绑定稳定 request ID 的子 context。
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// RequestID 返回 context 中的 request ID；未绑定时返回空字符串。
func RequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	return requestID
}
