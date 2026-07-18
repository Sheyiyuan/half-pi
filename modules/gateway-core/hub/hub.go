// Package hub 管理 Mind WebSocket Hub，负责加密节点注册、消息路由和防重放校验。
package hub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
)

// PeerType 标识连接节点的角色。
type PeerType = protocol.PeerType

const (
	PeerFace             = protocol.PeerFace
	PeerHand             = protocol.PeerHand
	PeerUnknown PeerType = ""

	DefaultHubID = "mind"

	writeTimeout     = 10 * time.Second
	readTimeout      = 60 * time.Second
	pingInterval     = 30 * time.Second
	pongTimeout      = 10 * time.Second
	handshakeTimeout = 10 * time.Second
)

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// PeerKey 是 Hub registry 使用的复合身份。
type PeerKey struct {
	Type  PeerType
	Label string
}

// Authentication 是握手认证结果；application key 仅用于派生会话密钥。
type Authentication struct {
	ApplicationKey string
	PrincipalID    string
}

// Authenticator 验证 label/token，并返回 application key 与稳定主体 ID。
type Authenticator func(key PeerKey, register protocol.Register) (Authentication, error)

// HandshakeError 是客户端可据此决定是否重试的握手错误。
type HandshakeError struct {
	Code string
}

func (e *HandshakeError) Error() string { return e.Code }

// Hub 管理所有 WebSocket 节点和消息投递。
type Hub struct {
	ID           string
	mu           sync.RWMutex
	peers        map[PeerKey]*Peer
	reservations map[PeerKey]*Peer
	pendingConns map[*websocket.Conn]PeerKey
	closed       bool
	authenticate Authenticator
	connectFn    func(peer *Peer)
	onMessage    func(peer *Peer, env protocol.Envelope)
	disconnectFn func(peer *Peer)
}

// New 创建一个新的 Hub。
func New() *Hub {
	return &Hub{
		ID:           DefaultHubID,
		peers:        make(map[PeerKey]*Peer),
		reservations: make(map[PeerKey]*Peer),
		pendingConns: make(map[*websocket.Conn]PeerKey),
	}
}

// OnHandshake 设置凭据认证回调。
func (h *Hub) OnHandshake(fn Authenticator) { h.authenticate = fn }

// OnMessage 设置接收已解密业务消息的回调。
func (h *Hub) OnMessage(fn func(peer *Peer, env protocol.Envelope)) { h.onMessage = fn }

// OnConnect 设置节点完成注册后的回调。
func (h *Hub) OnConnect(fn func(peer *Peer)) { h.connectFn = fn }

// OnDisconnect 设置节点断开回调。
func (h *Hub) OnDisconnect(fn func(peer *Peer)) { h.disconnectFn = fn }

// ServeWS 完成四步挑战握手，并在成功后启动加密读循环。
func (h *Hub) ServeWS(conn *websocket.Conn) error {
	if err := h.trackPending(conn); err != nil {
		return err
	}
	defer h.untrackPending(conn)
	deadline := time.Now().Add(handshakeTimeout)
	conn.SetReadLimit(wss.MaxFrameSize)
	_ = conn.SetReadDeadline(deadline)

	_, reg, err := readRegister(conn)
	if err != nil {
		return h.failHandshake(conn, handshakeCode(err), err)
	}
	key := PeerKey{Type: reg.Type, Label: reg.ClientID}
	if err := h.bindPending(conn, key); err != nil {
		_ = conn.Close()
		return err
	}
	authentication, err := h.authenticateRegister(key, reg)
	if err != nil {
		return h.failHandshake(conn, "authentication_failed", err)
	}

	transcript, challenge, err := h.newChallenge(key, deadline)
	if err != nil {
		_ = conn.Close()
		return err
	}
	challengeEnv, err := protocol.NewEnvelope("", protocol.TypeRegisterChallenge, challenge)
	if err != nil {
		_ = conn.Close()
		return err
	}
	_ = conn.SetWriteDeadline(deadline)
	if err := conn.WriteJSON(challengeEnv); err != nil {
		_ = conn.Close()
		return fmt.Errorf("write register challenge: %w", err)
	}

	proof, err := readProof(conn)
	if err != nil {
		return h.failHandshake(conn, handshakeCode(err), err)
	}
	keys, err := wss.DeriveSessionKeys(authentication.ApplicationKey, transcript)
	if err != nil || time.Now().After(deadline) || wss.VerifyRegisterProof(keys, transcript, proof) != nil {
		return h.failHandshake(conn, "authentication_failed", fmt.Errorf("invalid register proof"))
	}

	peer, err := h.reservePeer(key, reg, conn, transcript.SessionID, keys, authentication.PrincipalID)
	if err != nil {
		return h.failHandshake(conn, "duplicate_peer", err)
	}
	registered := protocol.Registered{
		ProtocolVersion: protocol.ProtocolVersion,
		ClientID:        key.Label,
		ServerID:        h.ID,
		SessionID:       transcript.SessionID,
	}
	registeredEnv, err := protocol.NewEnvelope("", protocol.TypeRegistered, registered)
	if err == nil {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		err = peer.SendContext(ctx, *registeredEnv)
		cancel()
	}
	if err != nil {
		h.releaseReservation(peer)
		peer.Close()
		return fmt.Errorf("send registered: %w", err)
	}
	if err := h.promotePeer(peer); err != nil {
		h.releaseReservation(peer)
		peer.Close()
		return err
	}
	if h.connectFn != nil {
		h.connectFn(peer)
	}

	_ = conn.SetReadDeadline(time.Time{})
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pingInterval + pongTimeout))
	})
	go peer.Serve()
	return nil
}

