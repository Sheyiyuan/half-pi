package lifecycle

import "context"

// MutableAction 是可被 Transformer 修改的 Action。
// Transformer 完成后必须重新执行 schema、大小和编码校验，再计算最终摘要。
type MutableAction struct {
	Meta   Meta
	Kind   string
	Fields map[string]any
}

// Transformer 在 Action 冻结前修改允许变换的字段。
// 它不是安全裁决器，不能替代 Guard 的决定。
type Transformer interface {
	// ID 返回 Transformer 的唯一标识。
	ID() string
	// Transform 在 Action 冻结前修改字段。
	// 返回修改后的 MutableAction。如果不需要修改，返回原值。
	// context 可用于超时控制。
	Transform(ctx context.Context, action MutableAction) (MutableAction, error)
}
