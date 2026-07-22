package lifecycle

// Phase 是生命周期阶段的唯一标识。
type Phase string

// ── Chat 和消息阶段 ──

const (
	// PhaseChatReceived transport 已完成认证和基础 payload 校验。
	PhaseChatReceived Phase = "chat.received"
	// PhaseMessageBeforeAccept 输入 Transformer 和 Guard 执行点。
	PhaseMessageBeforeAccept Phase = "message.before_accept"
	// PhaseMessageAdmitted 已通过输入策略，但尚未承诺持久化成功。
	PhaseMessageAdmitted Phase = "message.admitted"
	// PhaseMessagePersisted 用户消息已写入 conversation store。
	PhaseMessagePersisted Phase = "message.persisted"
	// PhaseChatFinished Chat 唯一终态。
	PhaseChatFinished Phase = "chat.finished"
)

// ── 模型调用阶段 ──

const (
	// PhaseModelBeforeRequest memory、Skill、prompt policy、token budget 等注入点。
	PhaseModelBeforeRequest Phase = "model.before_request"
	// PhaseModelRequested provider 请求已经开始。
	PhaseModelRequested Phase = "model.requested"
	// PhaseModelDelta 可见文本增量，允许有界丢弃。
	PhaseModelDelta Phase = "model.delta"
	// PhaseModelResponseReceived provider 完整响应已组装，尚未必交付。
	PhaseModelResponseReceived Phase = "model.response_received"
	// PhaseAssistantBeforeDeliver 完整输出审查、脱敏和最终展示变换。
	PhaseAssistantBeforeDeliver Phase = "assistant.before_deliver"
	// PhaseAssistantDelivered 输出已经进入 Face/REPL 可见面。
	PhaseAssistantDelivered Phase = "assistant.delivered"
	// PhaseModelFailed provider、解析、取消或一致性错误。
	PhaseModelFailed Phase = "model.failed"
)

// ── 工具调用阶段 ──

const (
	// PhaseToolProposed 模型或调用方提出工具名和原始参数。
	PhaseToolProposed Phase = "tool.proposed"
	// PhaseToolBeforeFreeze 参数规范化和授权 Transformer。
	PhaseToolBeforeFreeze Phase = "tool.before_freeze"
	// PhaseToolFrozen 最终参数通过 schema 校验并计算 digest。
	PhaseToolFrozen Phase = "tool.frozen"
	// PhaseToolBeforeExecute 权限、策略、插件 Guard、审批。
	PhaseToolBeforeExecute Phase = "tool.before_execute"
	// PhaseToolAuthorized Mind/本地执行层允许继续。
	PhaseToolAuthorized Phase = "tool.authorized"
	// PhaseToolDenied 执行前终止，不产生 started。
	PhaseToolDenied Phase = "tool.denied"
	// PhaseToolStarted Tool.Execute 即将被调用。
	PhaseToolStarted Phase = "tool.started"
	// PhaseToolProgress 有界 stdout/stderr 或其他进度。
	PhaseToolProgress Phase = "tool.progress"
	// PhaseToolResultBeforeCommit 结果脱敏、大小限制、结构化校验。
	PhaseToolResultBeforeCommit Phase = "tool.result_before_commit"
	// PhaseToolFinished 工具唯一终态。
	PhaseToolFinished Phase = "tool.finished"
)

// ── 审批阶段 ──

const (
	PhaseApprovalRequested Phase = "approval.requested"
	PhaseApprovalResolved  Phase = "approval.resolved"
	PhaseApprovalExpired   Phase = "approval.expired"
	PhaseApprovalCancelled Phase = "approval.cancelled"
)

// ── 安全审查阶段 ──

const (
	PhaseSecurityReviewRequested Phase = "security.review.requested"
	PhaseSecurityReviewResolved  Phase = "security.review.resolved"
	PhaseSecurityReviewFailed    Phase = "security.review.failed"
	PhaseSecurityDecision        Phase = "security.decision"
)

// IsBefore 判断当前阶段是否在参考阶段之前。
// 用于确保 Transformer 只在 freeze 前运行等约束。
func IsBefore(a, reference Phase) bool {
	aOrder, aOK := phaseOrder[a]
	referenceOrder, referenceOK := phaseOrder[reference]
	return aOK && referenceOK && aOrder < referenceOrder
}

// IsAfter 判断当前阶段是否在参考阶段之后。
func IsAfter(a, reference Phase) bool {
	aOrder, aOK := phaseOrder[a]
	referenceOrder, referenceOK := phaseOrder[reference]
	return aOK && referenceOK && aOrder > referenceOrder
}

// IsSupportedPhase 判断 phase 是否属于稳定生命周期契约。
func IsSupportedPhase(phase Phase) bool {
	_, ok := phaseOrder[phase]
	return ok
}

// phaseOrder 定义各阶段在生命周期中的顺序。
var phaseOrder = map[Phase]int{
	// Chat / message
	PhaseChatReceived:        100,
	PhaseMessageBeforeAccept: 200,
	PhaseMessageAdmitted:     300,
	PhaseMessagePersisted:    400,

	// Model
	PhaseModelBeforeRequest:     500,
	PhaseModelRequested:         600,
	PhaseModelDelta:             700,
	PhaseModelResponseReceived:  800,
	PhaseAssistantBeforeDeliver: 900,
	PhaseAssistantDelivered:     1000,
	PhaseModelFailed:            1100,

	// Tool
	PhaseToolProposed:      1200,
	PhaseToolBeforeFreeze:  1300,
	PhaseToolFrozen:        1400,
	PhaseToolBeforeExecute: 1500,

	// Security review and approval happen after freeze and before admission.
	PhaseSecurityReviewRequested: 1510,
	PhaseSecurityReviewResolved:  1520,
	PhaseSecurityReviewFailed:    1530,
	PhaseSecurityDecision:        1540,
	PhaseApprovalRequested:       1550,
	PhaseApprovalResolved:        1560,
	PhaseApprovalExpired:         1570,
	PhaseApprovalCancelled:       1580,

	PhaseToolAuthorized:         1600,
	PhaseToolDenied:             1700,
	PhaseToolStarted:            1800,
	PhaseToolProgress:           1900,
	PhaseToolResultBeforeCommit: 2000,
	PhaseToolFinished:           2100,

	// Chat finished
	PhaseChatFinished: 3000,
}
