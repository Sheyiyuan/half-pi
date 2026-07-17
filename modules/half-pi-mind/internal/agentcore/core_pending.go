package agentcore

import (
	"encoding/json"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

// pendingCall 记录一个等待中的 Hand 响应及其预期来源。
type pendingCall struct {
	ch           chan protocol.Envelope
	expectedPeer string
	accepted     bool
}

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

// PendingCall 注册等待 Hand 响应的通道。返回只读通道和取消函数。
// expectedPeer 用于校验响应来源，不匹配时拒绝投递。
func (c *Core) PendingCall(id string, timeout time.Duration, expectedPeer string) (<-chan protocol.Envelope, func()) {
	ch := make(chan protocol.Envelope, 2)
	pc := &pendingCall{ch: ch, expectedPeer: expectedPeer}
	c.pendingMu.Lock()
	c.pendingCalls[id] = pc
	c.pendingMu.Unlock()

	cancel := func() {
		c.pendingMu.Lock()
		delete(c.pendingCalls, id)
		c.pendingMu.Unlock()
	}

	if timeout > 0 {
		go func() {
			<-time.After(timeout)
			cancel()
		}()
	}

	return ch, cancel
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
