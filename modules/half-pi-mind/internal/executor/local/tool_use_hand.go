package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
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
				{Name: "background", Type: "boolean", Description: "作为 Hand 持久化后台任务启动（强制确认）"},
				{Name: "task_timeout_ms", Type: "integer", Description: "后台任务最长运行毫秒数"},
			},
			Required: []string{"tool", "args"},
		},
		Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
			bridge := remoteBridgeFromContext(ctx)
			if bridge == nil || bridge.Runs == nil || bridge.Hub == nil || bridge.ActiveHand == nil || bridge.CheckAndConfirm == nil {
				return &executor.ToolResult{Error: "远程执行系统未初始化"}
			}

			var params struct {
				Tool          string         `json:"tool"`
				Args          map[string]any `json:"args"`
				HandID        string         `json:"hand_id"`
				Confirm       bool           `json:"confirm"`
				TimeoutMs     int            `json:"timeout_ms"`
				Background    bool           `json:"background"`
				TaskTimeoutMs int64          `json:"task_timeout_ms"`
			}
			decoder := json.NewDecoder(bytes.NewReader(args))
			decoder.UseNumber()
			if err := decoder.Decode(&params); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}
			}
			if err := decoder.Decode(&struct{}{}); err != io.EOF {
				return &executor.ToolResult{Error: "参数解析失败: JSON 后存在多余内容"}
			}
			if params.Tool == "" {
				return &executor.ToolResult{Error: "tool 参数不能为空"}
			}
			if params.Args == nil {
				return &executor.ToolResult{Error: "args 参数必须是对象"}
			}

			// 1. 确定目标 Hand
			handID := params.HandID
			if handID == "" {
				handID = bridge.ActiveHand()
			}
			if handID == "" {
				return &executor.ToolResult{Error: "未指定 Hand 且没有默认 Hand。请先用 select_hand 设置，或用 list_hands 查看可用设备。"}
			}

			peer := bridge.Hub.PeerByType(hub.PeerHand, handID)
			if peer == nil || peer.Type != hub.PeerHand {
				return &executor.ToolResult{Error: fmt.Sprintf("Hand %q 不在线或不存在", handID)}
			}

			// 2. 生成 run 并完成 Mind 侧安全检查
			runID := protocol.MustNewMsgID()
			cleanArgs, _ := json.Marshal(params.Args)
			_, knownLocally := executor.FindTool(params.Tool)
			blocked, reason := bridge.CheckAndConfirm(params.Tool, cleanArgs, remoteNeedsConfirmation(params.Confirm, params.Background, knownLocally))
			// 3. 超时
			timeoutMs := params.TimeoutMs
			if timeoutMs <= 0 {
				timeoutMs = 30000
			}
			timeout := time.Duration(timeoutMs) * time.Millisecond
			now := time.Now()
			deadline := now.Add(timeout + cancelAckTimeout)
			rpc := protocol.RPC{
				RunID: runID, Tool: params.Tool, Args: params.Args, DeadlineAt: deadline.UnixMilli(),
			}
			if params.Background {
				if bridge.Tasks == nil {
					return &executor.ToolResult{Error: "后台任务系统未初始化"}
				}
				if params.TaskTimeoutMs <= 0 || params.TaskTimeoutMs > protocol.MaxTaskRuntimeMS {
					return &executor.ToolResult{Error: fmt.Sprintf("task_timeout_ms 必须在 1 到 %d 之间", protocol.MaxTaskRuntimeMS)}
				}
				if bridge.SessionID == nil || bridge.SessionID() == "" {
					return &executor.ToolResult{Error: "后台任务需要有效会话"}
				}
				rpc.Background = &protocol.RPCBackgroundOptions{TaskID: runID, MaxRuntimeMS: params.TaskTimeoutMs}
			}
			digest, err := protocol.RPCApprovalDigest(rpc, handID)
			if err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("创建审批摘要失败: %v", err)}
			}
			// 4. 创建 run 并发送 RPC
			sessionID := ""
			mode := ""
			if bridge.SessionID != nil {
				sessionID = bridge.SessionID()
			}
			if bridge.Mode != nil {
				mode = bridge.Mode()
			}
			metadata := remoteexec.AuditMetadata{
				RequestID: requestctx.RequestID(ctx), ArgsDigest: digest,
				ApprovalSource: "mind", ApprovalMode: mode, ApprovalReason: reason,
			}
			var createErr error
			if params.Background && !blocked {
				createErr = bridge.Runs.CreateTaskForPeer(runID, sessionID, handID, peer.SessionID(), params.Tool, metadata, remoteexec.Task{})
			} else {
				createErr = bridge.Runs.CreateForPeer(runID, sessionID, handID, peer.SessionID(), params.Tool, metadata)
			}
			if createErr != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("创建远程执行记录失败: %v", createErr)}
			}
			if blocked {
				if err := bridge.Runs.RejectLocal(runID, protocol.RejectCheckFailed, reason); err != nil {
					return remoteRunFailure(bridge, runID, fmt.Sprintf("记录本地拒绝失败: %v", err))
				}
				view, _ := RemoteRunSnapshot(bridge, runID)
				return &executor.ToolResult{
					Success: true,
					Output:  fmt.Sprintf("[run=%s hand=%s status=%s] 操作被拒绝: %s", runID, handID, view.Status, reason),
					Data:    view,
				}
			}
			approval := &protocol.Approval{
				Approved: true, Source: "mind", Mode: mode, Reason: reason, OneShot: true,
				ArgsDigest: digest, ApprovedAt: now.UnixMilli(), ExpiresAt: deadline.UnixMilli(),
			}
			rpc.Approval = approval
			if err := bridge.Runs.Transition(runID, protocol.RunApproved); err != nil {
				if params.Background {
					return failBackgroundStart(bridge, runID, sessionID, "审批状态更新失败: "+err.Error())
				}
				return remoteRunFailure(bridge, runID, fmt.Sprintf("审批状态更新失败: %v", err))
			}
			done, release, _ := bridge.Runs.Wait(runID)
			defer release()

			rpcEnv, err := protocol.NewEnvelope("", protocol.TypeRPC, rpc)
			if err != nil {
				if rejectErr := bridge.Runs.RejectLocal(runID, protocol.RejectInvalidRequest, err.Error()); rejectErr != nil {
					return remoteRunFailure(bridge, runID, fmt.Sprintf("记录无效 RPC 失败: %v", rejectErr))
				}
				if params.Background {
					return failBackgroundStart(bridge, runID, sessionID, "创建后台 RPC 失败: "+err.Error())
				}
				return remoteRunResult(bridge, runID, handID, timeout)
			}

			if err := bridge.Runs.Transition(runID, protocol.RunSent); err != nil {
				if params.Background {
					return failBackgroundStart(bridge, runID, sessionID, "发送状态更新失败: "+err.Error())
				}
				return remoteRunFailure(bridge, runID, fmt.Sprintf("发送状态更新失败: %v", err))
			}
			operationCtx, stopOperation := context.WithTimeout(ctx, timeout)
			defer stopOperation()
			if err := bridge.Hub.SendPeerContext(operationCtx, peer, *rpcEnv); err != nil {
				if transitionErr := bridge.Runs.Transition(runID, protocol.RunLost); transitionErr != nil {
					return remoteRunFailure(bridge, runID, fmt.Sprintf("记录 RPC 发送失败: %v", transitionErr))
				}
				if params.Background {
					if ctx.Err() != nil {
						return cancelBackgroundStart(bridge, runID, sessionID)
					}
					return backgroundStartFailure(bridge, runID, sessionID, "后台任务发送状态未知: "+err.Error())
				}
				return remoteRunResult(bridge, runID, handID, timeout)
			}
			notifyRunStarted(ctx, runID)

			// 5. 等待终态；后台启动通过 task 协议对账，不使用前台 rpc_cancel。
			select {
			case <-done:
			case <-operationCtx.Done():
				if params.Background {
					if ctx.Err() != nil {
						return cancelBackgroundStart(bridge, runID, sessionID)
					}
					_ = bridge.Runs.MarkTimedOut(runID)
					return reconcileBackgroundStart(bridge, runID, sessionID, "后台任务启动确认超时")
				}
				reason := "timeout"
				if ctx.Err() != nil {
					reason = "user"
				}
				requestRemoteCancel(bridge, peer, runID, handID, reason)
			}
			result := remoteRunResult(bridge, runID, handID, timeout)
			if params.Background {
				run, ok := bridge.Runs.Snapshot(runID)
				if !ok || run.Result == nil || !result.Success {
					if ok && run.Status == protocol.RunRejected {
						return failBackgroundStart(bridge, runID, sessionID, result.Error)
					}
					return backgroundStartFailure(bridge, runID, sessionID, result.Error)
				}
				task, err := bridge.Tasks.ApplyStartResult(runID, sessionID, *run.Result)
				if err != nil {
					return &executor.ToolResult{Error: fmt.Sprintf("保存后台任务快照失败: %v", err)}
				}
				if task.Stale {
					return reconcileBackgroundStart(bridge, runID, sessionID, "后台任务已进入终态，正在补全快照")
				}
				return taskToolResult(task)
			}
			return result
		},
	})
}