func (h *Hub) trackPending(conn *websocket.Conn) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		_ = conn.Close()
		return fmt.Errorf("hub is closed")
	}
	h.pendingConns[conn] = PeerKey{}
	return nil
}

func (h *Hub) bindPending(conn *websocket.Conn, key PeerKey) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fmt.Errorf("hub is closed")
	}
	if _, ok := h.pendingConns[conn]; !ok {
		return fmt.Errorf("handshake was revoked")
	}
	h.pendingConns[conn] = key
	return nil
}

func (h *Hub) untrackPending(conn *websocket.Conn) {
	h.mu.Lock()
	delete(h.pendingConns, conn)
	h.mu.Unlock()
}

func readRegister(conn *websocket.Conn) (protocol.Envelope, protocol.Register, error) {
	var zero protocol.Register
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return protocol.Envelope{}, zero, fmt.Errorf("read register: %w", err)
	}
	env, err := protocol.StrictDecode[protocol.Envelope](raw)
	if err != nil || env.Type != protocol.TypeRegister {
		return protocol.Envelope{}, zero, fmt.Errorf("invalid register envelope")
	}
	reg, err := protocol.DecodePayload[protocol.Register](&env)
	if err != nil {
		return protocol.Envelope{}, zero, fmt.Errorf("invalid register payload: %w", err)
	}
	if reg.ProtocolVersion != protocol.ProtocolVersion {
		return protocol.Envelope{}, zero, &HandshakeError{Code: "unsupported_protocol"}
	}
	if err := validateRegister(reg, env.Payload); err != nil {
		return protocol.Envelope{}, zero, err
	}
	return env, reg, nil
}

func validateRegister(reg protocol.Register, payload json.RawMessage) error {
	if reg.Type != PeerHand && reg.Type != PeerFace {
		return fmt.Errorf("invalid peer type")
	}
	if !labelPattern.MatchString(reg.ClientID) {
		return fmt.Errorf("invalid client label")
	}
	token, err := hex.DecodeString(reg.Token)
	if err != nil || len(token) != 16 || hex.EncodeToString(token) != reg.Token {
		return fmt.Errorf("invalid token")
	}
	if reg.Type == PeerFace {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(payload, &fields); err != nil {
			return fmt.Errorf("invalid register payload")
		}
		if _, present := fields["info"]; present {
			return fmt.Errorf("face must omit info")
		}
	}
	if reg.Type == PeerHand && (reg.Info == nil || reg.Info.OS == "" || reg.Info.Arch == "" || reg.Info.Hostname == "") {
		return fmt.Errorf("hand info is required")
	}
	return nil
}

func (h *Hub) authenticateRegister(key PeerKey, reg protocol.Register) (Authentication, error) {
	if h.authenticate == nil {
		return Authentication{}, fmt.Errorf("authenticator is not configured")
	}
	authentication, err := h.authenticate(key, reg)
	if err != nil {
		return Authentication{}, err
	}
	decoded, err := hex.DecodeString(authentication.ApplicationKey)
	if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != authentication.ApplicationKey {
		return Authentication{}, fmt.Errorf("invalid application key")
	}
	if authentication.PrincipalID == "" {
		return Authentication{}, fmt.Errorf("invalid principal ID")
	}
	return authentication, nil
}

