package agentcore

import (
	"context"

	coreexec "github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
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
	c.stateMu.Unlock()
	c.notifySessionChanged()
	return nil
}

// PrepareRemoteTool 让远程真实工具复用当前会话的 ToolRuntime admission。
func (c *Core) PrepareRemoteTool(ctx context.Context, invocation coreexec.Invocation, definition coreexec.Tool, digest coreexec.ExternalDigestFunc) (*coreexec.PreparedExternal, coreexec.Result) {
	c.stateMu.RLock()
	runtime := c.toolRuntime
	c.stateMu.RUnlock()
	return runtime.PrepareExternal(ctx, invocation, definition, digest)
}
