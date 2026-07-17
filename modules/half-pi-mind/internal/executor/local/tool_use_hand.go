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
			if remoteBridge == nil {
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
			deadline := now.Add(timeout)
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

			// 4. 发送 RPC
			ch, cancel := remoteBridge.PendingCall(runID, 0, handID)
			defer cancel()

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

			if err := remoteBridge.Hub.Send(handID, *rpcEnv); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("发送 RPC 失败: %v", err)}
			}

			// 5. 等待结果
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			for {
				select {
				case env := <-ch:
					switch env.Type {
					case protocol.TypeRPCAccepted:
						continue
					case protocol.TypeRPCRejected:
						rejected, err := protocol.DecodePayload[protocol.RPCRejected](&env)
						if err != nil {
							return &executor.ToolResult{Error: fmt.Sprintf("解析 RPC 拒绝失败: %v", err)}
						}
						return &executor.ToolResult{
							Error: fmt.Sprintf("[%s] Hand 拒绝执行 (%s): %s", handID, rejected.Code, rejected.Reason),
							Data:  rejected,
						}
					case protocol.TypeRPCResult:
						result, err := protocol.DecodePayload[protocol.RPCResult](&env)
						if err != nil {
							return &executor.ToolResult{Error: fmt.Sprintf("解析 RPC 结果失败: %v", err)}
						}
						toolResult := &executor.ToolResult{
							Success: result.Success,
							Output:  fmt.Sprintf("[%s] %s", handID, result.Output),
							Error:   result.Error,
							Data:    result,
						}
						if toolResult.Error != "" {
							toolResult.Error = fmt.Sprintf("[%s] %s", handID, toolResult.Error)
						}
						return toolResult
					}
				case <-timer.C:
					return &executor.ToolResult{Error: fmt.Sprintf("Hand %q 执行超时 (%v)", handID, timeout)}
				case <-ctx.Done():
					return &executor.ToolResult{Error: "操作已取消"}
				}
			}
		},
	})
}