func (h *Hub) newChallenge(key PeerKey, deadline time.Time) (protocol.HandshakeTranscript, protocol.RegisterChallenge, error) {
	handshakeID, err := randomHex(16)
	if err != nil {
		return protocol.HandshakeTranscript{}, protocol.RegisterChallenge{}, err
	}
	sessionID, err := randomHex(16)
	if err != nil {
		return protocol.HandshakeTranscript{}, protocol.RegisterChallenge{}, err
	}
	challengeBytes := make([]byte, 32)
	if _, err := rand.Read(challengeBytes); err != nil {
		return protocol.HandshakeTranscript{}, protocol.RegisterChallenge{}, fmt.Errorf("generate challenge: %w", err)
	}
	challengeText := base64.StdEncoding.EncodeToString(challengeBytes)
	challenge := protocol.RegisterChallenge{
		ProtocolVersion: protocol.ProtocolVersion,
		HandshakeID:     handshakeID,
		ServerID:        h.ID,
		SessionID:       sessionID,
		Challenge:       challengeText,
		ExpiresAt:       deadline.UnixMilli(),
		Algorithm:       protocol.HandshakeAlgorithm,
	}
	transcript := protocol.HandshakeTranscript{
		ProtocolVersion: protocol.ProtocolVersion,
		PeerType:        key.Type,
		Label:           key.Label,
		HandshakeID:     handshakeID,
		ServerID:        h.ID,
		SessionID:       sessionID,
		Challenge:       challengeText,
	}
	return transcript, challenge, nil
}

func readProof(conn *websocket.Conn) (protocol.RegisterProof, error) {
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return protocol.RegisterProof{}, fmt.Errorf("read register proof: %w", err)
	}
	env, err := protocol.StrictDecode[protocol.Envelope](raw)
	if err != nil || env.Type != protocol.TypeRegisterProof {
		return protocol.RegisterProof{}, fmt.Errorf("invalid register proof envelope")
	}
	proof, err := protocol.DecodePayload[protocol.RegisterProof](&env)
	if err != nil {
		return protocol.RegisterProof{}, fmt.Errorf("invalid register proof: %w", err)
	}
	if proof.ProtocolVersion != protocol.ProtocolVersion {
		return protocol.RegisterProof{}, &HandshakeError{Code: "unsupported_protocol"}
	}
	return proof, nil
}

func handshakeCode(err error) string {
	if handshakeErr, ok := err.(*HandshakeError); ok {
		return handshakeErr.Code
	}
	return "authentication_failed"
}

func (h *Hub) failHandshake(conn *websocket.Conn, code string, cause error) error {
	env, err := protocol.NewEnvelope("", protocol.TypeError, protocol.ErrorMsg{Code: code, Message: code})
	if err == nil {
		_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		_ = conn.WriteJSON(env)
	}
	_ = conn.Close()
	return &HandshakeError{Code: code}
}

func (h *Hub) reservePeer(key PeerKey, reg protocol.Register, conn *websocket.Conn, sessionID string, keys wss.SessionKeys, principalID string) (*Peer, error) {
	session, err := protocol.NewSession(h.ID, key.Label, sessionID)
	if err != nil {
		return nil, err
	}
	inCipher, err := wss.NewAES128GCM(keys.ClientToServer[:])
	if err != nil {
		return nil, err
	}
	outCipher, err := wss.NewAES128GCM(keys.ServerToClient[:])
	if err != nil {
		return nil, err
	}
	peer := &Peer{
		ID: key.Label, Type: key.Type, PrincipalID: principalID,
		Conn: conn, JoinedAt: time.Now(), hub: h, session: session,
		inCipher: inCipher, outCipher: outCipher, Info: reg.Info,
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, fmt.Errorf("hub is closed")
	}
	if _, exists := h.peers[key]; exists {
		return nil, fmt.Errorf("peer already connected")
	}
	if _, exists := h.reservations[key]; exists {
		return nil, fmt.Errorf("peer registration in progress")
	}
	h.reservations[key] = peer
	return peer, nil
}

func (h *Hub) promotePeer(peer *Peer) error {
	key := peer.Key()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.reservations[key] != peer {
		return fmt.Errorf("peer reservation lost")
	}
	delete(h.reservations, key)
	h.peers[key] = peer
	return nil
}

func (h *Hub) releaseReservation(peer *Peer) {
	h.mu.Lock()
	if h.reservations[peer.Key()] == peer {
		delete(h.reservations, peer.Key())
	}
	h.mu.Unlock()
}

// RemoveByType 移除并关闭指定复合身份的节点。
func (h *Hub) RemoveByType(peerType PeerType, label string) {
	h.removeKey(PeerKey{Type: peerType, Label: label})
}

