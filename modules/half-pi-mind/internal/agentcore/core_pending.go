package agentcore

import (
	"encoding/json"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
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
func (c *Core) CheckAndConfirm(toolName string, args json.RawMessage, llmConfirm bool) (blocked bool, reason string) {
	c.stateMu.Lock()
	tool, decision, reason, found := executor.CheckToolWithPolicy(toolName, args, c.policy)
	if !found {
		if !llmConfirm {
			c.stateMu.Unlock()
			return true, reason
		}
		decision = executor.DecisionConfirm
		reason = "Mind 无法验证远端专属工具，需用户确认"
	}
	if decision == executor.DecisionDeny {
		c.stateMu.Unlock()
		return true, reason
	}

	needsConfirm := llmConfirm || decision == executor.DecisionConfirm
	oneShotConfirm := llmConfirm || (found && tool.DefaultConfirm)
	if reason == "" && llmConfirm {
		reason = "LLM 标记为需确认操作"
	}
	if !needsConfirm {
		c.stateMu.Unlock()
		return false, reason
	}
	if c.autoDeny[toolName] {
		c.stateMu.Unlock()
		return true, reason
	}
	if c.autoAllow[toolName] && !oneShotConfirm {
		c.stateMu.Unlock()
		return false, reason
	}
	if c.Mode == "strict" || c.approver == nil {
		c.stateMu.Unlock()
		return true, reason
	}
	approver := c.approver
	c.stateMu.Unlock()

	result := approver.Confirm(toolName, reason)
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	switch result {
	case ConfirmAllow:
		return false, reason
	case ConfirmAllowAlways:
		if !oneShotConfirm {
			c.autoAllow[toolName] = true
		}
		return false, reason
	case ConfirmDenyAlways:
		c.autoDeny[toolName] = true
		return true, reason
	default:
		return true, reason
	}
}
