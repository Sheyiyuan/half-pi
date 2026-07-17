package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "use_hand",
		Description: "在指定 Hand 上执行工具。若不指定 hand_id，使用 select_hand 设置的默认 Hand。",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "tool", Type: "string", Description: "要执行的工具名称"},
				{Name: "args", Type: "object", Description: "工具参数"},
				{Name: "hand_id", Type: "string", Description: "目标 Hand ID，不传则用默认 Hand"},
				{Name: "confirm", Type: "boolean", Description: "设为 true 会在执行前请求用户确认"},
				{Name: "timeout_ms", Type: "integer", Description: "超时毫秒数（默认 30000）"},
			},
			Required: []string{"tool", "args"},
		},
		Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
			if remoteBridge == nil || remoteBridge.Runs == nil {
				return &executor.ToolResult{Error: "远程执行系统未初始化"}
			}

			var params struct {
				Tool      string         `json:"tool"`
				Args      map[string]any `json:"args"`
				HandID    string         `json:"hand_id"`
				Confirm   bool           `json:"confirm"`
				TimeoutMs int            `json:"timeout_ms"`
			}
			decoder := json.NewDecoder(bytes.NewReader(args))
			decoder.UseNumber()
			if err := decoder.Decode(&params); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}
			}
			if params.Tool == "" {
				return &executor.ToolResult{Error: "tool 参数不能为空"}
			}

			// 1. 确定目标 Hand
			handID := params.HandID
			if handID == "" {
				handID = remoteBridge.ActiveHand()
			}
			if handID == "" {
				return &executor.ToolResult{Error: "未指定 Hand 且没有默认 Hand。请先用 select_hand 设置，或用 list_hands 查看可用设备。"}
			}

			peer := remoteBridge.Hub.Peer(handID)
			if peer == nil || peer.Type != hub.PeerHand {
				return &executor.ToolResult{Error: fmt.Sprintf("Hand %q 不在线或不存在", handID)}
			}

			// 2. 生成 run 并完成 Mind 侧安全检查
			runID := protocol.MustNewMsgID()
			cleanArgs, _ := json.Marshal(params.Args)
			blocked, reason := remoteBridge.CheckAndConfirm(params.Tool, cleanArgs, params.Confirm)
			if blocked {
				return &executor.ToolResult{
					Success: true,
					Output:  fmt.Sprintf("⚠️ 操作被拒绝: %s", reason),
				}
			}
			// 3. 超时
			timeoutMs := params.TimeoutMs
			if timeoutMs <= 0 {
				timeoutMs = 30000
			}
			timeout := time.Duration(timeoutMs) * time.Millisecond
			now := time.Now()
			deadline := now.Add(timeout + cancelAckTimeout)
			digest, err := protocol.ApprovalDigest(runID, handID, params.Tool, params.Args)
			if err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("创建审批摘要失败: %v", err)}
			}
			approval := &protocol.Approval{
				Approved:   true,
				Source:     "mind",
				Reason:     reason,
				OneShot:    true,
				ArgsDigest: digest,
				ApprovedAt: now.UnixMilli(),
				ExpiresAt:  deadline.UnixMilli(),
			}

			// 4. 创建 run 并发送 RPC
			sessionID := ""
			if remoteBridge.SessionID != nil {
				sessionID = remoteBridge.SessionID()
			}
			if err := remoteBridge.Runs.Create(runID, sessionID, handID, params.Tool); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("创建远程执行记录失败: %v", err)}
			}
			if err := remoteBridge.Runs.Transition(runID, protocol.RunApproved); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("审批状态更新失败: %v", err)}
			}
			done, release, _ := remoteBridge.Runs.Wait(runID)
			defer release()

			rpcEnv, err := protocol.NewEnvelope("", protocol.TypeRPC, protocol.RPC{
				RunID:      runID,
				Tool:       params.Tool,
				Args:       params.Args,
				DeadlineAt: deadline.UnixMilli(),
				Approval:   approval,
			})
			if err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("创建 RPC 失败: %v", err)}
			}

			if err := remoteBridge.Runs.Transition(runID, protocol.RunSent); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("发送状态更新失败: %v", err)}
			}
			operationCtx, stopOperation := context.WithTimeout(ctx, timeout)
			defer stopOperation()
			if err := remoteBridge.Hub.SendContext(operationCtx, handID, *rpcEnv); err != nil {
				_ = remoteBridge.Runs.Transition(runID, protocol.RunLost)
				return &executor.ToolResult{Error: fmt.Sprintf("发送 RPC 失败: %v", err)}
			}

			// 5. 等待终态；本地取消或超时会显式通知 Hand。
			select {
			case <-done:
			case <-operationCtx.Done():
				reason := "timeout"
				if ctx.Err() != nil {
					reason = "user"
				}
				requestRemoteCancel(runID, handID, reason)
			}
			return remoteRunResult(runID, handID, params.Tool, timeout)
		},
	})
}

const cancelAckTimeout = 3 * time.Second

func requestRemoteCancel(runID, handID, reason string) {
	if !remoteBridge.Runs.RequestCancel(runID, reason) {
		return
	}
	env, err := protocol.NewEnvelope("", protocol.TypeRPCCancel, protocol.RPCCancel{RunID: runID, Reason: reason})
	if err != nil || remoteBridge.Hub.Send(handID, *env) != nil {
		_ = remoteBridge.Runs.Transition(runID, protocol.RunLost)
		return
	}
	done, _ := remoteBridge.Runs.Done(runID)
	select {
	case <-done:
	case <-time.After(cancelAckTimeout):
		remoteBridge.Runs.MarkCancelUnconfirmed(runID)
	}
}

func remoteRunResult(runID, handID, tool string, timeout time.Duration) *executor.ToolResult {
	run, ok := remoteBridge.Runs.Snapshot(runID)
	if !ok {
		return &executor.ToolResult{Error: fmt.Sprintf("远程执行记录 %q 不存在", runID)}
	}
	switch run.Status {
	case protocol.RunSucceeded, protocol.RunFailed:
		if run.Result == nil {
			return &executor.ToolResult{Error: fmt.Sprintf("[%s] 远程执行缺少最终结果", handID)}
		}
		result := run.Result
		toolResult := &executor.ToolResult{
			Success: result.Success,
			Output:  fmt.Sprintf("[%s] %s", handID, result.Output),
			Error:   result.Error,
			Data: map[string]any{
				"run_id": runID, "hand_id": handID, "tool": tool,
				"status": run.Status, "truncated": result.Truncated,
			},
		}
		if toolResult.Error != "" {
			toolResult.Error = fmt.Sprintf("[%s] %s", handID, toolResult.Error)
		}
		return toolResult
	case protocol.RunRejected:
		if run.Rejection != nil {
			return &executor.ToolResult{Error: fmt.Sprintf("[%s] Hand 拒绝执行 (%s): %s", handID, run.Rejection.Code, run.Rejection.Reason), Data: run.Rejection}
		}
	case protocol.RunCancelled:
		return &executor.ToolResult{Error: fmt.Sprintf("[%s] 操作已取消", handID), Data: run}
	case protocol.RunTimedOut:
		return &executor.ToolResult{Error: fmt.Sprintf("Hand %q 执行超时 (%v)", handID, timeout), Data: run}
	case protocol.RunLost:
		return &executor.ToolResult{Error: fmt.Sprintf("Hand %q 连接中断，执行状态未知", handID), Data: run}
	}
	return &executor.ToolResult{Error: fmt.Sprintf("[%s] 远程执行未进入终态: %s", handID, run.Status), Data: run}
}