func (h *Hub) removeKey(key PeerKey) {
	h.mu.Lock()
	peer, ok := h.peers[key]
	if ok {
		delete(h.peers, key)
	}
	reserved := h.reservations[key]
	if reserved != nil {
		delete(h.reservations, key)
	}
	pending := make([]*websocket.Conn, 0, 1)
	for conn, pendingKey := range h.pendingConns {
		if pendingKey == key {
			delete(h.pendingConns, conn)
			pending = append(pending, conn)
		}
	}
	h.mu.Unlock()
	for _, conn := range pending {
		_ = conn.Close()
	}
	if reserved != nil && reserved != peer {
		reserved.Close()
	}
	if ok {
		if h.disconnectFn != nil {
			h.disconnectFn(peer)
		}
		peer.Close()
	}
}

// RemovePeer 仅当当前注册的 peer 正是 target 时才移除。
func (h *Hub) RemovePeer(target *Peer) {
	if target == nil {
		return
	}
	key := target.Key()
	h.mu.Lock()
	current, ok := h.peers[key]
	if ok && current == target {
		delete(h.peers, key)
	}
	h.mu.Unlock()
	if ok && current == target {
		if h.disconnectFn != nil {
			h.disconnectFn(target)
		}
		target.Close()
	}
}

// SendToType 向指定复合身份发送消息。
func (h *Hub) SendToType(peerType PeerType, label string, env protocol.Envelope) error {
	return h.SendToTypeContext(context.Background(), peerType, label, env)
}

// SendToTypeContext 向指定复合身份发送消息，并允许上下文取消。
func (h *Hub) SendToTypeContext(ctx context.Context, peerType PeerType, label string, env protocol.Envelope) error {
	peer := h.PeerByType(peerType, label)
	if peer == nil {
		return fmt.Errorf("peer %s/%q not found", peerType, label)
	}
	return peer.SendContext(ctx, env)
}

// SendPeerContext 仅当 target 仍是当前连接时发送。
func (h *Hub) SendPeerContext(ctx context.Context, target *Peer, env protocol.Envelope) error {
	if target == nil {
		return fmt.Errorf("target peer is nil")
	}
	h.mu.RLock()
	current := h.peers[target.Key()]
	h.mu.RUnlock()
	if current != target {
		return fmt.Errorf("peer %s/%q connection changed", target.Type, target.ID)
	}
	return target.SendContext(ctx, env)
}

// Broadcast 向除指定复合身份外的所有节点广播消息。
func (h *Hub) Broadcast(env protocol.Envelope, exclude *PeerKey) {
	h.mu.RLock()
	snapshot := make([]*Peer, 0, len(h.peers))
	for key, peer := range h.peers {
		if exclude == nil || key != *exclude {
			snapshot = append(snapshot, peer)
		}
	}
	h.mu.RUnlock()
	for _, peer := range snapshot {
		if err := peer.SendContext(context.Background(), env); err != nil {
			h.RemovePeer(peer)
		}
	}
}

// Count 返回当前可路由连接数。
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.peers)
}

// Peers 返回所有在线 peer 的复合身份快照。
func (h *Hub) Peers() []PeerKey {
	h.mu.RLock()
	defer h.mu.RUnlock()
	keys := make([]PeerKey, 0, len(h.peers))
	for key := range h.peers {
		keys = append(keys, key)
	}
	return keys
}

// PeerByType 按复合身份返回 peer。
func (h *Hub) PeerByType(peerType PeerType, label string) *Peer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.peers[PeerKey{Type: peerType, Label: label}]
}

// PeersByType 返回指定类型的在线 peer 快照。
func (h *Hub) PeersByType(peerType PeerType) []*Peer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]*Peer, 0, len(h.peers))
	for key, peer := range h.peers {
		if key.Type == peerType {
			result = append(result, peer)
		}
	}
	return result
}

// Close 关闭所有在线和握手中的连接。
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	peers := make([]*Peer, 0, len(h.peers)+len(h.reservations))
	for _, peer := range h.peers {
		peers = append(peers, peer)
	}
	activeCount := len(peers)
	for _, peer := range h.reservations {
		peers = append(peers, peer)
	}
	pending := make([]*websocket.Conn, 0, len(h.pendingConns))
	for conn := range h.pendingConns {
		pending = append(pending, conn)
	}
	h.peers = make(map[PeerKey]*Peer)
	h.reservations = make(map[PeerKey]*Peer)
	h.pendingConns = make(map[*websocket.Conn]PeerKey)
	h.mu.Unlock()
	for _, conn := range pending {
		_ = conn.Close()
	}
	for i, peer := range peers {
		if i < activeCount && h.disconnectFn != nil {
			h.disconnectFn(peer)
		}
		peer.Close()
	}
}

func randomHex(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
