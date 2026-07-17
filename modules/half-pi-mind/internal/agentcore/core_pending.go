package agentcore

import (
	"encoding/json"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

// ActiveHand 返回当前会话默认 Hand ID。
func (c *Core) ActiveHand() string {
	return c.activeHand
}

// SetActiveHand 设置当前会话默认 Hand，同时持久化到 DB。
func (c *Core) SetActiveHand(handID string) error {
	if c.store != nil && c.sessionID != "" {
		if err := c.store.SetActiveHand(c.sessionID, handID); err != nil {
			return err
		}
	}
	c.activeHand = handID
	return nil
}

// CheckAndConfirm 执行工具安全检查并按需请求用户确认。
func (c *Core) CheckAndConfirm(toolName string, args json.RawMessage, llmConfirm bool) (blocked bool, reason string) {
	_, decision, reason, found := executor.CheckTool(toolName, args)
	if !found {
		return true, reason
	}
	if decision == executor.DecisionDeny {
		return true, reason
	}

	shouldBlock := false
	needsConfirm := llmConfirm || decision == executor.DecisionConfirm
	if reason == "" && llmConfirm {
		reason = "LLM 标记为需确认操作"
	}

	if needsConfirm {
		if c.autoDeny[toolName] {
			shouldBlock = true
		} else if c.autoAllow[toolName] {
			shouldBlock = false
		} else if c.Mode == "strict" {
			shouldBlock = true
		} else if llmConfirm || c.Mode == "normal" {
			if c.approver == nil {
				shouldBlock = true
			} else {
				r := c.approver.Confirm(toolName, reason)
				switch r {
				case ConfirmAllow:
					shouldBlock = false
				case ConfirmAllowAlways:
					shouldBlock = false
					c.autoAllow[toolName] = true
				case ConfirmDenyAlways:
					shouldBlock = true
					c.autoDeny[toolName] = true
				default:
					shouldBlock = true
				}
			}
		}
	}

	return shouldBlock, reason
}
