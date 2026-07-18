package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

const runCancelAcknowledgementTimeout = 3 * time.Second

var (
	// ErrRunNotFound 表示 run 不在内存权威 registry 中。
	ErrRunNotFound = errors.New("run not found")
	// ErrRunNotOwned 表示 run 不属于调用方指定的 conversation。
	ErrRunNotOwned = errors.New("run is not owned by conversation")
	// ErrRunHandOffline 表示 run 绑定的原 Hand 连接已经不可用。
	ErrRunHandOffline = errors.New("run Hand is offline")
)

// CancelRun 通过 Authority 和原 Hand 连接请求取消 conversation 所属 run。
func (a *Authority) CancelRun(ctx context.Context, conversationID, runID, reason string) (Run, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	run, ok := a.Registry.Snapshot(runID)
	if !ok {
		return Run{}, ErrRunNotFound
	}
	if conversationID == "" || run.SessionID != conversationID {
		return Run{}, ErrRunNotOwned
	}
	if protocol.IsTerminalRunStatus(run.Status) || run.Status == protocol.RunCancelRequested {
		return run, nil
	}
	peer := a.Hub.PeerByType(hub.PeerHand, run.HandID)
	if peer == nil || (run.ConnectionID != "" && peer.SessionID() != run.ConnectionID) {
		return Run{}, ErrRunHandOffline
	}
	if reason == "" {
		reason = "user"
	}
	requested, err := a.Registry.RequestCancel(runID, reason)
	if err != nil && !IsAuditFailure(err) {
		return Run{}, fmt.Errorf("request run cancellation: %w", err)
	}
	if err == nil && !requested {
		current, _ := a.Registry.Snapshot(runID)
		return current, nil
	}
	env, err := protocol.NewEnvelope("", protocol.TypeRPCCancel, protocol.RPCCancel{RunID: runID, Reason: reason})
	if err != nil {
		a.failCancelClosed(runID, "create cancellation request failed")
		return Run{}, fmt.Errorf("create run cancellation: %w", err)
	}
	if err := a.Hub.SendPeerContext(ctx, peer, *env); err != nil {
		a.failCancelClosed(runID, "send cancellation request failed")
		return Run{}, fmt.Errorf("send run cancellation: %w", err)
	}
	done, release, ok := a.Registry.Wait(runID)
	if !ok {
		return Run{}, ErrRunNotFound
	}
	defer release()
	timer := time.NewTimer(runCancelAcknowledgementTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
		current, _ := a.Registry.Snapshot(runID)
		return current, ctx.Err()
	case <-timer.C:
		if err := a.Registry.MarkCancelUnconfirmed(runID); err != nil {
			a.Registry.FailClosed(runID, fmt.Sprintf("record unconfirmed run cancellation: %v", err))
			return Run{}, fmt.Errorf("mark unconfirmed run cancellation: %w", err)
		}
	}
	current, ok := a.Registry.Snapshot(runID)
	if !ok {
		return Run{}, ErrRunNotFound
	}
	return current, nil
}

func (a *Authority) failCancelClosed(runID, reason string) {
	if err := a.Registry.MarkLost(runID, reason); err != nil {
		a.Registry.FailClosed(runID, fmt.Sprintf("%s: %v", reason, err))
	}
}
