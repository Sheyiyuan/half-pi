// Package facegateway 实现 Mind 侧统一 Face 协议网关。
package facegateway

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/conversation"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

const (
	handQueryTimeout = 10 * time.Second
	taskQueryTimeout = 10 * time.Second
)

// Config 是 Face Gateway 的进程级依赖。
type Config struct {
	Hub           *hub.Hub
	Store         *store.Store
	Conversations *conversation.Manager
	Approvals     *approval.Broker
	Authority     *remoteexec.Authority
	Tasks         *remoteexec.TaskService
	QueueSize     int
}

// Gateway 负责 Face command、scope、快照、订阅和有序出站投递。
type Gateway struct {
	hub           *hub.Hub
	store         *store.Store
	conversations *conversation.Manager
	approvals     *approval.Broker
	authority     *remoteexec.Authority
	tasks         *remoteexec.TaskService
	queueSize     int

	mu          sync.Mutex
	connections map[*hub.Peer]*connection
	chats       *chatRegistry
	domainMu    sync.Mutex
	version     atomic.Int64
	cursorKey   [32]byte
}

type connection struct {
	peer     *hub.Peer
	ctx      context.Context
	cancel   context.CancelFunc
	queue    chan protocol.Envelope
	outbound *outboundScheduler

	mu         sync.Mutex
	closed     bool
	slow       bool
	identity   protocol.FaceIdentity
	subscribed bool
	filter     subscription
	eventSeq   int64
	features   map[protocol.FaceFeature]struct{}
}

type subscription struct {
	conversations map[string]struct{}
	events        map[protocol.FaceEventType]struct{}
	transients    map[protocol.FaceTransientType]struct{}
}

// New 创建 Face Gateway 并连接结构化 domain 变化源。
func New(config Config) (*Gateway, error) {
	if config.Hub == nil || config.Store == nil || config.Conversations == nil || config.Approvals == nil || config.Authority == nil || config.Tasks == nil {
		return nil, fmt.Errorf("Face Gateway dependencies are required")
	}
	if config.QueueSize < 0 {
		return nil, fmt.Errorf("Face outbound queue size must not be negative")
	}
	if config.QueueSize == 0 {
		config.QueueSize = defaultOutboundQueueSize
	}
	gateway := &Gateway{
		hub: config.Hub, store: config.Store, conversations: config.Conversations, approvals: config.Approvals,
		authority: config.Authority, tasks: config.Tasks, queueSize: config.QueueSize,
		connections: make(map[*hub.Peer]*connection),
		chats:       newChatRegistry(),
	}
	if _, err := rand.Read(gateway.cursorKey[:]); err != nil {
		return nil, fmt.Errorf("generate Face task cursor key: %w", err)
	}
	gateway.version.Store(1)
	config.Conversations.Subscribe(gateway.PublishConversationChanged)
	config.Conversations.SubscribeCompact(gateway.PublishCompactEvent)
	config.Approvals.Subscribe(gateway.PublishApprovalRequested, gateway.PublishApprovalFinished)
	config.Authority.Registry.Subscribe(gateway.PublishRemoteRunChanged)
	config.Authority.SubscribeProgress(gateway.PublishRunProgress)
	config.Tasks.Subscribe(gateway.PublishTaskChanged)
	return gateway, nil
}

// HandleFaceMessage 校验并执行一个已解密 Face command。
func (g *Gateway) HandleFaceMessage(peer *hub.Peer, env protocol.Envelope) {
	if peer == nil || peer.Type != hub.PeerFace {
		return
	}
	state := g.connection(peer)
	meta := decodeMeta(env.Payload)
	if !protocol.IsFaceCommandType(env.Type) || protocol.ValidateFacePayload(env.Type, env.Payload) != nil {
		g.sendError(state, meta, protocol.FaceErrorInvalidRequest, "invalid Face request", false)
		return
	}
	identity, err := g.store.FaceIdentityByLabel(peer.ID)
	if err != nil {
		g.deauthorize(state)
		g.sendError(state, meta, protocol.FaceErrorInternal, "Face identity lookup failed", true)
		return
	}
	if identity == nil {
		g.deauthorize(state)
		g.sendError(state, meta, protocol.FaceErrorUnauthorized, "Face identity is no longer authorized", false)
		return
	}
	if peer.PrincipalID == "" || identity.ID != peer.PrincipalID {
		g.deauthorize(state)
		g.sendError(state, meta, protocol.FaceErrorUnauthorized, "Face identity is no longer authorized", false)
		return
	}
	state.mu.Lock()
	state.identity = *identity
	state.mu.Unlock()
	g.handleCommand(state, *identity, env)
}

func (g *Gateway) deauthorize(state *connection) {
	state.mu.Lock()
	state.identity = protocol.FaceIdentity{}
	state.subscribed = false
	state.filter = subscription{}
	state.features = nil
	state.mu.Unlock()
}

func (g *Gateway) connectionHasFeature(state *connection, feature protocol.FaceFeature) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	_, ok := state.features[feature]
	return ok
}

func (g *Gateway) requireFeature(state *connection, meta protocol.FaceCommandMeta, feature protocol.FaceFeature) bool {
	if g.connectionHasFeature(state, feature) {
		return true
	}
	g.sendError(state, meta, protocol.FaceErrorInvalidRequest, "Face feature was not negotiated", false)
	return false
}

