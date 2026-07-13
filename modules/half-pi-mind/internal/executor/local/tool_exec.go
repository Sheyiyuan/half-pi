// 执行 shell 命令。返回 stdout/stderr。
package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/security"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "exec_command",
		Description: "执行 shell 命令，返回命令输出",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "command", Type: "string", Description: "要执行的 shell 命令"},
				{Name: "timeout", Type: "number", Description: "超时秒数，默认 30"},
			},
			Required: []string{"command"},
		},
		Check: func(args json.RawMessage) (executor.Decision, string) {
			var p struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(args, &p); err != nil || p.Command == "" {
				return executor.DecisionDeny, "参数解析失败"
			}
			decision, reason := security.Check(p.Command)
			switch decision {
			case security.Deny:
				return executor.DecisionDeny, fmt.Sprintf("操作被安全策略拒绝: %s", reason)
			case security.NeedApproval:
				return executor.DecisionConfirm, fmt.Sprintf("需确认: %s", reason)
			default:
				return executor.DecisionAllow, ""
			}
		},
		Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
			var p struct {
				Command string  `json:"command"`
				Timeout float64 `json:"timeout"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}
			}
			if p.Command == "" {
				return &executor.ToolResult{Error: "command 不能为空"}
			}
			if p.Timeout <= 0 {
				p.Timeout = 30
			}

			dur := time.Duration(p.Timeout) * time.Second
			cmdCtx, cancel := context.WithTimeout(ctx, dur)
			defer cancel()

			sh := exec.CommandContext(cmdCtx, "sh", "-c", p.Command)
			var stdout, stderr bytes.Buffer
			sh.Stdout = &stdout
			sh.Stderr = &stderr

			if err := sh.Run(); err != nil {
				msg := fmt.Sprintf("执行失败: %v", err)
				if stderr.Len() > 0 {
					msg += "\n" + strings.TrimSpace(stderr.String())
				}
				return &executor.ToolResult{Error: msg}
			}

			output := strings.TrimSpace(stdout.String())
			if stderr.Len() > 0 {
				se := strings.TrimSpace(stderr.String())
				if output != "" {
					output += "\n" + se
				} else {
					output = se
				}
			}
			return &executor.ToolResult{Success: true, Output: output}
		},
	})
}
