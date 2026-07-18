package local

import (
	"context"
	"fmt"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

type runStartedKey struct{}

// WithRunStarted 注册 run 创建后的回调，用于非阻塞手动执行入口。
func WithRunStarted(ctx context.Context, callback func(string)) context.Context {
	return context.WithValue(ctx, runStartedKey{}, callback)
}

func notifyRunStarted(ctx context.Context, runID string) {
	if callback, ok := ctx.Value(runStartedKey{}).(func(string)); ok && callback != nil {
		callback(runID)
	}
}

// RemoteRunView 是所有远程执行状态共用的稳定输出结构。
type RemoteRunView struct {
	RunID      string              `json:"run_id"`
	SessionID  string              `json:"session_id"`
	HandID     string              `json:"hand_id"`
	Tool       string              `json:"tool"`
	Status     protocol.RunStatus  `json:"status"`
	DurationMs int64               `json:"duration_ms"`
	Truncated  bool                `json:"truncated"`
	Output     string              `json:"output,omitempty"`
	Error      string              `json:"error,omitempty"`
	RejectCode protocol.RejectCode `json:"reject_code,omitempty"`
}

// RemoteRunSnapshot 返回当前会话可见的结构化 run 状态。
func RemoteRunSnapshot(bridge *RemoteBridge, runID string) (RemoteRunView, error) {
	run, ok := bridge.Runs.Snapshot(runID)
	if !ok {
		return RemoteRunView{}, fmt.Errorf("远程执行记录 %q 不存在", runID)
	}
	if bridge.SessionID != nil && run.SessionID != bridge.SessionID() {
		return RemoteRunView{}, fmt.Errorf("run %q 不属于当前会话", runID)
	}
	return remoteRunView(run), nil
}

// CancelRemoteRun 通过原执行连接取消当前会话所属 run。
func CancelRemoteRun(ctx context.Context, bridge *RemoteBridge, runID string) (RemoteRunView, error) {
	run, ok := bridge.Runs.Snapshot(runID)
	if !ok {
		return RemoteRunView{}, fmt.Errorf("远程执行记录 %q 不存在", runID)
	}
	if bridge.SessionID != nil && run.SessionID != bridge.SessionID() {
		return RemoteRunView{}, fmt.Errorf("run %q 不属于当前会话", runID)
	}
	peer := bridge.Hub.PeerByType(hub.PeerHand, run.HandID)
	if peer == nil || peer.SessionID() != run.ConnectionID {
		return RemoteRunView{}, fmt.Errorf("run %q 的原 Hand 连接已断开", runID)
	}
	reason := "user"
	if err := ctx.Err(); err != nil {
		return RemoteRunView{}, err
	}
	requestRemoteCancel(bridge, peer, runID, run.HandID, reason)
	return RemoteRunSnapshot(bridge, runID)
}

func remoteRunView(run remoteexec.Run) RemoteRunView {
	end := run.FinishedAt
	if end.IsZero() {
		end = time.Now()
	}
	view := RemoteRunView{
		RunID: run.ID, SessionID: run.SessionID, HandID: run.HandID, Tool: run.Tool,
		Status: run.Status, DurationMs: max(0, end.Sub(run.CreatedAt).Milliseconds()), Error: run.Error,
	}
	if run.Result != nil {
		view.Output = run.Result.Output
		view.Error = run.Result.Error
		view.Truncated = run.Result.Truncated
	}
	if run.Rejection != nil {
		view.RejectCode = run.Rejection.Code
		view.Error = run.Rejection.Reason
	}
	return view
}
