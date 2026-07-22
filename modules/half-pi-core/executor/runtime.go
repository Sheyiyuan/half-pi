package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
)

const (
	maxInvocationArgs       = 1 << 20
	maxInvocationPurpose    = 1024
	maxInvocationTargetNode = 512
	maxInvocationRiskLabels = 64
	maxInvocationRiskLabel  = 128
)

// BackgroundContract 描述后台任务执行契约。
type BackgroundContract struct {
	TaskTimeout time.Duration
	LogLimit    int64
}

// Invocation 是一次工具调用的完整描述。
type Invocation struct {
	Meta              lifecycle.Meta
	Tool              string
	Args              json.RawMessage
	TargetNode        string
	RunID             string
	Timeout           time.Duration
	Background        *BackgroundContract
	ForceUserApproval bool
	Purpose           string
	RiskLabels        []string
}

// FrozenInvocation 是规范化、校验并冻结的工具调用。
type FrozenInvocation struct {
	Meta              lifecycle.Meta
	Tool              string
	Args              json.RawMessage
	ArgsDigest        string
	ApprovalDigest    string
	TargetNode        string
	RunID             string
	Timeout           time.Duration
	Background        *BackgroundContract
	ForceUserApproval bool
	Purpose           string
	RiskLabels        []string
}

// Freeze 校验 schema、规范化 JSON、深拷贝安全字段并计算摘要。
func (inv Invocation) Freeze() (FrozenInvocation, error) {
	tool, ok := FindTool(inv.Tool)
	if !ok {
		return FrozenInvocation{}, fmt.Errorf("unknown tool: %s", inv.Tool)
	}
	inv, err := normalizeInvocation(inv, tool)
	if err != nil {
		return FrozenInvocation{}, err
	}
	args := append(json.RawMessage(nil), inv.Args...)
	return FrozenInvocation{
		Meta:              inv.Meta,
		Tool:              inv.Tool,
		Args:              args,
		ArgsDigest:        hashDigest(args),
		TargetNode:        inv.TargetNode,
		RunID:             inv.RunID,
		Timeout:           inv.Timeout,
		Background:        cloneBackground(inv.Background),
		ForceUserApproval: inv.ForceUserApproval,
		Purpose:           inv.Purpose,
		RiskLabels:        append([]string(nil), inv.RiskLabels...),
	}, nil
}

// ToolDef 返回对应的 Tool 定义。
func (f FrozenInvocation) ToolDef() (Tool, bool) {
	return FindTool(f.Tool)
}

// Authorization 是一次安全审查和可选审批的结果。
type Authorization struct {
	Allowed             bool
	Reason              string
	ReasonCode          string
	ApprovalID          string
	ApprovalDecision    string
	ApprovalActorID     string
	ApprovalActorSource string
	Decision            string
	ResolutionID        string
	PolicyVersion       string
	RuleID              string
}

// Authorizer 对最终冻结的工具调用执行内置策略、Reviewer 和用户审批。
type Authorizer interface {
	Authorize(ctx context.Context, frozen FrozenInvocation) Authorization
}

// ExecutionOutcome 描述工具本体的执行结果。
type ExecutionOutcome string

const (
	ExecutionSucceeded ExecutionOutcome = "succeeded"
	ExecutionFailed    ExecutionOutcome = "failed"
	ExecutionCancelled ExecutionOutcome = "cancelled"
	ExecutionTimedOut  ExecutionOutcome = "timed_out"
	ExecutionPanicked  ExecutionOutcome = "panicked"
)

// Result 是工具执行和安全交付的完整结果。
type Result struct {
	ExecutionOutcome  ExecutionOutcome
	DeliveryOutcome   lifecycle.Outcome
	AuditDegraded     bool
	Output            string
	Data              any
	ErrorCode         string
	CompactProjection CompactProjection
}

// ToolRuntime 是生产工具执行的统一入口。
type ToolRuntime struct {
	authorizer Authorizer
	registry   *lifecycle.LifecycleRegistry
}

type lifecycleMetaContextKey struct{}

// WithLifecycleMeta 将当前冻结调用身份绑定到工具 context。
func WithLifecycleMeta(ctx context.Context, meta lifecycle.Meta) context.Context {
	return context.WithValue(ctx, lifecycleMetaContextKey{}, meta)
}

// LifecycleMetaFromContext 返回 ToolRuntime 注入的生命周期身份。
func LifecycleMetaFromContext(ctx context.Context) (lifecycle.Meta, bool) {
	meta, ok := ctx.Value(lifecycleMetaContextKey{}).(lifecycle.Meta)
	return meta, ok
}

// PreparedExecution 是已完成冻结、安全审查和 admission audit 的一次性执行对象。
type PreparedExecution struct {
	runtime       *ToolRuntime
	tool          Tool
	frozen        FrozenInvocation
	authorization Authorization
	used          atomic.Bool
}

// ExternalDigestFunc 在参数冻结后计算绑定目标节点、run 和执行模式的审批摘要。
type ExternalDigestFunc func(FrozenInvocation) (string, error)

// PreparedExternal 是已通过 ToolRuntime admission、但由外部状态机执行的调用。
type PreparedExternal struct {
	runtime       *ToolRuntime
	frozen        FrozenInvocation
	authorization Authorization
	used          atomic.Bool
}

// Frozen 返回只读冻结调用的深拷贝。
func (p *PreparedExecution) Frozen() FrozenInvocation {
	return cloneFrozenInvocation(p.frozen)
}

// Frozen 返回外部执行调用的冻结副本。
func (p *PreparedExternal) Frozen() FrozenInvocation { return cloneFrozenInvocation(p.frozen) }

