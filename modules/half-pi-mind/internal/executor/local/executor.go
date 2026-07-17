// Package local 提供 Mind 本地工具执行器和远程 Hand 路由工具。
package local

import (
	"context"
	"encoding/json"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	_ "github.com/Sheyiyuan/half-pi/modules/half-pi-core/tools"
)

type LocalExecutor struct {
	runner *executor.Runner
	bridge *RemoteBridge
}

// New 创建本地工具执行器。
func New(bridge ...*RemoteBridge) *LocalExecutor {
	var remote *RemoteBridge
	if len(bridge) > 0 {
		remote = bridge[0]
	}
	return &LocalExecutor{
		runner: executor.NewRunner(executor.ExecutionPolicy{SkipChecks: true}),
		bridge: remote,
	}
}

// Tools 返回本地可用工具列表。
func (e *LocalExecutor) Tools() []executor.Tool {
	return e.runner.Tools()
}

// ExecuteTool 执行本地工具。
func (e *LocalExecutor) ExecuteTool(ctx context.Context, name string, args json.RawMessage) *executor.ToolResult {
	if e.bridge != nil {
		ctx = WithRemoteBridge(ctx, e.bridge)
	}
	return e.runner.ExecuteTool(ctx, name, args)
}
