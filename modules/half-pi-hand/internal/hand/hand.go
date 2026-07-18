// Package hand Hand 远程执行节点：接收 Mind 的 RPC 请求，执行工具并返回结果。
package hand

import (
	"context"
	"fmt"
	"os"
	"sync"
	"unicode/utf8"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"

	// 注册通用工具
	_ "github.com/Sheyiyuan/half-pi/modules/half-pi-core/tools"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/taskmanager"
)

// Hand 远程执行节点，通过 WebSocket 接收 Mind 的 RPC 请求。
type Hand struct {
	conn        *wss.SessionConn
	cfg         *config.Config
	send        func(string, any) error
	taskManager *taskmanager.Manager

	tasksMu sync.Mutex
	tasks   map[string]*task
}

// New 创建 Hand 实例。
func New(conn *wss.SessionConn, cfg *config.Config) *Hand {
	return NewWithTaskManager(conn, cfg, nil)
}

// NewWithTaskManager 创建共享进程级后台任务管理器的 Hand 实例。
func NewWithTaskManager(conn *wss.SessionConn, cfg *config.Config, manager *taskmanager.Manager) *Hand {
	return &Hand{
		conn: conn, cfg: cfg, taskManager: manager, tasks: make(map[string]*task),
	}
}

// Serve 启动消息读取循环，阻塞直到连接断开或 ctx 取消。
func (h *Hand) Serve(ctx context.Context) error {
	runCtx, cancelRuns := context.WithCancel(ctx)
	defer cancelRuns()
	h.startMonitors(runCtx)

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
			go h.handleRPC(runCtx, env)
		case protocol.TypeRPCCancel:
			go h.handleRPCCancel(env)
		case protocol.TypeTaskStatusReq:
			go h.handleTaskStatus(env)
		case protocol.TypeTaskLogReq:
			go h.handleTaskLog(env)
		case protocol.TypeTaskCancel:
			go h.handleTaskCancel(env)
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

func (h *Hand) maxProgressSize() int64 {
	return min(h.maxOutputSize(), int64(protocol.MaxRPCProgressBytes))
}

func (h *Hand) sendRPCReply(runID string, success bool, output, errMsg string, truncated bool) error {
	reply := protocol.RPCResult{
		RunID:     runID,
		Success:   success,
		Output:    output,
		Error:     errMsg,
		Truncated: truncated,
	}
	return h.sendRPCMessage(protocol.TypeRPCResult, reply)
}

func (h *Hand) sendRPCMessage(typ string, payload any) error {
	if h.send != nil {
		return h.send(typ, payload)
	}
	env, err := protocol.NewEnvelope("", typ, payload)
	if err != nil {
		return fmt.Errorf("create %s: %w", typ, err)
	}
	if err := h.conn.Send(*env); err != nil {
		return fmt.Errorf("send %s: %w", typ, err)
	}
	return nil
}

func truncateBytes(s string, max int64) (string, bool) {
	if int64(len(s)) <= max {
		return s, false
	}
	end := int(max)
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end] + "\n…(truncated)", true
}