func backgroundStartFailure(bridge *RemoteBridge, taskID, sessionID, message string) *executor.ToolResult {
	task, err := bridge.Tasks.MarkStartStale(taskID, sessionID, message)
	if err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("%s; 保存 stale 任务快照失败: %v", message, err)}
	}
	result := taskToolResult(task)
	result.Success = false
	result.Error = message
	return result
}

func failBackgroundStart(bridge *RemoteBridge, taskID, sessionID, message string) *executor.ToolResult {
	task, err := bridge.Tasks.FailStart(taskID, sessionID, message)
	if err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("%s; 保存后台任务终态失败: %v", message, err)}
	}
	result := taskToolResult(task)
	result.Success = false
	result.Error = message
	return result
}

func reconcileBackgroundStart(bridge *RemoteBridge, taskID, sessionID, message string) *executor.ToolResult {
	queryCtx, cancel := context.WithTimeout(context.Background(), taskQueryTimeout)
	defer cancel()
	task, err := bridge.Tasks.Get(queryCtx, sessionID, taskID)
	if err == nil {
		return taskToolResult(task)
	}
	return backgroundStartFailure(bridge, taskID, sessionID, fmt.Sprintf("%s: %v", message, err))
}

func cancelBackgroundStart(bridge *RemoteBridge, taskID, sessionID string) *executor.ToolResult {
	cancelCtx, cancel := context.WithTimeout(context.Background(), taskQueryTimeout)
	result, cancelErr := bridge.Tasks.Cancel(cancelCtx, sessionID, taskID, "user")
	cancel()
	_ = bridge.Runs.MarkLost(taskID, "background start cancelled by caller")
	if cancelErr != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("后台任务启动已取消，但无法确认任务已停止: %v", cancelErr)}
	}
	if result.Status != protocol.TaskCancelCancelled {
		return &executor.ToolResult{Error: fmt.Sprintf("后台任务启动等待已取消，但任务未被取消: %s", result.Status), Data: result}
	}
	return &executor.ToolResult{Error: "后台任务启动已取消，Hand 已确认取消"}
}

