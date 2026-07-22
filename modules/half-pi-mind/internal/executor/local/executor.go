// Package local 提供 Mind 本地工具执行器和远程 Hand 路由工具。
package local

import (
	"context"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	_ "github.com/Sheyiyuan/half-pi/modules/half-pi-core/tools"
)

type LocalExecutor struct {
	bridge *RemoteBridge
}

// New 创建本地工具执行器。
func New(bridge ...*RemoteBridge) *LocalExecutor {
	var remote *RemoteBridge
	if len(bridge) > 0 {
		remote = bridge[0]
	}
	return &LocalExecutor{
		bridge: remote,
	}
}

// Tools 返回本地可用工具列表。
func (e *LocalExecutor) Tools() []executor.Tool {
	return executor.RegisteredTools()
}

// PrepareToolContext 注入远程执行桥，但不执行或绕过任何安全检查。
func (e *LocalExecutor) PrepareToolContext(ctx context.Context) context.Context {
	if e.bridge != nil {
		return WithRemoteBridge(ctx, e.bridge)
	}
	return ctx
}
