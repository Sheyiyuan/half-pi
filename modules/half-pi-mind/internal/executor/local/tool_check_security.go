// 查询安全策略对某条命令的检查结果，不实际执行。
// LLM 可在执行命令前先查询，避免被拒后重新规划。
package local

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/security"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "check_security",
		Description: "查询安全策略对命令的检查结果，不实际执行。用于执行敏感命令前预检",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "command", Type: "string", Description: "要检查的 shell 命令"},
			},
			Required: []string{"command"},
		},
		Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
			var p struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("failed to parse args: %v", err)}
			}
			if p.Command == "" {
				return &executor.ToolResult{Error: "command cannot be empty"}
			}

			decision, reason := security.Check(p.Command)
			switch decision {
			case security.Deny:
				return &executor.ToolResult{
					Success: true,
					Output:  fmt.Sprintf("❌ 拒绝: %s", reason),
				}
			case security.NeedApproval:
				return &executor.ToolResult{
					Success: true,
					Output:  fmt.Sprintf("⚠️ 需确认: %s", reason),
				}
			default:
				return &executor.ToolResult{
					Success: true,
					Output:  "✅ 允许执行",
				}
			}
		},
	})
}
