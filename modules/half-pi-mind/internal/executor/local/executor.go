package local

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor"
)

type LocalExecutor struct{}

func New() *LocalExecutor {
	return &LocalExecutor{}
}

func (e *LocalExecutor) Tools() []executor.Tool {
	return executor.RegisteredTools()
}

func (e *LocalExecutor) ExecuteTool(ctx context.Context, name string, args json.RawMessage) *executor.ToolResult {
	for _, t := range executor.RegisteredTools() {
		if t.Name == name {
			return t.Execute(ctx, args)
		}
	}
	return &executor.ToolResult{Error: fmt.Sprintf("unknown tool: %s", name)}
}
