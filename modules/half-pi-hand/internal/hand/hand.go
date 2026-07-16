// Package hand Hand 远程执行节点：接收 Mind 的 RPC 请求，执行工具并返回结果。
package hand

import (
	"context"
	"fmt"
	"os"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"

	// 注册通用工具
	_ "github.com/Sheyiyuan/half-pi/modules/half-pi-core/tools"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/config"
)

// Hand 远程执行节点，通过 WebSocket 接收 Mind 的 RPC 请求。
type Hand struct {
	conn          *wss.SessionConn
	cfg           *config.Config
	checkedRunner *executor.Runner // 走 Tool.Check / DefaultConfirm，Confirm 自动拒绝
	trustedRunner *executor.Runner // Mind 已安检，跳过所有检查
}

// New 创建 Hand 实例。
func New(conn *wss.SessionConn, cfg *config.Config) *Hand {
	return &Hand{
		conn: conn,
		cfg:  cfg,
		checkedRunner: executor.NewRunner(executor.ExecutionPolicy{
			Confirm:    nil,
			SkipChecks: false,
		}),
		trustedRunner: executor.NewRunner(executor.ExecutionPolicy{
			Confirm:    nil,
			SkipChecks: true,
		}),
	}
}

// Serve 启动消息读取循环，阻塞直到连接断开或 ctx 取消。
func (h *Hand) Serve(ctx context.Context) error {
	h.startMonitors(ctx)

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = h.conn.Conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		env, err := h.conn.Read()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read: %w", err)
		}

		switch env.Type {
		case protocol.TypeRPC:
			go h.handleRPC(ctx, env)
		case protocol.TypeHandInfoReq:
			go h.handleHandInfoReq(env)
		case protocol.TypeError:
			msg, _ := protocol.DecodePayload[protocol.ErrorMsg](&env)
			fmt.Fprintf(os.Stderr, "server error [%s]: %s\n", msg.Code, msg.Message)
		default:
			fmt.Fprintf(os.Stderr, "unhandled message type: %s\n", env.Type)
		}
	}
}

// maxOutputSize 返回输出截断上限。
func (h *Hand) maxOutputSize() int64 {
	if h.cfg == nil {
		return 1 << 20
	}
	size := h.cfg.Hand.Limits.MaxOutputSize
	if size <= 0 {
		return 1 << 20
	}
	return size
}

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

func truncateBytes(s string, max int64) string {
	if int64(len(s)) <= max {
		return s
	}
	return s[:max] + "\n…(truncated)"
}