// HandleFaceDisconnect 清理连接级队列、订阅和 event_seq。
func (g *Gateway) HandleFaceDisconnect(peer *hub.Peer) {
	g.mu.Lock()
	state := g.connections[peer]
	if state != nil {
		delete(g.connections, peer)
	}
	g.mu.Unlock()
	if state == nil {
		return
	}
	state.mu.Lock()
	state.closed = true
	state.mu.Unlock()
	state.cancel()
	if state.outbound != nil {
		state.outbound.close()
	}
}

func (g *Gateway) connection(peer *hub.Peer) *connection {
	g.mu.Lock()
	defer g.mu.Unlock()
	if state := g.connections[peer]; state != nil {
		return state
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &connection{
		peer: peer, ctx: ctx, cancel: cancel,
		outbound: newOutboundScheduler(g.queueSize),
	}
	g.connections[peer] = state
	go g.sendLoop(state)
	return state
}

func (g *Gateway) sendLoop(state *connection) {
	if state.outbound != nil {
		for {
			env, ok := state.outbound.pop(state.ctx)
			if !ok {
				return
			}
			if err := g.hub.SendPeerContext(state.ctx, state.peer, env); err != nil {
				g.hub.RemovePeer(state.peer)
				return
			}
		}
	}
	for {
		select {
		case <-state.ctx.Done():
			return
		case env := <-state.queue:
			if err := g.hub.SendPeerContext(state.ctx, state.peer, env); err != nil {
				g.hub.RemovePeer(state.peer)
				return
			}
		}
	}
}

func (g *Gateway) enqueue(state *connection, env protocol.Envelope) bool {
	state.mu.Lock()
	ok := g.enqueueLocked(state, env)
	state.mu.Unlock()
	return ok
}

func (g *Gateway) enqueueLocked(state *connection, env protocol.Envelope) bool {
	if state.closed || state.slow {
		return false
	}
	if state.outbound != nil {
		if state.outbound.enqueue(env, false) {
			return true
		}
		state.slow = true
		go g.hub.RemovePeer(state.peer)
		return false
	}
	select {
	case state.queue <- env:
		return true
	default:
		state.slow = true
		go g.hub.RemovePeer(state.peer)
		return false
	}
}

func (g *Gateway) enqueueTransientLocked(state *connection, env protocol.Envelope) bool {
	if state.closed || state.slow {
		return false
	}
	if state.outbound != nil {
		return state.outbound.enqueue(env, true)
	}
	select {
	case state.queue <- env:
		return true
	default:
		return false
	}
}

func (g *Gateway) sendPayload(state *connection, typ string, payload any) bool {
	env, err := protocol.NewEnvelope("", typ, payload)
	if err != nil || protocol.ValidateFacePayload(typ, env.Payload) != nil {
		return false
	}
	return g.enqueue(state, *env)
}

func (g *Gateway) sendTransientPayloadLocked(state *connection, typ string, payload any) bool {
	env, err := protocol.NewEnvelope("", typ, payload)
	if err != nil || protocol.ValidateFacePayload(typ, env.Payload) != nil {
		return false
	}
	return g.enqueueTransientLocked(state, *env)
}

func (g *Gateway) sendError(state *connection, meta protocol.FaceCommandMeta, code protocol.FaceErrorCode, message string, retryable bool) {
	g.sendPayload(state, protocol.TypeFaceError, protocol.FaceError{
		RequestID: meta.RequestID, ConversationID: meta.ConversationID,
		Code: code, Message: message, Retryable: retryable,
	})
}

func (g *Gateway) sendAccepted(state *connection, meta protocol.FaceCommandMeta, operation protocol.FaceOperation, version int64) bool {
	return g.sendPayload(state, protocol.TypeFaceAccepted, protocol.FaceAccepted{
		RequestID: meta.RequestID, ConversationID: meta.ConversationID,
		Operation: operation, SnapshotVersion: version,
	})
}

func (g *Gateway) sendResult(state *connection, meta protocol.FaceCommandMeta, operation protocol.FaceOperation, data any) {
	raw, err := json.Marshal(data)
	if err != nil || protocol.ValidateFaceResultData(operation, raw) != nil {
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "Face result projection failed")
		return
	}
	g.sendPayload(state, protocol.TypeFaceResult, protocol.FaceResult{
		RequestID: meta.RequestID, ConversationID: meta.ConversationID,
		Status: protocol.FaceResultSucceeded, Data: raw,
	})
}

func (g *Gateway) sendSuccessResult(state *connection, meta protocol.FaceCommandMeta, content string) {
	g.sendPayload(state, protocol.TypeFaceResult, protocol.FaceResult{
		RequestID: meta.RequestID, ConversationID: meta.ConversationID,
		Status: protocol.FaceResultSucceeded, Content: content,
	})
}

func (g *Gateway) sendFailedResult(state *connection, meta protocol.FaceCommandMeta, status protocol.FaceResultStatus, code protocol.FaceErrorCode, message string) {
	g.sendPayload(state, protocol.TypeFaceResult, protocol.FaceResult{
		RequestID: meta.RequestID, ConversationID: meta.ConversationID,
		Status: status, ErrorCode: code, Error: message,
	})
}

func decodeMeta(payload json.RawMessage) protocol.FaceCommandMeta {
	var meta protocol.FaceCommandMeta
	_ = json.Unmarshal(payload, &meta)
	return meta
}

func hasScope(identity protocol.FaceIdentity, scope protocol.FaceScope) bool {
	for _, candidate := range identity.Scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}
