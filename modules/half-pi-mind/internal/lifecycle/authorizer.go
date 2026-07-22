// Package lifecycle 提供 Mind 生命周期协调、安全审核和 EventBus 适配。
package lifecycle

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	coreexec "github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/security"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
)

const defaultPolicyVersion = "v1"

// Approver 是审批流程的抽象。
type Approver interface {
	Confirm(ctx context.Context, request approval.Request) approval.Resolution
}

// MindAuthorizer 组合确定性策略、独立 Reviewer 和 Approval Broker。
type MindAuthorizer struct {
	mu             sync.Mutex
	policy         *security.Policy
	mode           string
	approver       Approver
	reviewer       Reviewer
	autoAllow      map[authorizationKey]bool
	autoDeny       map[authorizationKey]bool
	policyVersion  string
	reviewObserver ReviewObserver
}

// ReviewObserver 接收独立 Reviewer 的脱敏生命周期事实。
// security.decision 的必需审计失败通过 error 让 Authorizer fail closed。
type ReviewObserver func(context.Context, corelifecycle.Phase, coreexec.FrozenInvocation, ReviewVerdict, error) error

type authorizationKey struct {
	GroupID    string
	Tool       string
	TargetNode string
	Policy     string
}

// NewMindAuthorizer 创建 Mind 侧授权器。
func NewMindAuthorizer(mode string, policy *security.Policy, approver Approver) *MindAuthorizer {
	if policy == nil {
		policy = security.New()
	}
	return &MindAuthorizer{
		mode: mode, policy: policy.Clone(), approver: approver,
		autoAllow: make(map[authorizationKey]bool), autoDeny: make(map[authorizationKey]bool),
		policyVersion: defaultPolicyVersion,
	}
}

// SetMode 原子更新安全模式及其策略路由。
func (a *MindAuthorizer) SetMode(mode string, policy *security.Policy) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mode = canonicalMode(mode)
	if policy == nil {
		policy = security.New()
	}
	a.policy = policy.Clone()
}

// SetApprover 更新用户审批 Broker。
func (a *MindAuthorizer) SetApprover(approver Approver) {
	a.mu.Lock()
	a.approver = approver
	a.mu.Unlock()
}

// SetReviewer 更新独立 AI Reviewer；nil 表示审核候选必须交给用户。
func (a *MindAuthorizer) SetReviewer(reviewer Reviewer) {
	a.mu.Lock()
	a.reviewer = reviewer
	a.mu.Unlock()
}

// SetReviewObserver 设置 Reviewer 生命周期观察器。
func (a *MindAuthorizer) SetReviewObserver(observer ReviewObserver) {
	a.mu.Lock()
	a.reviewObserver = observer
	a.mu.Unlock()
}