func remoteNeedsConfirmation(requested, background, knownLocally bool) bool {
	return requested || background || !knownLocally
}

func remoteRunFailure(bridge *RemoteBridge, runID, message string) *executor.ToolResult {
	bridge.Runs.FailClosed(runID, message)
	view, err := RemoteRunSnapshot(bridge, runID)
	if err != nil {
		return &executor.ToolResult{Error: message}
	}
	return &executor.ToolResult{Error: fmt.Sprintf("[run=%s status=%s] %s", runID, view.Status, message), Data: view}
}

const cancelAckTimeout = 3 * time.Second

func requestRemoteCancel(bridge *RemoteBridge, peer *hub.Peer, runID, handID, reason string) {
	requested, err := bridge.Runs.RequestCancel(runID, reason)
	if err == nil && !requested {
		return
	}
	env, err := protocol.NewEnvelope("", protocol.TypeRPCCancel, protocol.RPCCancel{RunID: runID, Reason: reason})
	if err != nil || bridge.Hub.SendPeerContext(context.Background(), peer, *env) != nil {
		if transitionErr := bridge.Runs.Transition(runID, protocol.RunLost); transitionErr != nil {
			bridge.Runs.FailClosed(runID, fmt.Sprintf("记录取消发送失败: %v", transitionErr))
		}
		return
	}
	done, _ := bridge.Runs.Done(runID)
	select {
	case <-done:
	case <-time.After(cancelAckTimeout):
		if err != nil {
			bridge.Runs.FailClosed(runID, fmt.Sprintf("记录取消请求失败: %v", err))
			return
		}
		if err := bridge.Runs.MarkCancelUnconfirmed(runID); err != nil {
			bridge.Runs.FailClosed(runID, fmt.Sprintf("记录取消超时失败: %v", err))
		}
	}
}

