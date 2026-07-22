package lifecycle

import "context"

// RedactedEvent 是经过脱敏后的生命周期事件。
// Observer 和普通插件默认只收到 RedactedEvent，原始内容必须通过显式 capability 获取。
type RedactedEvent struct {
	Meta
	Phase   Phase   `json:"phase"`
	Outcome Outcome `json:"outcome,omitempty"`

	ExecutionOutcome Outcome `json:"execution_outcome,omitempty"`
	DeliveryOutcome  Outcome `json:"delivery_outcome,omitempty"`
	AuditDegraded    bool    `json:"audit_degraded,omitempty"`

	// 摘要信息（非敏感）
	ResourceName string `json:"resource_name,omitempty"`
	InputLength  int    `json:"input_length,omitempty"`
	OutputLength int    `json:"output_length,omitempty"`
	InputDigest  string `json:"input_digest,omitempty"`
	ReasonCode   string `json:"reason_code,omitempty"`
	Summary      string `json:"summary,omitempty"`

	// 关联 ID
	RunID      string `json:"run_id,omitempty"`
	ApprovalID string `json:"approval_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`

	// Provider / Model 标识
	ProviderID    string `json:"provider_id,omitempty"`
	ModelID       string `json:"model_id,omitempty"`
	Profile       string `json:"profile,omitempty"`
	PolicyVersion string `json:"policy_version,omitempty"`
	DurationMS    int64  `json:"duration_ms,omitempty"`

	// 额外安全数据（仅授予敏感读取 capability 的观察者可见）
	Sensitive map[string]any `json:"-"`
}

// Observer 消费不可变、默认脱敏的 Event。
// Observer 不得阻塞核心流程，不得重新进入同一个 Core，也不得成为安全放行的必要条件。
type Observer interface {
	// ID 返回 Observer 的唯一标识。
	ID() string
	// Observe 消费脱敏后的生命周期事件。
	// 实现必须快速返回，错误只用于内部记录，不影响主流程。
	Observe(ctx context.Context, event RedactedEvent)
}

// ObserverView 为一个 Observer 生成独立的数据视图。
// 普通 Observer 只能看到摘要；敏感字段必须由注册 capability 显式授予。
func ObserverView(event RedactedEvent, registration Registration) RedactedEvent {
	view := event
	allowed := registration.HasCapability(CapabilityReadRaw)
	if !allowed && registration.HasCapability(CapabilityReadArgs) {
		allowed = isToolInputPhase(event.Phase)
	}
	if !allowed && registration.HasCapability(CapabilityReadResults) {
		allowed = event.Phase == PhaseToolProgress || event.Phase == PhaseToolResultBeforeCommit || event.Phase == PhaseToolFinished
	}
	if !allowed {
		view.Sensitive = nil
		return view
	}
	view.Sensitive = cloneStringMap(event.Sensitive)
	return view
}

func isToolInputPhase(phase Phase) bool {
	switch phase {
	case PhaseToolProposed, PhaseToolBeforeFreeze, PhaseToolFrozen, PhaseToolBeforeExecute,
		PhaseToolAuthorized, PhaseToolDenied, PhaseToolStarted:
		return true
	default:
		return false
	}
}