// Authorize 实现 executor.Authorizer。
func (a *MindAuthorizer) Authorize(ctx context.Context, frozen coreexec.FrozenInvocation) (authorization coreexec.Authorization) {
	a.mu.Lock()
	mode := canonicalMode(a.mode)
	policy := a.policy.Clone()
	approver := a.approver
	reviewer := a.reviewer
	reviewObserver := a.reviewObserver
	policyVersion := a.policyVersion
	key := authorizationKey{GroupID: frozen.Meta.GroupID, Tool: frozen.Tool, TargetNode: frozen.TargetNode, Policy: policyVersion}
	autoAllow, autoDeny := a.autoAllow[key], a.autoDeny[key]
	a.mu.Unlock()
	defer func() {
		if authorization.PolicyVersion == "" {
			authorization.PolicyVersion = policyVersion
		}
	}()

	tool, decision, reason, found := coreexec.CheckToolWithPolicy(frozen.Tool, frozen.Args, policy)
	remoteUnknown := !found && frozen.TargetNode != ""
	if !found && !remoteUnknown {
		return denyAuthorization(reason, "unknown_tool")
	}
	if decision == coreexec.DecisionDeny && !remoteUnknown {
		return denyAuthorization(reason, "deterministic_deny")
	}
	if remoteUnknown && !frozen.ForceUserApproval {
		return denyAuthorization("Mind cannot verify a remote-only tool without explicit approval", "unknown_remote_tool")
	}

	defaultConfirm := found && tool.DefaultConfirm
	forceUser := frozen.ForceUserApproval || defaultConfirm
	needsUser := forceUser
	reasonCode := "policy_allow"
	if reason == "" && forceUser {
		reason = "tool requires explicit user approval"
	}

	switch mode {
	case "strict":
		if decision != coreexec.DecisionAllow || remoteUnknown {
			return denyAuthorization(reason, "strict_deny")
		}
	case "normal":
		needsUser = needsUser || decision == coreexec.DecisionConfirm || remoteUnknown
		if needsUser && reason == "" {
			reason = "tool requires user approval"
		}
		reasonCode = "normal_policy"
	case "review":
		if !needsUser && reviewCandidate(found, tool, frozen.Args, policy) {
			_ = notifyReview(reviewObserver, ctx, corelifecycle.PhaseSecurityReviewRequested, frozen, reviewerMetadata(reviewer, policyVersion), nil)
			verdict, err := reviewSafely(ctx, reviewer, frozen, policyVersion)
			if err != nil {
				_ = notifyReview(reviewObserver, ctx, corelifecycle.PhaseSecurityReviewFailed, frozen, verdict, err)
			} else {
				_ = notifyReview(reviewObserver, ctx, corelifecycle.PhaseSecurityReviewResolved, frozen, verdict, nil)
			}
			if auditErr := notifyReview(reviewObserver, ctx, corelifecycle.PhaseSecurityDecision, frozen, verdict, err); auditErr != nil {
				return denyAuthorization("security review audit failed", "audit_admission_failed")
			}
			if err != nil || verdict.Decision == ReviewRequireUser {
				needsUser = true
				reasonCode = "review_require_user"
				if verdict.Summary != "" {
					reason = verdict.Summary
				} else if reason == "" {
					reason = "independent security review requires user approval"
				}
			} else {
				return coreexec.Authorization{Allowed: true, Decision: "allow", ReasonCode: verdict.ReasonCode}
			}
		}
	case "yolo":
		// Hard deny was already applied; explicit and default confirmation remain mandatory.
	}

	if !needsUser {
		return coreexec.Authorization{Allowed: true, Decision: "allow", ReasonCode: reasonCode}
	}
	if autoDeny {
		return denyAuthorization(reason, "session_deny")
	}
	// Review mode still reviews every new frozen Action; session allow only removes
	// repeated user prompts after the reviewer has requested escalation.
	if autoAllow && !forceUser {
		return coreexec.Authorization{Allowed: true, Decision: "allow", ReasonCode: "session_allow"}
	}
	if approver == nil {
		return denyAuthorization(reason, "approval_unavailable")
	}

	resolution := approver.Confirm(ctx, approval.Request{
		Meta:           frozen.Meta,
		ConversationID: frozen.Meta.ConversationID,
		RequestID:      frozen.Meta.RequestID,
		RunID:          frozen.RunID,
		Tool:           frozen.Tool,
		Reason:         reason,
		ArgsDigest:     frozenAuthorizationDigest(frozen),
	})
	result := coreexec.Authorization{
		Allowed: false, Reason: reason, ReasonCode: "user_denied",
		Decision: "require_approval", ApprovalID: resolution.ApprovalID,
		ApprovalDecision: string(resolution.Decision), ApprovalActorID: resolution.Actor.ID,
		ApprovalActorSource: resolution.Actor.Source,
	}
	switch resolution.Decision {
	case protocol.FaceApprovalAllowOnce:
		result.Allowed = true
		result.ReasonCode = "user_allow_once"
	case protocol.FaceApprovalAllowSession:
		if !forceUser {
			a.mu.Lock()
			a.autoAllow[key] = true
			a.mu.Unlock()
		}
		result.Allowed = true
		result.ReasonCode = "user_allow_session"
	case protocol.FaceApprovalDenySession:
		a.mu.Lock()
		a.autoDeny[key] = true
		a.mu.Unlock()
	}
	return result
}

func frozenAuthorizationDigest(frozen coreexec.FrozenInvocation) string {
	if frozen.ApprovalDigest != "" {
		return frozen.ApprovalDigest
	}
	return frozen.ArgsDigest
}

