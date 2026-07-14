// Package hand Hand 远程执行节点：接收 Mind 的 RPC 请求，执行工具并返回结果。
package hand

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"

	// 注册通用工具
	_ "github.com/Sheyiyuan/half-pi/modules/half-pi-core/tools"
)

// Hand 远程执行节点，通过 WebSocket 接收 Mind 的 RPC 请求。
type Hand struct {
	conn   *wss.SessionConn
	runner *executor.Runner
}

// New 创建 Hand 实例。Confirm 为 nil，确认类操作自动拒绝。
func New(conn *wss.SessionConn) *Hand {
	return &Hand{
		conn: conn,
		runner: executor.NewRunner(executor.ExecutionPolicy{
			Confirm:    nil,
			SkipChecks: false,
		}),
	}
}

// Serve 启动消息读取循环，阻塞直到连接断开或 ctx 取消。
func (h *Hand) Serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		env, err := h.conn.Read()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		switch env.Type {
		case protocol.TypeRPC:
			h.handleRPC(env)
		case protocol.TypeError:
			msg, _ := protocol.DecodePayload[protocol.ErrorMsg](&env)
			fmt.Fprintf(os.Stderr, "server error [%s]: %s\n", msg.Code, msg.Message)
		default:
			fmt.Fprintf(os.Stderr, "unhandled message type: %s\n", env.Type)
		}
	}
}

// handleRPC 处理工具执行请求，执行后回送 RPCResult。
func (h *Hand) handleRPC(env protocol.Envelope) {
	rpc, err := protocol.DecodePayload[protocol.RPC](&env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode rpc failed: %v\n", err)
		return
	}

	args, err := json.Marshal(rpc.Args)
	if err != nil {
		h.sendRPCReply(rpc.ID, env.MsgID, false, "", fmt.Sprintf("marshal args: %v", err))
		return
	}

	result := h.runner.ExecuteTool(context.Background(), rpc.Tool, args)
	h.sendRPCReply(rpc.ID, env.MsgID, result.Success, result.Output, result.Error)
}

// sendRPCReply 发送 RPCResult 回执。
func (h *Hand) sendRPCReply(rpcID, msgID string, success bool, output, errMsg string) {
	reply := protocol.RPCResult{
		ID:      rpcID,
		Success: success,
		Output:  output,
		Error:   errMsg,
	}
	env, err := protocol.NewEnvelope(msgID, protocol.TypeRPCResult, reply)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create rpc result failed: %v\n", err)
		return
	}
	if err := h.conn.Send(*env); err != nil {
		fmt.Fprintf(os.Stderr, "send rpc result failed: %v\n", err)
	}
}
