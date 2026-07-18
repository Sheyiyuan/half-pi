package remoteexec

import (
	"fmt"
	"sync"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
)

type pendingCall struct {
	ch           chan protocol.Envelope
	expectedPeer string
	expectedConn *hub.Peer
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

// NewAuthority 创建远程执行权威。Hub 回调由 Mind dispatcher 统一安装。
func NewAuthority(h *hub.Hub, registry *Registry, bus *events.EventBus) *Authority {
	return &Authority{Hub: h, Registry: registry, bus: bus, pending: make(map[string]*pendingCall)}
}

// HandleHandDisconnect 处理已认证 Hand 断连。
func (a *Authority) HandleHandDisconnect(peer *hub.Peer) {
	if peer == nil || peer.Type != hub.PeerHand {
		return
	}
	if err := a.Registry.MarkLostByPeer(peer.ID, peer.SessionID()); err != nil {
		a.publish(events.LevelError, events.TypeSystem, err.Error())
	}
	a.publish(events.LevelInfo, events.TypeSystem, fmt.Sprintf("[HUB] %s 已断开", peer.ID))
}

// PendingCall 注册一次普通 Hand 请求的响应等待器。
func (a *Authority) PendingCall(id string, timeout time.Duration, expectedPeer string) (<-chan protocol.Envelope, func()) {
	return a.pendingCall(id, timeout, expectedPeer, nil)
}

// PendingPeerCall 注册绑定具体 Hand 连接的响应等待器。
func (a *Authority) PendingPeerCall(id string, timeout time.Duration, peer *hub.Peer) (<-chan protocol.Envelope, func()) {
	return a.pendingCall(id, timeout, peer.ID, peer)
}

func (a *Authority) pendingCall(id string, timeout time.Duration, expectedPeer string, expectedConn *hub.Peer) (<-chan protocol.Envelope, func()) {
	ch := make(chan protocol.Envelope, 1)
	a.pendingMu.Lock()
	a.pending[id] = &pendingCall{ch: ch, expectedPeer: expectedPeer, expectedConn: expectedConn}
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

// HandleHandMessage 处理已解密 Hand 业务消息。
func (a *Authority) HandleHandMessage(peer *hub.Peer, msg protocol.Envelope) {
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
			a.deliverPending(peer, resp.ID, msg)
		}
		a.publishError(err)
	case protocol.TypeTaskStatusResp:
		resp, err := protocol.DecodePayload[protocol.TaskStatusResp](&msg)
		responseID := msg.MsgID
		if err == nil {
			responseID = resp.ID
			err = protocol.ValidateTaskStatusResp(resp)
		}
		if responseID != "" {
			a.deliverPending(peer, responseID, msg)
		}
		a.publishError(err)
	case protocol.TypeTaskLogResp:
		resp, err := protocol.DecodePayload[protocol.TaskLogResp](&msg)
		responseID := msg.MsgID
		if err == nil {
			responseID = resp.ID
			err = protocol.ValidateTaskLogResp(resp)
		}
		if responseID != "" {
			a.deliverPending(peer, responseID, msg)
		}
		a.publishError(err)
	case protocol.TypeTaskCancelResult:
		resp, err := protocol.DecodePayload[protocol.TaskCancelResult](&msg)
		responseID := msg.MsgID
		if err == nil {
			responseID = resp.ID
			err = protocol.ValidateTaskCancelResult(resp)
		}
		if responseID != "" {
			a.deliverPending(peer, responseID, msg)
		}
		a.publishError(err)
	case protocol.TypeError:
		errMsg, err := protocol.DecodePayload[protocol.ErrorMsg](&msg)
		if err == nil && errMsg.MsgID != "" {
			a.deliverPending(peer, errMsg.MsgID, msg)
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

func (a *Authority) deliverPending(peer *hub.Peer, id string, msg protocol.Envelope) {
	a.pendingMu.Lock()
	pending, ok := a.pending[id]
	matches := ok && pending.expectedPeer == peer.ID && (pending.expectedConn == nil || pending.expectedConn == peer)
	if matches {
		delete(a.pending, id)
	}
	a.pendingMu.Unlock()
	if matches {
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