// Authorization 返回用于生成远程一次性审批证明的结构化裁决。
func (p *PreparedExternal) Authorization() Authorization { return p.authorization }

// ObserveProgress 把外部状态机已接纳的进度接入当前工具 span。
// 返回 false 表示完整结果 Transformer 要求缓冲，调用方不得把原始进度继续投影给 Face 或普通观察者。
func (p *PreparedExternal) ObserveProgress(ctx context.Context, progress Progress) bool {
	if p == nil || p.runtime == nil {
		return false
	}
	p.runtime.publishProgress(ctx, p.frozen, progress)
	return !p.runtime.requiresBufferedResult(p.frozen.Meta)
}

// NewToolRuntime 创建工具运行时；nil Authorizer 始终拒绝。
func NewToolRuntime(authorizer Authorizer, registry *lifecycle.LifecycleRegistry) *ToolRuntime {
	if authorizer == nil {
		authorizer = denyAuthorizer{}
	}
	return &ToolRuntime{authorizer: authorizer, registry: registry}
}

// Registry 返回运行时使用的生命周期注册表。
func (rt *ToolRuntime) Registry() *lifecycle.LifecycleRegistry {
	return rt.registry
}

// Execute 按固定顺序变换、冻结、审查、审批、审计和执行工具。
func (rt *ToolRuntime) Execute(ctx context.Context, inv Invocation) Result {
	prepared, terminal := rt.Prepare(ctx, inv)
	if prepared == nil {
		return withCompactProjection(terminal)
	}
	return prepared.Execute(ctx)
}

// Prepare 完成所有外部副作用前的变换、冻结、Guard、审批和 admission audit。
func (rt *ToolRuntime) Prepare(ctx context.Context, inv Invocation) (*PreparedExecution, Result) {
	if ctx == nil {
		ctx = context.Background()
	}
	if lifecycle.IsHookContext(ctx) {
		inv.Meta = ensureMeta(inv.Meta)
		rt.publishInvocationDenied(ctx, inv, "hook_reentrancy", true)
		return nil, deniedResult("synchronous Hook reentrancy is not allowed", "hook_reentrancy")
	}
	tool, ok := FindTool(inv.Tool)
	if !ok {
		inv.Meta = ensureMeta(inv.Meta)
		rt.publishInvocationDenied(ctx, inv, "unknown_tool", true)
		return nil, deniedResult("unknown tool: "+inv.Tool, "unknown_tool")
	}
	return rt.prepareWithTool(ctx, inv, tool, nil, false)
}

// PrepareExternal 对由外部权威状态机执行的工具完成相同的 freeze、Guard、审批和 admission audit。
// definition 必须描述实际远程工具；digest 在参数冻结后计算，避免 Transformer 后摘要失配。
func (rt *ToolRuntime) PrepareExternal(ctx context.Context, inv Invocation, definition Tool, digest ExternalDigestFunc) (*PreparedExternal, Result) {
	if ctx == nil {
		ctx = context.Background()
	}
	if lifecycle.IsHookContext(ctx) {
		inv.Meta = ensureMeta(inv.Meta)
		rt.publishInvocationDenied(ctx, inv, "hook_reentrancy", true)
		return nil, deniedResult("synchronous Hook reentrancy is not allowed", "hook_reentrancy")
	}
	if definition.Name == "" || definition.Name != inv.Tool {
		inv.Meta = ensureMeta(inv.Meta)
		rt.publishInvocationDenied(ctx, inv, "invalid_external_tool", true)
		return nil, deniedResult("external tool definition does not match invocation", "invalid_external_tool")
	}
	if digest == nil {
		inv.Meta = ensureMeta(inv.Meta)
		rt.publishInvocationDenied(ctx, inv, "external_digest_required", true)
		return nil, deniedResult("external execution requires a bound approval digest", "external_digest_required")
	}
	prepared, terminal := rt.prepareWithTool(ctx, inv, definition, digest, true)
	if prepared == nil {
		return nil, terminal
	}
	return &PreparedExternal{
		runtime: rt, frozen: prepared.frozen, authorization: prepared.authorization,
	}, Result{}
}

