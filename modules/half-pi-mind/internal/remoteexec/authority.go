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
	orderedMu sync.Mutex
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
	case protocol.TypeRPCAccepted, protocol.TypeRPCRejected, protocol.TypeRPCProgress,
		protocol.TypeRPCResult, protocol.TypeRPCCancelResult:
		a.orderedMu.Lock()
		defer a.orderedMu.Unlock()
	}
	switch msg.Type {
	case protocol.TypeRPCAccepted:
		accepted, err := protocol.DecodePayload[protocol.RPCAccepted](&msg)
		if err == nil && protocol.ValidateRPCAccepted(accepted) == nil {
			err = a.Registry.ApplyAcceptedFrom(peer.ID, peer.SessionID(), accepted)
		}
		a.publishError(err)
		if err == nil {
			a.publishRun(accepted.RunID)
		}
	case protocol.TypeRPCRejected:
		rejected, err := protocol.DecodePayload[protocol.RPCRejected](&msg)
		if err == nil && protocol.ValidateRPCRejected(rejected) == nil {
			err = a.Registry.ApplyRejectedFrom(peer.ID, peer.SessionID(), rejected)
		}
		if IsAuditFailure(err) {
			a.Registry.FailClosed(rejected.RunID, fmt.Sprintf("记录 Hand 拒绝失败: %v", err))
		}
		a.publishError(err)
		if err == nil || IsAuditFailure(err) {
			a.publishRun(rejected.RunID)
		}
	case protocol.TypeRPCProgress:
		progress, err := protocol.DecodePayload[protocol.RPCProgress](&msg)
		var admission ProgressAdmission
		if err == nil {
			err = protocol.ValidateRPCProgress(progress)
		}
		if err == nil {
			admission, err = a.Registry.ApplyProgressFrom(peer.ID, peer.SessionID(), progress)
		}
		a.publishError(err)
		if err == nil && admission.Accepted {
			a.publishProgress(progress, admission.Gap)
		}
	case protocol.TypeRPCResult:
		result, err := protocol.DecodePayload[protocol.RPCResult](&msg)
		if err == nil && protocol.ValidateRPCResult(result) == nil {
			err = a.Registry.ApplyResultFrom(peer.ID, peer.SessionID(), result)
		}
		if IsAuditFailure(err) {
			a.Registry.FailClosed(result.RunID, fmt.Sprintf("记录 Hand 结果失败: %v", err))
		}
		a.publishError(err)
		if err == nil || IsAuditFailure(err) {
			a.publishRun(result.RunID)
		}
	case protocol.TypeRPCCancelResult:
		result, err := protocol.DecodePayload[protocol.RPCCancelResult](&msg)
		if err == nil && protocol.ValidateRPCCancelResult(result) == nil {
			err = a.Registry.ApplyCancelResultFrom(peer.ID, peer.SessionID(), result)
		}
		if IsAuditFailure(err) {
			a.Registry.FailClosed(result.RunID, fmt.Sprintf("记录 Hand 取消结果失败: %v", err))
		}
		a.publishError(err)
		if err == nil || IsAuditFailure(err) {
			a.publishRun(result.RunID)
		}
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

// ToolProgressEventData 是 TypeToolProgress 的结构化数据。
type ToolProgressEventData struct {
	RunID string                `json:"run_id"`
	Seq   int64                 `json:"seq"`
	Kind  protocol.ProgressKind `json:"kind"`
	Data  string                `json:"data"`
	Gap   bool                  `json:"gap"`
}

func (a *Authority) publishProgress(progress protocol.RPCProgress, gap bool) {
	run, ok := a.Registry.Snapshot(progress.RunID)
	if !ok || a.bus == nil {
		return
	}
	a.bus.PublishSync(events.New(run.SessionID, "remoteexec", events.LevelInfo, events.TypeToolProgress, progress.Data).WithData(
		ToolProgressEventData{RunID: progress.RunID, Seq: progress.Seq, Kind: progress.Kind, Data: progress.Data, Gap: gap},
	))
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

func (a *Authority) publishRun(runID string) {
	run, ok := a.Registry.Snapshot(runID)
	if !ok || a.bus == nil {
		return
	}
	a.bus.PublishSync(events.New(run.SessionID, "remoteexec", events.LevelInfo, events.TypeToolResult,
		fmt.Sprintf("run=%s hand=%s tool=%s status=%s", run.ID, run.HandID, run.Tool, run.Status)))
}

func (a *Authority) publish(level, typ, message string) {
	if a.bus != nil {
		a.bus.Publish(events.New("", "remoteexec", level, typ, message))
	}
}
