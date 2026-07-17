// Peer WebSocket 连接封装，提供线程安全的读写和生命周期管理。
package hub

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// Peer 封装 WebSocket 连接及其元数据。
type Peer struct {
	ID       string
	Type     PeerType
	Conn     *websocket.Conn
	JoinedAt time.Time
	hub      *Hub
	session  *protocol.Session
	Info     *protocol.HandInfo // Hand 注册时上报的静态设备信息

	writeOnce sync.Once
	writeGate chan struct{} // 保护 Conn 写入，gorilla/websocket 要求单 writer
	closeOnce sync.Once
}

// SessionID 返回当前连接的会话 ID。
func (p *Peer) SessionID() string {
	if p == nil || p.session == nil {
		return ""
	}
	return p.session.SessionID
}

// WriteJSON 线程安全地写 JSON 到 WebSocket 连接，带写入超时。
func (p *Peer) WriteJSON(v any) error {
	return p.WriteJSONContext(context.Background(), v)
}

// WriteJSONContext 线程安全地写 JSON，并允许上下文中断阻塞写入。
func (p *Peer) WriteJSONContext(ctx context.Context, v any) error {
	if err := p.acquireWriter(ctx); err != nil {
		return err
	}
	defer p.releaseWriter()
	return p.writeJSONLocked(ctx, v)
}

// SendContext 在同一写锁内分配序号并发送，保证线序与写入顺序一致。
func (p *Peer) SendContext(ctx context.Context, env protocol.Envelope) error {
	if err := p.acquireWriter(ctx); err != nil {
		return err
	}
	defer p.releaseWriter()
	stamped, err := p.stampOutgoing(env)
	if err != nil {
		return err
	}
	return p.writeJSONLocked(ctx, stamped)
}

func (p *Peer) writeJSONLocked(ctx context.Context, v any) error {
	deadline := time.Now().Add(writeTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := p.Conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	interruptDone := make(chan struct{})
	stopInterrupt := context.AfterFunc(ctx, func() {
		_ = p.Conn.SetWriteDeadline(time.Now())
		close(interruptDone)
	})
	defer func() {
		if !stopInterrupt() {
			<-interruptDone
		}
	}()
	return p.Conn.WriteJSON(v)
}

// Close 关闭连接并清理。
func (p *Peer) Close() {
	p.closeOnce.Do(func() {
		if err := p.acquireWriter(context.Background()); err != nil {
			return
		}
		defer p.releaseWriter()
		p.Conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		_ = p.Conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		_ = p.Conn.Close()
	})
}

// Serve 启动读循环：读取消息 → Accept 校验 → onMessage。
// 阻塞直到连接断开。
func (p *Peer) Serve() {
	defer p.hub.RemovePeer(p)

	p.Conn.SetReadLimit(1 << 20)

	go p.keepalive()

	for {
		p.Conn.SetReadDeadline(time.Now().Add(readTimeout))
		_, raw, err := p.Conn.ReadMessage()
		if err != nil {
			return
		}

		var env protocol.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			p.sendError("", "parse_error", "invalid message format")
			continue
		}
		if err := p.hub.Accept(p, env); err != nil {
			p.sendError(env.MsgID, "rejected", err.Error())
			continue
		}
		if p.hub.onMessage != nil {
			p.hub.onMessage(p, env)
		}
	}
}

func (p *Peer) keepalive() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for range ticker.C {
		if err := p.acquireWriter(context.Background()); err != nil {
			return
		}
		p.Conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		err := p.Conn.WriteMessage(websocket.PingMessage, nil)
		p.releaseWriter()
		if err != nil {
			return
		}
	}
}

func (p *Peer) acquireWriter(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.writeOnce.Do(func() {
		p.writeGate = make(chan struct{}, 1)
		p.writeGate <- struct{}{}
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.writeGate:
		return nil
	}
}

func (p *Peer) releaseWriter() {
	p.writeGate <- struct{}{}
}

func (p *Peer) sendError(msgID, code, message string) {
	env, err := protocol.NewEnvelope("", protocol.TypeError, protocol.ErrorMsg{
		MsgID:   msgID,
		Code:    code,
		Message: message,
	})
	if err != nil {
		return
	}
	_ = p.SendContext(context.Background(), *env)
}

func (p *Peer) stampOutgoing(env protocol.Envelope) (protocol.Envelope, error) {
	return p.session.Stamp(env)
}
