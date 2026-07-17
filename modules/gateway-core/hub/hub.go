// Package hub 管理 Mind WebSocket Hub，负责节点注册、消息路由和防重放校验。
package hub

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// PeerType 标识连接节点的角色。
type PeerType = string

const (
	// PeerFace 表示用户交互节点。
	PeerFace PeerType = "face"
	// PeerHand 表示远程执行节点。
	PeerHand PeerType = "hand"
	// PeerUnknown 表示未识别或未注册节点。
	PeerUnknown PeerType = ""

	// DefaultHubID 是 Mind Hub 的默认节点 ID。
	DefaultHubID = "mind"

	writeTimeout = 10 * time.Second
	readTimeout  = 60 * time.Second
	pingInterval = 30 * time.Second
	pongTimeout  = 10 * time.Second
)

// Hub 管理所有 WebSocket 节点和消息投递。
type Hub struct {
	ID           string
	mu           sync.RWMutex
	peers        map[string]*Peer
	handshakeFn  func(peer *Peer, env protocol.Envelope) error
	onMessage    func(peer *Peer, env protocol.Envelope)
	disconnectFn func(peer *Peer)
}

// New 创建一个新的 Hub。
func New() *Hub {
	return &Hub{
		ID:    DefaultHubID,
		peers: make(map[string]*Peer),
	}
}

// OnHandshake 设置节点注册握手回调。token 验证由回调负责。
func (h *Hub) OnHandshake(fn func(peer *Peer, env protocol.Envelope) error) {
	h.handshakeFn = fn
}

// OnMessage 设置接收业务消息的回调。
func (h *Hub) OnMessage(fn func(peer *Peer, env protocol.Envelope)) {
	h.onMessage = fn
}

// OnDisconnect 设置节点断开回调。
func (h *Hub) OnDisconnect(fn func(peer *Peer)) {
	h.disconnectFn = fn
}

// ServeWS 接受 WebSocket 连接并启动 Peer 的读循环。
func (h *Hub) ServeWS(conn *websocket.Conn) error {
	_, raw, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return fmt.Errorf("read handshake: %w", err)
	}

	peer, err := h.Register(raw, conn)
	if err != nil {
		env, envErr := protocol.NewEnvelope("", protocol.TypeError, protocol.ErrorMsg{
			Code:    "register_failed",
			Message: err.Error(),
		})
		if envErr == nil {
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			_ = conn.WriteJSON(env)
		}
		conn.Close()
		return err
	}

	registered, err := protocol.NewEnvelope("", protocol.TypeRegistered, protocol.Registered{
		ClientID:  peer.ID,
		ServerID:  h.ID,
		SessionID: peer.SessionID(),
	})
	if err != nil {
		h.RemovePeer(peer)
		return err
	}
	if err := h.Send(peer.ID, *registered); err != nil {
		h.RemovePeer(peer)
		return err
	}

	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pingInterval + pongTimeout))
		return nil
	})

	go peer.Serve()
	return nil
}

// Register 注册新节点。第一个消息必须为 register 类型。
func (h *Hub) Register(raw []byte, conn *websocket.Conn) (*Peer, error) {
	var env protocol.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("invalid handshake: %w", err)
	}
	if env.Type != protocol.TypeRegister {
		return nil, fmt.Errorf("first message must be register, got %q", env.Type)
	}

	var reg protocol.Register
	if err := json.Unmarshal(env.Payload, &reg); err != nil {
		return nil, fmt.Errorf("invalid register payload: %w", err)
	}
	if reg.ClientID == "" {
		return nil, fmt.Errorf("client_id is required")
	}
	if reg.Type != PeerFace && reg.Type != PeerHand {
		return nil, fmt.Errorf("peer type must be face or hand, got %q", reg.Type)
	}
	sessionID, err := newSessionID()
	if err != nil {
		return nil, err
	}
	session, err := protocol.NewSession(h.ID, reg.ClientID, sessionID)
	if err != nil {
		return nil, err
	}

	peer := &Peer{
		ID:       reg.ClientID,
		Type:     reg.Type,
		Conn:     conn,
		JoinedAt: time.Now(),
		hub:      h,
		session:  session,
	}

	if reg.Info.OS != "" {
		peer.Info = &reg.Info
	}

	if h.handshakeFn != nil {
		if err := h.handshakeFn(peer, env); err != nil {
			return nil, err
		}
	}

	h.mu.Lock()
	old, exists := h.peers[peer.ID]
	if exists {
		h.mu.Unlock()
		old.Close()
		h.mu.Lock()
	}
	h.peers[peer.ID] = peer
	h.mu.Unlock()

	return peer, nil
}

