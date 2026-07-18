package agentcore

import (
	"context"
	"encoding/json"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
)

// ActiveHand 返回当前会话默认 Hand ID。
func (c *Core) ActiveHand() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.activeHand
}

// SetActiveHand 设置当前会话默认 Hand，同时持久化到 DB。
func (c *Core) SetActiveHand(handID string) error {
	c.stateMu.Lock()
	if c.store != nil && c.sessionID != "" {
		if err := c.store.SetActiveHand(c.sessionID, handID); err != nil {
			c.stateMu.Unlock()
			return err
		}
	}
	c.activeHand = handID
	observer := c.sessionChanged
	c.stateMu.Unlock()
	if observer != nil {
		observer()
	}
	return nil
}

// CheckAndConfirm 执行工具安全检查并按需请求用户确认。
func (c *Core) CheckAndConfirm(ctx context.Context, toolName string, args json.RawMessage, llmConfirm bool) (blocked bool, reason string) {
	result := c.checkAndConfirm(ctx, "", toolName, args, approval.ArgsDigest(args), llmConfirm)
	return result.Blocked, result.Reason
}

// CheckAndConfirmRun 为远程 run 执行安全检查并保留完整审批裁决元数据。
func (c *Core) CheckAndConfirmRun(ctx context.Context, runID, toolName string, args json.RawMessage, argsDigest string, llmConfirm bool) approval.CheckResult {
	if argsDigest == "" {
		argsDigest = approval.ArgsDigest(args)
	}
	return c.checkAndConfirm(ctx, runID, toolName, args, argsDigest, llmConfirm)
}

func (c *Core) checkAndConfirm(ctx context.Context, runID, toolName string, args json.RawMessage, argsDigest string, llmConfirm bool) approval.CheckResult {
	if ctx == nil {
		ctx = context.Background()
	}
	c.stateMu.Lock()
	tool, decision, reason, found := executor.CheckToolWithPolicy(toolName, args, c.policy)
	if !found {
		if !llmConfirm {
			c.stateMu.Unlock()
			return approval.CheckResult{Blocked: true, Reason: reason}
		}
		decision = executor.DecisionConfirm
		reason = "Mind 无法验证远端专属工具，需用户确认"
	}
	if decision == executor.DecisionDeny {
		c.stateMu.Unlock()
		return approval.CheckResult{Blocked: true, Reason: reason}
	}

	needsConfirm := llmConfirm || decision == executor.DecisionConfirm
	oneShotConfirm := llmConfirm || (found && tool.DefaultConfirm)
	if reason == "" && llmConfirm {
		reason = "LLM 标记为需确认操作"
	}
	if !needsConfirm {
		c.stateMu.Unlock()
		return approval.CheckResult{Reason: reason}
	}
	if c.autoDeny[toolName] {
		c.stateMu.Unlock()
		return approval.CheckResult{Blocked: true, Reason: reason}
	}
	if c.autoAllow[toolName] && !oneShotConfirm {
		c.stateMu.Unlock()
		return approval.CheckResult{Reason: reason}
	}
	if c.Mode == "strict" || c.approver == nil {
		c.stateMu.Unlock()
		return approval.CheckResult{Blocked: true, Reason: reason}
	}
	approver := c.approver
	conversationID := c.sessionID
	c.stateMu.Unlock()

	if reason == "" {
		reason = "Tool requires confirmation"
	}
	resolution := approver.Confirm(ctx, approval.Request{
		ConversationID: conversationID, RequestID: requestctx.RequestID(ctx), RunID: runID,
		Tool: toolName, Reason: reason, ArgsDigest: argsDigest,
	})
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	result := approval.CheckResult{Blocked: true, Reason: reason, Resolution: resolution}
	switch resolution.Decision {
	case protocol.FaceApprovalAllowOnce:
		result.Blocked = false
	case protocol.FaceApprovalAllowSession:
		if !oneShotConfirm {
			c.autoAllow[toolName] = true
		}
		result.Blocked = false
	case protocol.FaceApprovalDenySession:
		c.autoDeny[toolName] = true
	}
	return result
}