func (rt *ToolRuntime) prepareWithTool(ctx context.Context, inv Invocation, tool Tool, digest ExternalDigestFunc, external bool) (*PreparedExecution, Result) {

	inv.Meta = ensureMeta(inv.Meta)
	rt.publish(ctx, lifecycle.PhaseToolProposed, inv.Meta, lifecycle.RedactedEvent{
		ResourceName: inv.Tool,
		InputLength:  len(inv.Args),
		RunID:        inv.RunID,
		Sensitive:    map[string]any{"args": append(json.RawMessage(nil), inv.Args...)},
	})

	prepared, err := rt.transformInvocation(ctx, inv, tool)
	if err != nil {
		rt.publishInvocationDenied(ctx, inv, "transform_failed", false)
		return nil, failedResult("tool invocation transform failed", "transform_failed")
	}

	if external {
		if !externalContractMatches(inv, prepared) {
			rt.publishInvocationDenied(ctx, prepared, "external_contract_changed", false)
			return nil, failedResult("external tool transformer changed the bound execution contract", "external_contract_changed")
		}
	} else {
		var found bool
		tool, found = FindTool(prepared.Tool)
		if !found {
			rt.publishInvocationDenied(ctx, prepared, "unknown_transformed_tool", false)
			return nil, failedResult("unknown transformed tool: "+prepared.Tool, "unknown_transformed_tool")
		}
	}
	prepared, err = normalizeInvocation(prepared, tool)
	if err != nil {
		rt.publishInvocationDenied(ctx, prepared, "freeze_error", false)
		return nil, failedResult(err.Error(), "freeze_error")
	}
	frozen := freezeNormalized(prepared)
	if digest != nil {
		approvalDigest, digestErr := digest(cloneFrozenInvocation(frozen))
		if digestErr != nil || approvalDigest == "" {
			rt.publishDenied(ctx, frozen, "contract_digest_failed")
			return nil, failedResult("external approval digest failed", "contract_digest_failed")
		}
		frozen.ApprovalDigest = approvalDigest
	}
	rt.publish(ctx, lifecycle.PhaseToolFrozen, frozen.Meta, lifecycle.RedactedEvent{
		ResourceName: frozen.Tool, InputLength: len(frozen.Args), InputDigest: frozen.ArgsDigest, RunID: frozen.RunID,
		Sensitive: map[string]any{"args": append(json.RawMessage(nil), frozen.Args...)},
	})

	verdict, guardReason := rt.runGuards(ctx, lifecycle.PhaseToolBeforeExecute, frozen)
	if verdict == lifecycle.VerdictDeny {
		if guardReason == "" {
			guardReason = "guard_denied"
		}
		rt.publishDenied(ctx, frozen, guardReason)
		return nil, deniedResult("blocked by guard", "guard_denied")
	}
	if verdict == lifecycle.VerdictRequireApproval {
		frozen.ForceUserApproval = true
	}

	authorization := rt.authorize(ctx, frozen)
	if !authorization.Allowed {
		reason := authorization.Reason
		if reason == "" {
			reason = "authorization denied"
		}
		rt.publishDenied(ctx, frozen, authorizationReasonCode(authorization))
		return nil, deniedResult(reason, "authorizer_denied")
	}

	if err := rt.commitAudit(ctx, lifecycle.PhaseToolAuthorized, lifecycle.AuditMutation{
		Meta: frozen.Meta, ActionKind: "tool", ResourceName: frozen.Tool,
		TargetNode: frozen.TargetNode, InputDigest: authorizationDigest(frozen), RiskLabels: append([]string(nil), frozen.RiskLabels...),
		Decision:   authorization.Decision,
		ReasonCode: authorization.ReasonCode, RuleID: authorization.RuleID, PolicyVersion: authorization.PolicyVersion,
		ApprovalID: authorization.ApprovalID, RunID: frozen.RunID,
	}); err != nil {
		rt.publishDenied(ctx, frozen, "audit_admission_failed")
		return nil, deniedResult("security admission audit failed", "audit_admission_failed")
	}
	rt.publish(ctx, lifecycle.PhaseToolAuthorized, frozen.Meta, lifecycle.RedactedEvent{
		ResourceName: frozen.Tool, InputDigest: authorizationDigest(frozen),
		ApprovalID: authorization.ApprovalID, RunID: frozen.RunID,
		Sensitive: map[string]any{"args": append(json.RawMessage(nil), frozen.Args...)},
	})

	return &PreparedExecution{runtime: rt, tool: tool, frozen: frozen, authorization: authorization}, Result{}
}