// Remove 移除并关闭指定 ID 的节点。
func (h *Hub) Remove(id string) {
	h.mu.Lock()
	peer, ok := h.peers[id]
	if ok {
		delete(h.peers, id)
	}
	h.mu.Unlock()
	if ok {
		if h.disconnectFn != nil {
			h.disconnectFn(peer)
		}
		peer.Close()
	}
}

// RemovePeer 仅当当前注册的 peer 正是 target 时才移除。
func (h *Hub) RemovePeer(target *Peer) {
	h.mu.Lock()
	current, ok := h.peers[target.ID]
	if ok && current == target {
		delete(h.peers, target.ID)
	}
	h.mu.Unlock()
	if ok && current == target {
		if h.disconnectFn != nil {
			h.disconnectFn(target)
		}
		target.Close()
	}
}

// Send 向指定节点发送消息，自动 stamp outgoing。
func (h *Hub) Send(peerID string, env protocol.Envelope) error {
	h.mu.RLock()
	peer, ok := h.peers[peerID]
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("peer %q not found", peerID)
	}
	stamped, err := peer.stampOutgoing(env)
	if err != nil {
		return err
	}
	return peer.WriteJSON(stamped)
}

// Broadcast 向除排除节点外的所有节点广播消息。
// 锁内复制快照，锁外发送，慢连接不阻塞注册/移除。
func (h *Hub) Broadcast(env protocol.Envelope, excludeID string) {
	h.mu.RLock()
	snapshot := make([]*Peer, 0, len(h.peers))
	for id, peer := range h.peers {
		if id != excludeID {
			snapshot = append(snapshot, peer)
		}
	}
	h.mu.RUnlock()

	for _, peer := range snapshot {
		stamped, err := peer.stampOutgoing(env)
		if err == nil {
			err = peer.WriteJSON(stamped)
		}
		if err != nil {
			h.RemovePeer(peer)
		}
	}
}

// Accept 校验来自 peer 的业务消息上下文和单调序号。
// Register 首包不走该路径；连接建立后每条业务消息必须使用当前 peer 的 SessionID。
func (h *Hub) Accept(peer *Peer, env protocol.Envelope) error {
	if peer == nil {
		return fmt.Errorf("peer is nil")
	}
	if env.Type == "" {
		return fmt.Errorf("message type is required")
	}
	if env.Type == protocol.TypeRegister {
		return fmt.Errorf("register message is only allowed as handshake")
	}
	if env.SessionID != peer.SessionID() {
		return fmt.Errorf("session_id mismatch")
	}
	if env.From != peer.ID {
		return fmt.Errorf("from mismatch: got %q, want %q", env.From, peer.ID)
	}
	if env.To != h.ID {
		return fmt.Errorf("to mismatch: got %q, want %q", env.To, h.ID)
	}
	return peer.session.Accept(env)
}

// Count 返回当前连接数。
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.peers)
}

// Peers 返回所有在线 peer 的 ID 快照。
func (h *Hub) Peers() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]string, 0, len(h.peers))
	for id := range h.peers {
		ids = append(ids, id)
	}
	return ids
}

// Peer 按 ID 返回单个 peer，不存在时返回 nil。
func (h *Hub) Peer(id string) *Peer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.peers[id]
}

// PeersByType 返回指定类型的在线 peer 快照。
func (h *Hub) PeersByType(pt PeerType) []*Peer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]*Peer, 0, len(h.peers))
	for _, p := range h.peers {
		if p.Type == pt {
			result = append(result, p)
		}
	}
	return result
}

func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
