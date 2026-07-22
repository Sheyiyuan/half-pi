package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	coreexec "github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/security"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
)

func TestAIReviewerStrictVerdict(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{Response: llm.LLMResponse{Content: `{
		"schema_version":1,"decision":"allow","reason_code":"safe_operation",
		"summary":"bounded","policy_version":"v1"}`}})
	reviewer, err := NewAIReviewer(provider, ReviewerConfig{Timeout: time.Second, PolicyVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := reviewer.Review(context.Background(), ReviewRequest{SchemaVersion: 1, PolicyVersion: "v1"})
	if err != nil || verdict.Decision != ReviewAllow {
		t.Fatalf("review verdict = %+v, err=%v", verdict, err)
	}
}

func TestAIReviewerMalformedVerdictRequiresUser(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{Response: llm.LLMResponse{Content: `{"decision":"allow","extra":true}`}})
	reviewer, err := NewAIReviewer(provider, ReviewerConfig{Timeout: time.Second, PolicyVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := reviewer.Review(context.Background(), ReviewRequest{})
	if err == nil || verdict.Decision != ReviewRequireUser {
		t.Fatalf("malformed verdict produced allow: %+v, err=%v", verdict, err)
	}
}

func TestReviewModeUsesIndependentReviewer(t *testing.T) {
	const toolName = "review_mode_sensitive_tool"
	registerReviewerTool(t, toolName, "rm file.txt")
	reviewer := &countingReviewer{verdict: ReviewVerdict{
		SchemaVersion: 1, Decision: ReviewAllow, ReasonCode: "safe_operation", PolicyVersion: "v1",
	}}
	authorizer := NewMindAuthorizer("review", security.New(), nil)
	authorizer.SetReviewer(reviewer)
	result := authorizer.Authorize(context.Background(), reviewerFrozen(toolName, false))
	if !result.Allowed || reviewer.calls.Load() != 1 {
		t.Fatalf("review result = %+v, calls=%d", result, reviewer.calls.Load())
	}
}

func TestExplicitApprovalSkipsReviewer(t *testing.T) {
	const toolName = "review_mode_force_user_tool"
	registerReviewerTool(t, toolName, "rm file.txt")
	reviewer := &countingReviewer{verdict: ReviewVerdict{
		SchemaVersion: 1, Decision: ReviewAllow, ReasonCode: "safe_operation", PolicyVersion: "v1",
	}}
	authorizer := NewMindAuthorizer("review", security.New(), allowOnceApprover{})
	authorizer.SetReviewer(reviewer)
	result := authorizer.Authorize(context.Background(), reviewerFrozen(toolName, true))
	if !result.Allowed || result.ApprovalDecision != string(protocol.FaceApprovalAllowOnce) || reviewer.calls.Load() != 0 {
		t.Fatalf("explicit approval = %+v, reviewer calls=%d", result, reviewer.calls.Load())
	}
}

func TestHardDenySkipsReviewer(t *testing.T) {
	const toolName = "review_mode_hard_deny_tool"
	registerReviewerTool(t, toolName, "rm -rf /")
	reviewer := &countingReviewer{verdict: ReviewVerdict{Decision: ReviewAllow}}
	authorizer := NewMindAuthorizer("review", security.New(), allowOnceApprover{})
	authorizer.SetReviewer(reviewer)
	result := authorizer.Authorize(context.Background(), reviewerFrozen(toolName, false))
	if result.Allowed || reviewer.calls.Load() != 0 {
		t.Fatalf("hard deny = %+v, reviewer calls=%d", result, reviewer.calls.Load())
	}
}

func TestInvalidCustomReviewerVerdictEscalatesToUser(t *testing.T) {
	const toolName = "review_mode_invalid_verdict_tool"
	registerReviewerTool(t, toolName, "rm file.txt")
	reviewer := &countingReviewer{verdict: ReviewVerdict{
		SchemaVersion: 1, Decision: ReviewAllow, ReasonCode: "not_allowed", PolicyVersion: "v1",
	}}
	approver := &countingApprover{decision: protocol.FaceApprovalDenyOnce}
	authorizer := NewMindAuthorizer("review", security.New(), approver)
	authorizer.SetReviewer(reviewer)
	result := authorizer.Authorize(context.Background(), reviewerFrozen(toolName, false))
	if result.Allowed || result.ReasonCode != "user_denied" || reviewer.calls.Load() != 1 || approver.calls.Load() != 1 {
		t.Fatalf("invalid verdict result=%+v reviewer calls=%d approver calls=%d", result, reviewer.calls.Load(), approver.calls.Load())
	}
}

func TestReviewDecisionAuditFailureFailsClosed(t *testing.T) {
	const toolName = "review_mode_audit_failure_tool"
	registerReviewerTool(t, toolName, "rm file.txt")
	reviewer := &countingReviewer{verdict: ReviewVerdict{
		SchemaVersion: 1, Decision: ReviewAllow, ReasonCode: "safe_operation", PolicyVersion: "v1",
	}}
	authorizer := NewMindAuthorizer("review", security.New(), nil)
	authorizer.SetReviewer(reviewer)
	authorizer.SetReviewObserver(func(_ context.Context, phase corelifecycle.Phase, _ coreexec.FrozenInvocation, _ ReviewVerdict, _ error) error {
		if phase == corelifecycle.PhaseSecurityDecision {
			return errors.New("audit unavailable")
		}
		return nil
	})
	result := authorizer.Authorize(context.Background(), reviewerFrozen(toolName, false))
	if result.Allowed || result.ReasonCode != "audit_admission_failed" || reviewer.calls.Load() != 1 {
		t.Fatalf("audit failure result=%+v reviewer calls=%d", result, reviewer.calls.Load())
	}
}

func TestSensitiveReviewProjectionSkipsReviewerAndRequiresUser(t *testing.T) {
	const toolName = "review_mode_secret_projection_tool"
	coreexec.Register(coreexec.Tool{
		Name: toolName,
		Parameters: &coreexec.ObjectSchema{
			Properties: []coreexec.PropertySchema{{Name: "token", Type: "string", Review: coreexec.ReviewRequireUser}},
			Required:   []string{"token"},
		},
		PolicyCheck: func(_ json.RawMessage, _ *security.Policy) (coreexec.Decision, string) {
			return coreexec.DecisionConfirm, "sensitive"
		},
		Execute: func(context.Context, json.RawMessage) *coreexec.ToolResult {
			return &coreexec.ToolResult{Success: true}
		},
	})
	reviewer := &countingReviewer{verdict: ReviewVerdict{
		SchemaVersion: 1, Decision: ReviewAllow, ReasonCode: "safe_operation", PolicyVersion: "v1",
	}}
	approver := &countingApprover{decision: protocol.FaceApprovalAllowOnce}
	authorizer := NewMindAuthorizer("review", security.New(), approver)
	authorizer.SetReviewer(reviewer)
	frozen := reviewerFrozen(toolName, false)
	frozen.Args = json.RawMessage(`{"token":"must-not-reach-reviewer"}`)
	frozen.ArgsDigest = corelifecycle.HashDigest(frozen.Args)
	result := authorizer.Authorize(context.Background(), frozen)
	if !result.Allowed || reviewer.calls.Load() != 0 || approver.calls.Load() != 1 ||
		result.ApprovalDecision != string(protocol.FaceApprovalAllowOnce) {
		t.Fatalf("sensitive projection result=%+v reviewer calls=%d approver calls=%d", result, reviewer.calls.Load(), approver.calls.Load())
	}
}

type countingReviewer struct {
	calls   atomic.Int32
	verdict ReviewVerdict
	err     error
}

func (r *countingReviewer) Review(context.Context, ReviewRequest) (ReviewVerdict, error) {
	r.calls.Add(1)
	return r.verdict, r.err
}

type allowOnceApprover struct{}

func (allowOnceApprover) Confirm(_ context.Context, request approval.Request) approval.Resolution {
	return approval.Resolution{
		ApprovalID: "approval-review", ConversationID: request.ConversationID,
		Decision: protocol.FaceApprovalAllowOnce, Actor: approval.Actor{ID: "user", Source: "test"},
	}
}

type countingApprover struct {
	calls    atomic.Int32
	decision protocol.FaceApprovalDecision
}

func (a *countingApprover) Confirm(_ context.Context, request approval.Request) approval.Resolution {
	a.calls.Add(1)
	return approval.Resolution{
		ApprovalID: "approval-counted", ConversationID: request.ConversationID,
		Decision: a.decision, Actor: approval.Actor{ID: "user", Source: "test"},
	}
}

func registerReviewerTool(t *testing.T, name, command string) {
	t.Helper()
	coreexec.Register(coreexec.Tool{
		Name:       name,
		Parameters: &coreexec.ObjectSchema{Properties: []coreexec.PropertySchema{{Name: "command", Type: "string"}}, Required: []string{"command"}},
		PolicyCheck: func(_ json.RawMessage, policy *security.Policy) (coreexec.Decision, string) {
			decision, reason := policy.Check(command)
			return coreexec.Decision(decision), reason
		},
		Execute: func(context.Context, json.RawMessage) *coreexec.ToolResult {
			return &coreexec.ToolResult{Success: true}
		},
	})
}

func reviewerFrozen(tool string, force bool) coreexec.FrozenInvocation {
	meta := corelifecycle.NewMeta(corelifecycle.SourceMind).
		WithConversation("conversation-review").WithGroup("group-review").WithRequest("request-review")
	args := json.RawMessage(`{"command":"candidate"}`)
	return coreexec.FrozenInvocation{
		Meta: meta, Tool: tool, Args: args, ArgsDigest: corelifecycle.HashDigest(args), ForceUserApproval: force,
	}
}