// Execute 执行已准备的冻结调用；同一对象只能使用一次。
func (p *PreparedExecution) Execute(ctx context.Context) Result {
	if p == nil || p.runtime == nil {
		return failedResult("prepared execution is nil", "invalid_prepared_execution")
	}
	if !p.used.CompareAndSwap(false, true) {
		return failedResult("prepared execution was already consumed", "prepared_execution_reused")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rt, frozen, authorization := p.runtime, p.frozen, p.authorization
	result := rt.executeTool(ctx, p.tool, frozen)
	return rt.finish(ctx, frozen, authorization, result)
}

// Complete 提交外部状态机已经产生的唯一终态，并执行结果 Transformer 与 terminal audit。
func (p *PreparedExternal) Complete(ctx context.Context, result Result) Result {
	if p == nil || p.runtime == nil {
		return failedResult("prepared external execution is nil", "invalid_prepared_execution")
	}
	if !p.used.CompareAndSwap(false, true) {
		return failedResult("prepared external execution was already consumed", "prepared_execution_reused")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return p.runtime.finish(ctx, p.frozen, p.authorization, result)
}

func (rt *ToolRuntime) finish(ctx context.Context, frozen FrozenInvocation, authorization Authorization, result Result) Result {
	result = rt.transformResult(ctx, frozen, result)
	if err := rt.commitAudit(ctx, lifecycle.PhaseToolFinished, lifecycle.AuditMutation{
		Meta: frozen.Meta, ActionKind: "tool", ResourceName: frozen.Tool,
		TargetNode: frozen.TargetNode, InputDigest: authorizationDigest(frozen), RiskLabels: append([]string(nil), frozen.RiskLabels...),
		Decision:   string(result.ExecutionOutcome),
		ReasonCode: result.ErrorCode, RuleID: authorization.RuleID, PolicyVersion: authorization.PolicyVersion,
		ApprovalID: authorization.ApprovalID, RunID: frozen.RunID,
		Details: map[string]any{
			"execution_outcome": result.ExecutionOutcome,
			"delivery_outcome":  result.DeliveryOutcome,
		},
	}); err != nil {
		result.AuditDegraded = true
		result.DeliveryOutcome = lifecycle.OutcomeFailed
		result.ErrorCode = "terminal_audit_failed"
		result.Output = "tool execution may have completed, but its terminal audit could not be recorded"
	}
	rt.publishFinished(ctx, frozen, result)
	return withCompactProjection(result)
}

// Abort 在外部状态机尚未产生副作用时终止调用。
func (p *PreparedExternal) Abort(ctx context.Context, reasonCode string) Result {
	if p == nil || p.runtime == nil {
		return failedResult("prepared external execution is nil", "invalid_prepared_execution")
	}
	if !p.used.CompareAndSwap(false, true) {
		return failedResult("prepared external execution was already consumed", "prepared_execution_reused")
	}
	if reasonCode == "" {
		reasonCode = "admission_aborted"
	}
	p.runtime.publishDenied(ctx, p.frozen, reasonCode)
	return deniedResult("prepared external execution aborted", reasonCode)
}

// Abort 在尚未调用 Tool.Execute 时终止已准备调用。
func (p *PreparedExecution) Abort(ctx context.Context, reasonCode string) Result {
	if p == nil || p.runtime == nil {
		return failedResult("prepared execution is nil", "invalid_prepared_execution")
	}
	if !p.used.CompareAndSwap(false, true) {
		return failedResult("prepared execution was already consumed", "prepared_execution_reused")
	}
	if reasonCode == "" {
		reasonCode = "admission_aborted"
	}
	p.runtime.publishDenied(ctx, p.frozen, reasonCode)
	return deniedResult("prepared execution aborted", reasonCode)
}

func (rt *ToolRuntime) authorize(ctx context.Context, frozen FrozenInvocation) (authorization Authorization) {
	defer func() {
		if recovered := recover(); recovered != nil {
			authorization = Authorization{Allowed: false, Decision: "deny", Reason: "authorizer failed", ReasonCode: "authorizer_panic"}
		}
	}()
	return rt.authorizer.Authorize(ctx, frozen)
}

func (rt *ToolRuntime) executeTool(ctx context.Context, tool Tool, frozen FrozenInvocation) (result Result) {
	if tool.Execute == nil {
		return failedResult(fmt.Sprintf("tool %s has no execute function", frozen.Tool), "no_execute")
	}
	rt.publish(ctx, lifecycle.PhaseToolStarted, frozen.Meta, lifecycle.RedactedEvent{
		ResourceName: frozen.Tool, InputDigest: frozen.ArgsDigest, RunID: frozen.RunID,
		Sensitive: map[string]any{"args": append(json.RawMessage(nil), frozen.Args...)},
	})

	execCtx := ctx
	var cancel context.CancelFunc
	if frozen.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, frozen.Timeout)
		defer cancel()
	}
	execCtx = WithLifecycleMeta(execCtx, frozen.Meta)
	parentProgress := progressFunc(execCtx)
	forwardProgress := !rt.requiresBufferedResult(frozen.Meta)
	execCtx = WithProgress(execCtx, func(progress Progress) {
		rt.publishProgress(execCtx, frozen, progress)
		if forwardProgress && parentProgress != nil {
			parentProgress(progress)
		}
	})
	defer func() {
		if recovered := recover(); recovered != nil {
			result = Result{
				ExecutionOutcome: ExecutionPanicked,
				DeliveryOutcome:  lifecycle.OutcomeFailed,
				Output:           "tool execution panicked",
				ErrorCode:        "panic",
			}
		}
	}()
	if err := execCtx.Err(); err != nil {
		return cancelledResult(err)
	}

	toolResult := tool.Execute(execCtx, append(json.RawMessage(nil), frozen.Args...))
	if err := execCtx.Err(); err != nil {
		return cancelledResult(err)
	}
	if toolResult == nil {
		return failedResult("tool returned nil result", "nil_result")
	}
	outcome := ExecutionSucceeded
	errorCode := ""
	if !toolResult.Success {
		outcome = ExecutionFailed
		errorCode = "tool_error"
	}
	output := toolResult.Output
	if output == "" && toolResult.Error != "" {
		output = toolResult.Error
	}
	return Result{
		ExecutionOutcome: outcome,
		DeliveryOutcome:  lifecycle.OutcomeSucceeded,
		Output:           output,
		Data:             toolResult.Data,
		ErrorCode:        errorCode,
		CompactProjection: CompactProjection{
			ReasonCategory: toolResult.CompactReason,
			CandidateFacts: append([]CompactFact(nil), toolResult.CompactFacts...),
		},
	}
}

func withCompactProjection(result Result) Result {
	result.CompactProjection.OutputBytes = len(result.Output)
	result.CompactProjection.OutputDigest = hashDigest([]byte(result.Output))
	if result.CompactProjection.ReasonCategory == "" {
		if result.ErrorCode != "" {
			result.CompactProjection.ReasonCategory = result.ErrorCode
		} else if result.ExecutionOutcome == ExecutionSucceeded {
			result.CompactProjection.ReasonCategory = "succeeded"
		} else {
			result.CompactProjection.ReasonCategory = "failed"
		}
	}
	return result
}

