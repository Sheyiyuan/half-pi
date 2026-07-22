package lifecycle

import "context"

// Verdict 是 Guard 对 Action 的判断结果。
type Verdict int

const (
	// VerdictAbstain 表示本 Guard 不参与判断。
	VerdictAbstain Verdict = iota
	// VerdictAllow 表示本 Guard 不反对，但不能覆盖其他 Guard 的拒绝。
	VerdictAllow
	// VerdictRequireApproval 表示要求进入用户审批流程。
	VerdictRequireApproval
	// VerdictDeny 表示拒绝 Action。deny 的数值最大，不可被其他 verdict 覆盖。
	VerdictDeny
)

// String 返回 verdict 的可读表示。
func (v Verdict) String() string {
	switch v {
	case VerdictAbstain:
		return "abstain"
	case VerdictAllow:
		return "allow"
	case VerdictRequireApproval:
		return "require_approval"
	case VerdictDeny:
		return "deny"
	default:
		return "unknown"
	}
}

// MergeVerdicts 按单调收紧规则合并多个 Guard 的结果。
// 优先级：deny > require_approval > allow > abstain。
func MergeVerdicts(verdicts ...Verdict) Verdict {
	result := VerdictAbstain
	for _, v := range verdicts {
		if v > result {
			result = v
		}
	}
	return result
}

// FrozenAction 是已经冻结、不可再修改安全相关字段的 Action。
// Guard 只能读取 FrozenAction，不能修改。
type FrozenAction struct {
	Meta       Meta
	Kind       string
	Resource   string
	ArgsDigest string
	TargetNode string
	RiskLabels []string
	Purpose    string
	Sensitive  map[string]any
}

// Guard 是同步的前置决策器。它只运行在明确定义的 before_* 阶段。
// 实现必须快速返回，不得阻塞或重入 lifecycle。
type Guard interface {
	// ID 返回 Guard 的唯一标识。
	ID() string
	// Evaluate 评估冻结的 Action 并返回 verdict。
	// 返回 VerdictDeny 表示拒绝，VerdictRequireApproval 表示需用户审批。
	// context 可用于超时控制，Guard 应遵守 context 取消。
	Evaluate(ctx context.Context, action FrozenAction) Verdict
}

// CloneFrozenAction 创建独立的 Guard 只读视图。
func CloneFrozenAction(action FrozenAction) FrozenAction {
	clone := action
	clone.RiskLabels = append([]string(nil), action.RiskLabels...)
	clone.Sensitive = cloneStringMap(action.Sensitive)
	return clone
}
