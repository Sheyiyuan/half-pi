// 执行 shell 命令。返回 stdout/stderr。
// Unix 平台使用 sh -c。
//
//go:build !windows

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/security"
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
				return executor.DecisionDeny, "failed to parse args"
			}
			decision, reason := security.Check(p.Command)
			switch decision {
			case security.Deny:
				return executor.DecisionDeny, fmt.Sprintf("blocked by security policy: %s", reason)
			case security.NeedApproval:
				return executor.DecisionConfirm, fmt.Sprintf("requires approval: %s", reason)
			default:
				return executor.DecisionAllow, ""
			}
		},
		PolicyCheck: policyCheckCommand,
		Execute:     executeWithShell("sh", "-c"),
	})
}

func policyCheckCommand(args json.RawMessage, policy *security.Policy) (executor.Decision, string) {
	var p struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Command == "" {
		return executor.DecisionDeny, "failed to parse args"
	}
	decision, reason := policy.Check(p.Command)
	switch decision {
	case security.Deny:
		return executor.DecisionDeny, fmt.Sprintf("blocked by security policy: %s", reason)
	case security.NeedApproval:
		return executor.DecisionConfirm, fmt.Sprintf("requires approval: %s", reason)
	default:
		return executor.DecisionAllow, ""
	}
}

func executeWithShell(shell string, shellArgs ...string) func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
	return func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
		var p struct {
			Command string  `json:"command"`
			Timeout float64 `json:"timeout"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return &executor.ToolResult{Error: fmt.Sprintf("failed to parse args: %v", err)}
		}
		if p.Command == "" {
			return &executor.ToolResult{Error: "command cannot be empty"}
		}
		if p.Timeout <= 0 {
			p.Timeout = 30
		}

		dur := time.Duration(p.Timeout) * time.Second
		cmdCtx, cancel := context.WithTimeout(ctx, dur)
		defer cancel()

		cmd := exec.CommandContext(cmdCtx, shell, append(shellArgs, p.Command)...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		stdout := commandOutput{ctx: ctx, kind: "stdout"}
		stderr := commandOutput{ctx: ctx, kind: "stderr"}
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := runCommand(cmdCtx, cmd)
		stdout.Flush()
		stderr.Flush()
		if err != nil {
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
	}
}

func runCommand(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		<-done
		return ctx.Err()
	}
}