func (rt *ToolRuntime) transformInvocation(ctx context.Context, inv Invocation, tool Tool) (Invocation, error) {
	inv, ownedConfirmRequired, err := normalizeInvocationBeforeTransform(inv, tool)
	if err != nil {
		return Invocation{}, err
	}
	if rt.registry == nil {
		return inv, nil
	}
	action := lifecycle.MutableAction{Meta: inv.Meta, Kind: "tool", Fields: invocationFields(inv)}
	for _, binding := range rt.registry.TransformerBindingsForPhase(lifecycle.PhaseToolBeforeFreeze, inv.Meta) {
		transformed, hookErr := callTransformer(ctx, binding, cloneMutableAction(action))
		if hookErr != nil {
			if binding.Registration.FailureMode == lifecycle.FailureFailOpen {
				continue
			}
			return Invocation{}, hookErr
		}
		if transformed.Meta != action.Meta || transformed.Kind != "tool" {
			return Invocation{}, fmt.Errorf("transformer %s changed action identity", binding.Registration.ID)
		}
		action = transformed
	}
	transformed, err := invocationFromFields(inv.Meta, action.Fields)
	if err != nil {
		return Invocation{}, err
	}
	if inv.ForceUserApproval && !transformed.ForceUserApproval {
		return Invocation{}, fmt.Errorf("transformer removed required user approval")
	}
	if ownedConfirmRequired {
		confirmed, confirmErr := explicitConfirm(transformed.Args)
		if confirmErr != nil {
			return Invocation{}, confirmErr
		}
		if !confirmed {
			return Invocation{}, fmt.Errorf("transformer removed required user approval")
		}
	}
	return transformed, nil
}

func (rt *ToolRuntime) runGuards(ctx context.Context, phase lifecycle.Phase, frozen FrozenInvocation) (lifecycle.Verdict, string) {
	if rt.registry == nil {
		return lifecycle.VerdictAbstain, ""
	}
	action := lifecycle.FrozenAction{
		Meta: frozen.Meta, Kind: "tool", Resource: frozen.Tool, ArgsDigest: frozen.ArgsDigest,
		TargetNode: frozen.TargetNode, RiskLabels: append([]string(nil), frozen.RiskLabels...), Purpose: frozen.Purpose,
		Sensitive: map[string]any{"args": append(json.RawMessage(nil), frozen.Args...)},
	}
	bindings := rt.registry.GuardBindingsForPhase(phase, frozen.Meta)
	verdicts := make([]lifecycle.Verdict, 0, len(bindings))
	for _, binding := range bindings {
		view := lifecycle.CloneFrozenAction(action)
		if !binding.Registration.HasCapability(lifecycle.CapabilityReadRaw) && !binding.Registration.HasCapability(lifecycle.CapabilityReadArgs) {
			view.Sensitive = nil
		}
		verdict, err := callGuard(ctx, binding, view)
		if err != nil {
			return lifecycle.VerdictDeny, "hook_failed:" + binding.Registration.ID
		}
		verdicts = append(verdicts, verdict)
	}
	return lifecycle.MergeVerdicts(verdicts...), ""
}

func (rt *ToolRuntime) transformResult(ctx context.Context, frozen FrozenInvocation, result Result) Result {
	if rt.registry == nil {
		return result
	}
	action := lifecycle.MutableAction{Meta: frozen.Meta, Kind: "tool_result", Fields: map[string]any{
		"output": result.Output, "data": result.Data, "error_code": result.ErrorCode,
	}}
	for _, binding := range rt.registry.TransformerBindingsForPhase(lifecycle.PhaseToolResultBeforeCommit, frozen.Meta) {
		transformed, err := callTransformer(ctx, binding, cloneMutableAction(action))
		if err != nil {
			if binding.Registration.FailureMode == lifecycle.FailureFailOpen {
				continue
			}
			result.DeliveryOutcome = lifecycle.OutcomeFailed
			result.Output = "tool completed, but its result could not be delivered safely"
			result.Data = nil
			result.ErrorCode = "result_transform_failed"
			return result
		}
		if transformed.Meta != action.Meta || transformed.Kind != "tool_result" {
			result.DeliveryOutcome = lifecycle.OutcomeFailed
			result.Output = "tool completed, but its result could not be delivered safely"
			result.Data = nil
			result.ErrorCode = "result_transform_identity"
			return result
		}
		action = transformed
	}
	output, ok := action.Fields["output"].(string)
	if !ok || !utf8.ValidString(output) {
		result.DeliveryOutcome = lifecycle.OutcomeFailed
		result.Output = "tool completed, but its result could not be delivered safely"
		result.Data = nil
		result.ErrorCode = "invalid_transformed_result"
		return result
	}
	result.Output = output
	result.Data = action.Fields["data"]
	if code, ok := action.Fields["error_code"].(string); ok {
		result.ErrorCode = code
	}
	return result
}

func (rt *ToolRuntime) commitAudit(ctx context.Context, phase lifecycle.Phase, mutation lifecycle.AuditMutation) error {
	if rt.registry == nil {
		return nil
	}
	mutation.Meta = rt.registry.NextEventMeta(mutation.Meta)
	for _, binding := range rt.registry.AuditorBindingsForPhase(phase, mutation.Meta) {
		if err := callAuditor(ctx, binding, lifecycle.CloneAuditMutation(mutation)); err != nil && binding.Registration.FailureMode == lifecycle.FailureFailClosed {
			return err
		}
	}
	return nil
}

func (rt *ToolRuntime) publishDenied(ctx context.Context, frozen FrozenInvocation, reasonCode string) {
	_ = rt.commitAudit(ctx, lifecycle.PhaseToolDenied, lifecycle.AuditMutation{
		Meta: frozen.Meta, ActionKind: "tool", ResourceName: frozen.Tool,
		TargetNode: frozen.TargetNode, InputDigest: authorizationDigest(frozen), RiskLabels: append([]string(nil), frozen.RiskLabels...),
		Decision: "deny", ReasonCode: reasonCode, RunID: frozen.RunID,
	})
	rt.publish(ctx, lifecycle.PhaseToolDenied, frozen.Meta, lifecycle.RedactedEvent{
		Outcome: lifecycle.OutcomeDenied, ResourceName: frozen.Tool, InputDigest: authorizationDigest(frozen),
		ReasonCode: reasonCode, RunID: frozen.RunID,
		Sensitive: map[string]any{"args": append(json.RawMessage(nil), frozen.Args...)},
	})
}

