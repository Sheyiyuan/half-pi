package lifecycle

import "context"

// AuditMutation 是需要持久化的安全或权威状态事实。
type AuditMutation struct {
	Meta
	ActionKind    string
	ResourceName  string
	TargetNode    string
	InputDigest   string
	RiskLabels    []string
	Decision      string
	ReasonCode    string
	RuleID        string
	PolicyVersion string
	ApprovalID    string
	RunID         string
	TaskID        string
	Details       map[string]any
}

// Auditor 负责持久化安全或权威状态事实。
// Auditor 与 Observer 的区别是：Auditor 有明确的数据完整性要求，
// 敏感 mutation 可能要求业务数据和 audit 在同一事务提交，Auditor 失败可能让操作 fail closed。
type Auditor interface {
	// ID 返回 Auditor 的唯一标识。
	ID() string
	// Commit 持久化审计记录。失败时调用方必须根据上下文决定拒绝还是进入 degraded 状态。
	Commit(ctx context.Context, mutation AuditMutation) error
}

// CloneAuditMutation 创建独立的 Auditor 事实视图。
func CloneAuditMutation(mutation AuditMutation) AuditMutation {
	clone := mutation
	clone.RiskLabels = append([]string(nil), mutation.RiskLabels...)
	clone.Details = cloneStringMap(mutation.Details)
	return clone
}
