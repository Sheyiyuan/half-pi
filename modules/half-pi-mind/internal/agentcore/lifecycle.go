package agentcore

import (
	"context"
	"errors"
	"fmt"
	"time"

	coreexec "github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	mindlifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
)

const maxChatMessageBytes = 1 << 20

func (c *Core) newChatMeta(ctx context.Context) corelifecycle.Meta {
	c.stateMu.RLock()
	sessionID, groupID := c.sessionID, c.groupID
	c.stateMu.RUnlock()
	source := requestctx.Source(ctx)
	if source == "" {
		source = corelifecycle.SourceMind
	}
	return corelifecycle.NewMeta(source).
		WithConversation(sessionID).
		WithGroup(groupID).
		WithRequest(requestctx.RequestID(ctx)).
		WithPrincipal(requestctx.PrincipalID(ctx)).
		WithNode("mind")
}

// ObserveApprovalRequested 把 Approval Broker 的权威 pending 事实接入会话 lifecycle。
func (c *Core) ObserveApprovalRequested(observation approval.Observation) {
	request, meta := observation.Request, observation.Meta
	c.publishLifecycle(context.Background(), meta, corelifecycle.PhaseApprovalRequested, corelifecycle.RedactedEvent{
		ResourceName: request.Tool, InputDigest: request.ArgsDigest,
		ApprovalID: request.ApprovalID, RunID: request.RunID,
	})
}

// ObserveApprovalFinished 把 Approval Broker 的唯一终态接入会话 lifecycle。
func (c *Core) ObserveApprovalFinished(observation approval.Observation, status approval.Status, resolution approval.Resolution) {
	request := observation.Request
	phase := corelifecycle.PhaseApprovalResolved
	outcome := corelifecycle.OutcomeSucceeded
	reasonCode := string(resolution.Decision)
	switch status {
	case approval.StatusExpired:
		phase, outcome, reasonCode = corelifecycle.PhaseApprovalExpired, corelifecycle.OutcomeTimedOut, "approval_expired"
	case approval.StatusCancelled:
		phase, outcome, reasonCode = corelifecycle.PhaseApprovalCancelled, corelifecycle.OutcomeCancelled, "approval_cancelled"
	}
	meta := observation.Meta
	c.publishLifecycle(context.Background(), meta, phase, corelifecycle.RedactedEvent{
		Outcome: outcome, ResourceName: request.Tool, InputDigest: request.ArgsDigest,
		ReasonCode: reasonCode, ApprovalID: request.ApprovalID, RunID: request.RunID,
	})
}

func (c *Core) lifecycleRegistry() *corelifecycle.LifecycleRegistry {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.lifecycle
}

func (c *Core) publishLifecycle(ctx context.Context, base corelifecycle.Meta, phase corelifecycle.Phase, event corelifecycle.RedactedEvent) {
	registry := c.lifecycleRegistry()
	if registry == nil {
		return
	}
	event.Meta = registry.NextEventMeta(base)
	event.Phase = phase
	registry.Publish(ctx, event)
}

func (c *Core) commitLifecycleAudit(ctx context.Context, base corelifecycle.Meta, phase corelifecycle.Phase, mutation corelifecycle.AuditMutation) error {
	registry := c.lifecycleRegistry()
	if registry == nil {
		return nil
	}
	mutation.Meta = registry.NextEventMeta(base)
	return registry.Commit(ctx, phase, mutation)
}

func lifecycleOutcome(ctx context.Context, err error) corelifecycle.Outcome {
	if err == nil {
		return corelifecycle.OutcomeSucceeded
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return corelifecycle.OutcomeTimedOut
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return corelifecycle.OutcomeCancelled
	}
	return corelifecycle.OutcomeFailed
}

func durationMillis(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

func (c *Core) observeSecurityReview(ctx context.Context, phase corelifecycle.Phase, frozen coreexec.FrozenInvocation, verdict mindlifecycle.ReviewVerdict, reviewErr error) error {
	reasonCode := verdict.ReasonCode
	outcome := corelifecycle.OutcomeSucceeded
	if reviewErr != nil {
		outcome = corelifecycle.OutcomeFailed
		if reasonCode == "" {
			reasonCode = "reviewer_failed"
		}
	}
	summary := verdict.Summary
	if summary == "" && reviewErr != nil {
		summary = fmt.Sprintf("security review failed: %s", reasonCode)
	}
	c.publishLifecycle(ctx, frozen.Meta, phase, corelifecycle.RedactedEvent{
		Outcome: outcome, ResourceName: frozen.Tool, InputDigest: frozen.ArgsDigest,
		ReasonCode: reasonCode, Summary: summary, RunID: frozen.RunID,
		ProviderID: verdict.ProviderID, ModelID: verdict.ModelID, Profile: verdict.Profile,
		PolicyVersion: verdict.PolicyVersion, DurationMS: verdict.DurationMS,
	})
	if phase != corelifecycle.PhaseSecurityDecision {
		return nil
	}
	decision := verdict.Decision
	if decision == "" {
		decision = mindlifecycle.ReviewRequireUser
	}
	return c.commitLifecycleAudit(ctx, frozen.Meta, phase, corelifecycle.AuditMutation{
		ActionKind: "security_review", ResourceName: frozen.Tool,
		TargetNode: frozen.TargetNode, InputDigest: frozenAuthorizationDigestForAudit(frozen),
		RiskLabels: append([]string(nil), frozen.RiskLabels...), Decision: decision,
		ReasonCode: reasonCode, PolicyVersion: verdict.PolicyVersion, RunID: frozen.RunID,
		Details: map[string]any{
			"provider_id": verdict.ProviderID, "model_id": verdict.ModelID, "profile": verdict.Profile,
			"duration_ms": verdict.DurationMS, "failure_code": verdict.FailureCode,
		},
	})
}

func frozenAuthorizationDigestForAudit(frozen coreexec.FrozenInvocation) string {
	if frozen.ApprovalDigest != "" {
		return frozen.ApprovalDigest
	}
	return frozen.ArgsDigest
}