func (rt *ToolRuntime) publishInvocationDenied(ctx context.Context, inv Invocation, reasonCode string, proposed bool) {
	if proposed {
		rt.publish(ctx, lifecycle.PhaseToolProposed, inv.Meta, lifecycle.RedactedEvent{
			ResourceName: inv.Tool, InputLength: len(inv.Args), RunID: inv.RunID,
			Sensitive: map[string]any{"args": append(json.RawMessage(nil), inv.Args...)},
		})
	}
	digest := lifecycle.HashDigest(inv.Args)
	_ = rt.commitAudit(ctx, lifecycle.PhaseToolDenied, lifecycle.AuditMutation{
		Meta: inv.Meta, ActionKind: "tool", ResourceName: inv.Tool,
		TargetNode: inv.TargetNode, InputDigest: digest, RiskLabels: append([]string(nil), inv.RiskLabels...),
		Decision: "deny", ReasonCode: reasonCode, RunID: inv.RunID,
	})
	rt.publish(ctx, lifecycle.PhaseToolDenied, inv.Meta, lifecycle.RedactedEvent{
		Outcome: lifecycle.OutcomeDenied, ResourceName: inv.Tool, InputLength: len(inv.Args),
		InputDigest: digest, ReasonCode: reasonCode, RunID: inv.RunID,
		Sensitive: map[string]any{"args": append(json.RawMessage(nil), inv.Args...)},
	})
}

func (rt *ToolRuntime) requiresBufferedResult(meta lifecycle.Meta) bool {
	return rt.registry != nil && len(rt.registry.TransformerBindingsForPhase(lifecycle.PhaseToolResultBeforeCommit, meta)) > 0
}

func (rt *ToolRuntime) publishProgress(ctx context.Context, frozen FrozenInvocation, progress Progress) {
	rt.publish(ctx, lifecycle.PhaseToolProgress, frozen.Meta, lifecycle.RedactedEvent{
		ResourceName: frozen.Tool, InputDigest: frozen.ArgsDigest, OutputLength: len(progress.Data),
		Summary: progress.Kind, RunID: frozen.RunID,
		Sensitive: map[string]any{"kind": progress.Kind, "data": progress.Data},
	})
}

func (rt *ToolRuntime) publishFinished(ctx context.Context, frozen FrozenInvocation, result Result) {
	outcome := lifecycle.Outcome(result.ExecutionOutcome)
	if !outcome.IsTerminal() {
		outcome = lifecycle.OutcomeFailed
	}
	rt.publish(ctx, lifecycle.PhaseToolFinished, frozen.Meta, lifecycle.RedactedEvent{
		Outcome: outcome, ExecutionOutcome: lifecycle.Outcome(result.ExecutionOutcome), DeliveryOutcome: result.DeliveryOutcome,
		AuditDegraded: result.AuditDegraded, ResourceName: frozen.Tool, InputDigest: frozen.ArgsDigest,
		OutputLength: len(result.Output), ReasonCode: result.ErrorCode, RunID: frozen.RunID,
		Sensitive: map[string]any{"output": result.Output},
	})
}

func (rt *ToolRuntime) publish(ctx context.Context, phase lifecycle.Phase, base lifecycle.Meta, event lifecycle.RedactedEvent) {
	if rt.registry == nil {
		return
	}
	event.Meta = rt.registry.NextEventMeta(base)
	event.Phase = phase
	rt.registry.Publish(ctx, event)
}

func callGuard(ctx context.Context, binding lifecycle.GuardBinding, action lifecycle.FrozenAction) (verdict lifecycle.Verdict, err error) {
	hookCtx, cancel := context.WithTimeout(ctx, binding.Registration.Timeout)
	defer cancel()
	hookCtx = lifecycle.WithHookContext(hookCtx)
	result := make(chan lifecycle.Verdict, 1)
	panicked := make(chan any, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				panicked <- recovered
			}
		}()
		result <- binding.Guard.Evaluate(hookCtx, action)
	}()
	select {
	case verdict = <-result:
		return verdict, nil
	case recovered := <-panicked:
		return lifecycle.VerdictDeny, fmt.Errorf("guard %s panicked: %v", binding.Registration.ID, recovered)
	case <-hookCtx.Done():
		return lifecycle.VerdictDeny, fmt.Errorf("guard %s: %w", binding.Registration.ID, hookCtx.Err())
	}
}

func callTransformer(ctx context.Context, binding lifecycle.TransformerBinding, action lifecycle.MutableAction) (result lifecycle.MutableAction, err error) {
	hookCtx, cancel := context.WithTimeout(ctx, binding.Registration.Timeout)
	defer cancel()
	hookCtx = lifecycle.WithHookContext(hookCtx)
	type response struct {
		action lifecycle.MutableAction
		err    error
	}
	responses := make(chan response, 1)
	panicked := make(chan any, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				panicked <- recovered
			}
		}()
		transformed, transformErr := binding.Transformer.Transform(hookCtx, action)
		responses <- response{action: transformed, err: transformErr}
	}()
	select {
	case response := <-responses:
		return response.action, response.err
	case recovered := <-panicked:
		return lifecycle.MutableAction{}, fmt.Errorf("transformer %s panicked: %v", binding.Registration.ID, recovered)
	case <-hookCtx.Done():
		return lifecycle.MutableAction{}, fmt.Errorf("transformer %s: %w", binding.Registration.ID, hookCtx.Err())
	}
}