func remoteRunResult(bridge *RemoteBridge, runID, handID string, timeout time.Duration) *executor.ToolResult {
	run, ok := bridge.Runs.Snapshot(runID)
	if !ok {
		return &executor.ToolResult{Error: fmt.Sprintf("远程执行记录 %q 不存在", runID)}
	}
	view := remoteRunView(run)
	switch run.Status {
	case protocol.RunSucceeded, protocol.RunFailed:
		if run.Result == nil {
			return &executor.ToolResult{Error: fmt.Sprintf("[run=%s hand=%s status=%s] 远程执行缺少最终结果", runID, handID, run.Status), Data: view}
		}
		result := run.Result
		toolResult := &executor.ToolResult{
			Success: result.Success,
			Output: fmt.Sprintf("[run=%s hand=%s status=%s duration_ms=%d truncated=%t]\n%s",
				runID, handID, run.Status, view.DurationMs, view.Truncated, result.Output),
			Error: result.Error,
			Data:  view,
		}
		if toolResult.Error != "" {
			toolResult.Error = fmt.Sprintf("[run=%s hand=%s status=%s] %s", runID, handID, run.Status, toolResult.Error)
		}
		return toolResult
	case protocol.RunRejected:
		if run.Rejection != nil {
			return &executor.ToolResult{Error: fmt.Sprintf("[run=%s hand=%s status=%s] Hand 拒绝执行 (%s): %s", runID, handID, run.Status, run.Rejection.Code, run.Rejection.Reason), Data: view}
		}
	case protocol.RunCancelled:
		return &executor.ToolResult{Error: fmt.Sprintf("[run=%s hand=%s status=%s] 操作已取消", runID, handID, run.Status), Data: view}
	case protocol.RunTimedOut:
		return &executor.ToolResult{Error: fmt.Sprintf("[run=%s hand=%s status=%s] 执行超时 (%v)", runID, handID, run.Status, timeout), Data: view}
	case protocol.RunLost:
		return &executor.ToolResult{Error: fmt.Sprintf("[run=%s hand=%s status=%s] 连接中断，执行状态未知", runID, handID, run.Status), Data: view}
	}
	return &executor.ToolResult{Error: fmt.Sprintf("[%s] 远程执行未进入终态: %s", handID, run.Status), Data: view}
}
