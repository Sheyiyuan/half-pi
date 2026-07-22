// Package requestctx 在 Mind 内部传播一次 Face command 的关联标识。
package requestctx

import "context"

type requestIDKey struct{}
type principalIDKey struct{}
type sourceKey struct{}

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

// WithPrincipalID 返回绑定已认证主体 ID 的子 context。
func WithPrincipalID(ctx context.Context, principalID string) context.Context {
	if principalID == "" {
		return ctx
	}
	return context.WithValue(ctx, principalIDKey{}, principalID)
}

// PrincipalID 返回 context 中的已认证主体 ID。
func PrincipalID(ctx context.Context) string {
	principalID, _ := ctx.Value(principalIDKey{}).(string)
	return principalID
}

// WithSource 返回绑定可信入口类型的子 context。
func WithSource(ctx context.Context, source string) context.Context {
	if source == "" {
		return ctx
	}
	return context.WithValue(ctx, sourceKey{}, source)
}

// Source 返回系统分配的入口类型。
func Source(ctx context.Context) string {
	source, _ := ctx.Value(sourceKey{}).(string)
	return source
}