func callAuditor(ctx context.Context, binding lifecycle.AuditorBinding, mutation lifecycle.AuditMutation) (err error) {
	hookCtx, cancel := context.WithTimeout(ctx, binding.Registration.Timeout)
	defer cancel()
	hookCtx = lifecycle.WithHookContext(hookCtx)
	responses := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				responses <- fmt.Errorf("auditor %s panicked: %v", binding.Registration.ID, recovered)
			}
		}()
		responses <- binding.Auditor.Commit(hookCtx, mutation)
	}()
	select {
	case err := <-responses:
		return err
	case <-hookCtx.Done():
		return fmt.Errorf("auditor %s: %w", binding.Registration.ID, hookCtx.Err())
	}
}

func normalizeInvocation(inv Invocation, tool Tool) (Invocation, error) {
	inv, _, args, err := parseInvocation(inv, tool)
	if err != nil {
		return Invocation{}, err
	}
	if err := validateObjectSchema(tool, args); err != nil {
		return Invocation{}, err
	}
	return inv, nil
}

func normalizeInvocationBeforeTransform(inv Invocation, tool Tool) (Invocation, bool, error) {
	inv, ownedConfirmRequired, _, err := parseInvocation(inv, tool)
	return inv, ownedConfirmRequired, err
}

func parseInvocation(inv Invocation, tool Tool) (Invocation, bool, map[string]any, error) {
	if inv.Timeout < 0 {
		return Invocation{}, false, nil, fmt.Errorf("tool %s timeout cannot be negative", inv.Tool)
	}
	if !utf8.ValidString(inv.TargetNode) || len(inv.TargetNode) > maxInvocationTargetNode {
		return Invocation{}, false, nil, fmt.Errorf("tool %s target node is invalid or too long", inv.Tool)
	}
	if !utf8.ValidString(inv.Purpose) || len(inv.Purpose) > maxInvocationPurpose {
		return Invocation{}, false, nil, fmt.Errorf("tool %s purpose is invalid or too long", inv.Tool)
	}
	if len(inv.RiskLabels) > maxInvocationRiskLabels {
		return Invocation{}, false, nil, fmt.Errorf("tool %s has too many risk labels", inv.Tool)
	}
	for _, label := range inv.RiskLabels {
		if label == "" || !utf8.ValidString(label) || len(label) > maxInvocationRiskLabel {
			return Invocation{}, false, nil, fmt.Errorf("tool %s has an invalid risk label", inv.Tool)
		}
	}
	if len(inv.Args) == 0 {
		inv.Args = json.RawMessage(`{}`)
	}
	if len(inv.Args) > maxInvocationArgs {
		return Invocation{}, false, nil, fmt.Errorf("tool %s args exceed %d bytes", inv.Tool, maxInvocationArgs)
	}
	args, err := decodeInvocationArgs(inv.Tool, inv.Args)
	if err != nil {
		return Invocation{}, false, nil, err
	}
	ownedConfirmRequired := false
	if confirm, exists := args["confirm"]; exists {
		value, ok := confirm.(bool)
		if !ok {
			return Invocation{}, false, nil, fmt.Errorf("tool %s confirm must be boolean", inv.Tool)
		}
		if tool.OwnsConfirm {
			ownedConfirmRequired = value
		} else {
			inv.ForceUserApproval = inv.ForceUserApproval || value
			delete(args, "confirm")
		}
	}
	canonical, err := json.Marshal(args)
	if err != nil {
		return Invocation{}, false, nil, fmt.Errorf("canonicalize args for tool %s: %w", inv.Tool, err)
	}
	inv.Args = append(json.RawMessage(nil), canonical...)
	inv.Background = cloneBackground(inv.Background)
	inv.RiskLabels = append([]string(nil), inv.RiskLabels...)
	return inv, ownedConfirmRequired, args, nil
}

func explicitConfirm(raw json.RawMessage) (bool, error) {
	args, err := decodeInvocationArgs("transformed tool", raw)
	if err != nil {
		return false, err
	}
	confirm, exists := args["confirm"]
	if !exists {
		return false, nil
	}
	value, ok := confirm.(bool)
	if !ok {
		return false, fmt.Errorf("transformed tool confirm must be boolean")
	}
	return value, nil

}

func decodeInvocationArgs(toolName string, raw json.RawMessage) (map[string]any, error) {
	var args map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&args); err != nil {
		return nil, fmt.Errorf("invalid args JSON for tool %s: %w", toolName, err)
	}
	if args == nil || decoder.Decode(&struct{}{}) == nil {
		return nil, fmt.Errorf("invalid args JSON for tool %s: expected one object", toolName)
	}
	return args, nil
}

func validateObjectSchema(tool Tool, args map[string]any) error {
	if tool.Parameters == nil {
		return nil
	}
	properties := make(map[string]string, len(tool.Parameters.Properties))
	for _, property := range tool.Parameters.Properties {
		properties[property.Name] = property.Type
	}
	for _, required := range tool.Parameters.Required {
		if _, exists := args[required]; !exists {
			return fmt.Errorf("tool %s missing required argument %q", tool.Name, required)
		}
	}
	for name, value := range args {
		typ, exists := properties[name]
		if !exists {
			return fmt.Errorf("tool %s has unknown argument %q", tool.Name, name)
		}
		if !matchesJSONType(value, typ) {
			return fmt.Errorf("tool %s argument %q must be %s", tool.Name, name, typ)
		}
	}
	return nil
}

