package remoteexec

import (
	"fmt"
	"sync"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
)

// AuthenticateFunc 校验 Hand token 并返回用于日志展示的标签。
type AuthenticateFunc func(token string) (string, error)

type pendingCall struct {
	ch           chan protocol.Envelope
	expectedPeer string
}

// Authority 是进程级远程执行权威，统一管理 Hub 路由和 run 生命周期。
type Authority struct {
	Hub      *hub.Hub
	Registry *Registry
	bus      *events.EventBus

	pendingMu sync.Mutex
	pending   map[string]*pendingCall
}

// NewAuthority 创建 Authority 并安装唯一的 Hub 生命周期回调。
func NewAuthority(h *hub.Hub, registry *Registry, bus *events.EventBus, authenticate AuthenticateFunc) *Authority {
	a := &Authority{Hub: h, Registry: registry, bus: bus, pending: make(map[string]*pendingCall)}
	h.OnHandshake(func(peer *hub.Peer, msg protocol.Envelope) error {
		reg, err := protocol.DecodePayload[protocol.Register](&msg)
		if err != nil {
			return err
		}
		if reg.Token == "" {
			return fmt.Errorf("token is required")
		}
		if authenticate == nil {
			return fmt.Errorf("authentication is not configured")
		}
		label, err := authenticate(reg.Token)
		if err != nil {
			return err
		}
		a.publish(events.LevelInfo, events.TypeSystem, fmt.Sprintf("[HUB] %s (%s) 已连接", peer.ID, label))
		return nil
	})
	h.OnDisconnect(a.handleDisconnect)
	h.OnMessage(a.handleMessage)
	return a
}

func (a *Authority) handleDisconnect(peer *hub.Peer) {
	if err := a.Registry.MarkLostByPeer(peer.ID, peer.SessionID()); err != nil {
		a.publish(events.LevelError, events.TypeSystem, err.Error())
	}
	a.publish(events.LevelInfo, events.TypeSystem, fmt.Sprintf("[HUB] %s 已断开", peer.ID))
}

// PendingCall 注册一次普通 Hand 请求的响应等待器。
func (a *Authority) PendingCall(id string, timeout time.Duration, expectedPeer string) (<-chan protocol.Envelope, func()) {
	ch := make(chan protocol.Envelope, 1)
	a.pendingMu.Lock()
	a.pending[id] = &pendingCall{ch: ch, expectedPeer: expectedPeer}
	a.pendingMu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			a.pendingMu.Lock()
			delete(a.pending, id)
			a.pendingMu.Unlock()
		})
	}
	if timeout > 0 {
		time.AfterFunc(timeout, cancel)
	}
	return ch, cancel
}

// Close 将全部非终态 run 收敛为 lost，并关闭在线 Peer。
func (a *Authority) Close() error {
	err := a.Registry.MarkAllLost("service_shutdown")
	a.Hub.Close()
	return err
}

func (a *Authority) handleMessage(peer *hub.Peer, msg protocol.Envelope) {
	if peer.Type != hub.PeerHand {
		return
	}
	switch msg.Type {
	case protocol.TypeRPCAccepted:
		accepted, err := protocol.DecodePayload[protocol.RPCAccepted](&msg)
		if err == nil && protocol.ValidateRPCAccepted(accepted) == nil {
			err = a.Registry.ApplyAcceptedFrom(peer.ID, peer.SessionID(), accepted)
		}
		a.publishError(err)
	case protocol.TypeRPCRejected:
		rejected, err := protocol.DecodePayload[protocol.RPCRejected](&msg)
		if err == nil && protocol.ValidateRPCRejected(rejected) == nil {
			err = a.Registry.ApplyRejectedFrom(peer.ID, peer.SessionID(), rejected)
		}
		a.publishError(err)
	case protocol.TypeRPCResult:
		result, err := protocol.DecodePayload[protocol.RPCResult](&msg)
		if err == nil && protocol.ValidateRPCResult(result) == nil {
			err = a.Registry.ApplyResultFrom(peer.ID, peer.SessionID(), result)
		}
		a.publishError(err)
	case protocol.TypeRPCCancelResult:
		result, err := protocol.DecodePayload[protocol.RPCCancelResult](&msg)
		if err == nil && protocol.ValidateRPCCancelResult(result) == nil {
			err = a.Registry.ApplyCancelResultFrom(peer.ID, peer.SessionID(), result)
		}
		a.publishError(err)
	case protocol.TypeHandInfoResp:
		resp, err := protocol.DecodePayload[protocol.HandInfoResp](&msg)
		if err == nil {
			a.deliverPending(peer.ID, resp.ID, msg)
		}
		a.publishError(err)
	case protocol.TypeHandEvent:
		event, err := protocol.DecodePayload[protocol.HandEvent](&msg)
		if err != nil {
			a.publishError(err)
			return
		}
		level := events.LevelInfo
		if event.Status == "triggered" {
			level = events.LevelWarn
		}
		a.publish(level, events.TypeSystem, fmt.Sprintf("[%s/%s] %s\n%s", peer.ID, event.Name, event.Status, event.Output))
	}
}

func (a *Authority) deliverPending(peerID, id string, msg protocol.Envelope) {
	a.pendingMu.Lock()
	pending, ok := a.pending[id]
	if ok && pending.expectedPeer == peerID {
		delete(a.pending, id)
	}
	a.pendingMu.Unlock()
	if ok && pending.expectedPeer == peerID {
		select {
		case pending.ch <- msg:
		default:
		}
	}
}

func (a *Authority) publishError(err error) {
	if err != nil {
		a.publish(events.LevelWarn, events.TypeSystem, err.Error())
	}
}

func (a *Authority) publish(level, typ, message string) {
	if a.bus != nil {
		a.bus.Publish(events.New("", "remoteexec", level, typ, message))
	}
}