func notifyReview(observer ReviewObserver, ctx context.Context, phase corelifecycle.Phase, frozen coreexec.FrozenInvocation, verdict ReviewVerdict, err error) error {
	if observer != nil {
		return observer(ctx, phase, frozen, verdict, err)
	}
	return nil
}

func reviewCandidate(found bool, tool coreexec.Tool, args []byte, policy *security.Policy) bool {
	if !found || tool.PolicyCheck == nil {
		return false
	}
	decision, _ := tool.PolicyCheck(args, policy.WithMode(security.ModeNormal))
	return decision == coreexec.DecisionConfirm
}

func reviewSafely(ctx context.Context, reviewer Reviewer, frozen coreexec.FrozenInvocation, policyVersion string) (verdict ReviewVerdict, err error) {
	started := time.Now()
	metadata := reviewerMetadata(reviewer, policyVersion)
	defer func() {
		if verdict.ProviderID == "" {
			verdict.ProviderID = metadata.ProviderID
		}
		if verdict.ModelID == "" {
			verdict.ModelID = metadata.ModelID
		}
		if verdict.Profile == "" {
			verdict.Profile = metadata.Profile
		}
		if verdict.DurationMS == 0 {
			verdict.DurationMS = time.Since(started).Milliseconds()
		}
	}()
	if reviewer == nil {
		return requireUserVerdict(policyVersion, "reviewer_unavailable"), fmt.Errorf("reviewer unavailable")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			verdict = requireUserVerdict(policyVersion, "reviewer_panic")
			err = fmt.Errorf("reviewer panic: %v", recovered)
		}
	}()
	request := NewReviewRequest(frozen, policyVersion)
	if !request.ProjectionComplete {
		return ReviewVerdict{
			SchemaVersion: 1, Decision: ReviewRequireUser, ReasonCode: "sensitive_argument",
			Summary: "this action contains fields that require direct user approval", PolicyVersion: policyVersion,
		}, nil
	}
	verdict, err = reviewer.Review(ctx, request)
	if err != nil {
		failed := requireUserVerdict(policyVersion, "reviewer_failed")
		failed.ProviderID, failed.ModelID, failed.Profile = verdict.ProviderID, verdict.ModelID, verdict.Profile
		failed.DurationMS, failed.FailureCode = verdict.DurationMS, verdict.FailureCode
		return failed, err
	}
	if validationErr := validateReviewVerdict(verdict, policyVersion); validationErr != nil {
		failed := requireUserVerdict(policyVersion, "reviewer_failed")
		failed.ProviderID, failed.ModelID, failed.Profile = verdict.ProviderID, verdict.ModelID, verdict.Profile
		failed.DurationMS, failed.FailureCode = verdict.DurationMS, "malformed_verdict"
		return failed, validationErr
	}
	return verdict, nil
}

type reviewerMetadataProvider interface {
	Metadata() ReviewVerdict
}

func reviewerMetadata(reviewer Reviewer, policyVersion string) ReviewVerdict {
	metadata := ReviewVerdict{SchemaVersion: 1, PolicyVersion: policyVersion}
	if provider, ok := reviewer.(reviewerMetadataProvider); ok {
		provided := provider.Metadata()
		metadata.ProviderID = provided.ProviderID
		metadata.ModelID = provided.ModelID
		metadata.Profile = provided.Profile
	}
	return metadata
}

func denyAuthorization(reason, code string) coreexec.Authorization {
	if reason == "" {
		reason = code
	}
	return coreexec.Authorization{Allowed: false, Reason: reason, ReasonCode: code, Decision: "deny"}
}

func canonicalMode(mode string) string {
	if mode == "trust" || mode == "ai_review" {
		return "review"
	}
	return mode
}

// PolicyMode 从 security.Policy 导出 mode 字符串。
func PolicyMode(policy *security.Policy) string {
	if policy == nil {
		return "normal"
	}
	switch policy.Mode {
	case security.ModeStrict:
		return "strict"
	case security.ModeTrust:
		return "review"
	case security.ModeYOLO:
		return "yolo"
	default:
		return "normal"
	}
}