func matchesJSONType(value any, typ string) bool {
	switch typ {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		return ok && !strings.ContainsAny(number.String(), ".eE")
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	default:
		return false
	}
}

func invocationFields(inv Invocation) map[string]any {
	return map[string]any{
		"tool": inv.Tool, "args": append(json.RawMessage(nil), inv.Args...), "target_node": inv.TargetNode,
		"run_id": inv.RunID, "timeout": inv.Timeout, "background": cloneBackground(inv.Background),
		"force_user_approval": inv.ForceUserApproval, "purpose": inv.Purpose,
		"risk_labels": append([]string(nil), inv.RiskLabels...),
	}
}

func invocationFromFields(meta lifecycle.Meta, fields map[string]any) (Invocation, error) {
	tool, ok := fields["tool"].(string)
	if !ok || tool == "" {
		return Invocation{}, fmt.Errorf("transformed tool name is invalid")
	}
	args, err := rawMessageField(fields["args"])
	if err != nil {
		return Invocation{}, err
	}
	inv := Invocation{Meta: meta, Tool: tool, Args: args}
	inv.TargetNode, _ = fields["target_node"].(string)
	inv.RunID, _ = fields["run_id"].(string)
	inv.Timeout, _ = fields["timeout"].(time.Duration)
	inv.Background, _ = fields["background"].(*BackgroundContract)
	inv.ForceUserApproval, _ = fields["force_user_approval"].(bool)
	inv.Purpose, _ = fields["purpose"].(string)
	inv.RiskLabels, _ = fields["risk_labels"].([]string)
	return inv, nil
}

func rawMessageField(value any) (json.RawMessage, error) {
	switch args := value.(type) {
	case json.RawMessage:
		return append(json.RawMessage(nil), args...), nil
	case []byte:
		return append(json.RawMessage(nil), args...), nil
	default:
		encoded, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("transformed args are invalid: %w", err)
		}
		return encoded, nil
	}
}

func cloneMutableAction(action lifecycle.MutableAction) lifecycle.MutableAction {
	return lifecycle.CloneMutableAction(action)
}

func cloneBackground(background *BackgroundContract) *BackgroundContract {
	if background == nil {
		return nil
	}
	clone := *background
	return &clone
}

func externalContractMatches(original, transformed Invocation) bool {
	return original.Tool == transformed.Tool &&
		original.TargetNode == transformed.TargetNode &&
		original.RunID == transformed.RunID &&
		original.Timeout == transformed.Timeout &&
		backgroundContractsEqual(original.Background, transformed.Background)
}

func backgroundContractsEqual(a, b *BackgroundContract) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func freezeNormalized(inv Invocation) FrozenInvocation {
	args := append(json.RawMessage(nil), inv.Args...)
	return FrozenInvocation{
		Meta: inv.Meta, Tool: inv.Tool, Args: args, ArgsDigest: hashDigest(args),
		TargetNode: inv.TargetNode, RunID: inv.RunID, Timeout: inv.Timeout,
		Background: cloneBackground(inv.Background), ForceUserApproval: inv.ForceUserApproval,
		Purpose: inv.Purpose, RiskLabels: append([]string(nil), inv.RiskLabels...),
	}
}

func cloneFrozenInvocation(source FrozenInvocation) FrozenInvocation {
	clone := source
	clone.Args = append(json.RawMessage(nil), source.Args...)
	clone.Background = cloneBackground(source.Background)
	clone.RiskLabels = append([]string(nil), source.RiskLabels...)
	return clone
}

func authorizationDigest(frozen FrozenInvocation) string {
	if frozen.ApprovalDigest != "" {
		return frozen.ApprovalDigest
	}
	return frozen.ArgsDigest
}

func ensureMeta(meta lifecycle.Meta) lifecycle.Meta {
	if meta.EventID == "" || meta.TraceID == "" || meta.SpanID == "" {
		generated := lifecycle.NewMeta(meta.Source)
		if meta.Source == "" {
			meta.Source = lifecycle.SourceMind
		}
		generated.Source = meta.Source
		generated.RequestID = meta.RequestID
		generated.ConversationID = meta.ConversationID
		generated.GroupID = meta.GroupID
		generated.PrincipalID = meta.PrincipalID
		generated.NodeID = meta.NodeID
		return generated
	}
	return meta
}

func failedResult(output, code string) Result {
	return Result{ExecutionOutcome: ExecutionFailed, DeliveryOutcome: lifecycle.OutcomeFailed, Output: output, ErrorCode: code}
}

func deniedResult(reason, code string) Result {
	return Result{
		ExecutionOutcome: ExecutionFailed, DeliveryOutcome: lifecycle.OutcomeDenied,
		Output: "operation denied: " + reason, ErrorCode: code,
	}
}

func cancelledResult(err error) Result {
	outcome := ExecutionCancelled
	delivery := lifecycle.OutcomeCancelled
	if err == context.DeadlineExceeded {
		outcome = ExecutionTimedOut
		delivery = lifecycle.OutcomeTimedOut
	}
	return Result{
		ExecutionOutcome: outcome, DeliveryOutcome: delivery,
		Output: fmt.Sprintf("execution cancelled: %v", err), ErrorCode: string(outcome),
	}
}

func authorizationReasonCode(authorization Authorization) string {
	if authorization.ReasonCode != "" {
		return authorization.ReasonCode
	}
	return authorization.Decision
}

func hashDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(context.Context, FrozenInvocation) Authorization {
	return Authorization{Allowed: false, Reason: "no authorizer configured", ReasonCode: "no_authorizer", Decision: "deny"}
}
