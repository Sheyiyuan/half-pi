package local

import (
	"context"
	"encoding/json"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	_ "github.com/Sheyiyuan/half-pi/modules/half-pi-core/tools"
)

type LocalExecutor struct {
	runner *executor.Runner
}

func New() *LocalExecutor {
	return &LocalExecutor{
		runner: executor.NewRunner(executor.ExecutionPolicy{SkipChecks: true}),
	}
}

func (e *LocalExecutor) Tools() []executor.Tool {
	return e.runner.Tools()
}

func (e *LocalExecutor) ExecuteTool(ctx context.Context, name string, args json.RawMessage) *executor.ToolResult {
	return e.runner.ExecuteTool(ctx, name, args)
}
