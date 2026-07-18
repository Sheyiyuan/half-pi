package remoteexec

import (
	"errors"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// ApplyCancelResult 应用 Hand 的取消响应。
func (r *Registry) ApplyCancelResult(handID string, msg protocol.RPCCancelResult) error {
	return r.ApplyCancelResultFrom(handID, "", msg)
}

// ApplyCancelResultFrom 应用来自指定 Hand 连接的取消响应。
func (r *Registry) ApplyCancelResultFrom(handID, connectionID string, msg protocol.RPCCancelResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, err := r.runFromPeerLocked(msg.RunID, handID, connectionID)
	if err != nil {
		return err
	}
	if protocol.IsTerminalRunStatus(run.Status) {
		return nil
	}
	switch msg.Status {
	case protocol.CancelCancelled:
		if run.Status != protocol.RunCancelRequested && run.CancelReason != "" {
			if err := r.transitionLocked(run, protocol.RunCancelRequested, time.Now(), AuditTransition{
				EventType: "cancel_request_recovered", Message: run.CancelReason,
			}); err != nil {
				return err
			}
		}
		status := protocol.RunCancelled
		if run.CancelReason == "timeout" {
			status = protocol.RunTimedOut
		}
		return r.transitionLocked(run, status, time.Now(), AuditTransition{})
	case protocol.CancelUnknownRun:
		errMsg := "Hand does not know this run"
		if err := r.transitionLocked(run, protocol.RunLost, time.Now(), AuditTransition{Error: errMsg}); err != nil {
			return err
		}
		run.Error = errMsg
		return nil
	case protocol.CancelFailed:
		if err := r.recordEventLocked(run, AuditTransition{EventType: "cancel_failed", Error: msg.Error, Message: msg.Error, At: time.Now()}); err != nil {
			return err
		}
		run.Error = msg.Error
	}
	return nil
}

// RequestCancel 将非终态 run 转为 cancel_requested。
func (r *Registry) RequestCancel(id, reason string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok || protocol.IsTerminalRunStatus(run.Status) || run.Status == protocol.RunCancelRequested {
		return false, nil
	}
	previousReason := run.CancelReason
	run.CancelReason = reason
	if err := r.transitionLocked(run, protocol.RunCancelRequested, time.Now(), AuditTransition{Message: reason}); err != nil {
		if !IsAuditFailure(err) {
			run.CancelReason = previousReason
		}
		return false, err
	}
	return true, nil
}

// MarkTimedOut 将仍未结束的 run 收敛为 timed_out。
func (r *Registry) MarkTimedOut(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[id]
	if run == nil || protocol.IsTerminalRunStatus(run.Status) {
		return nil
	}
	return r.transitionLocked(run, protocol.RunTimedOut, time.Now(), AuditTransition{})
}

// MarkLost 将仍未结束的 run 收敛为 lost。
func (r *Registry) MarkLost(id, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[id]
	if run == nil || protocol.IsTerminalRunStatus(run.Status) {
		return nil
	}
	if err := r.transitionLocked(run, protocol.RunLost, time.Now(), AuditTransition{Error: reason, Message: reason}); err != nil {
		return err
	}
	run.Error = reason
	return nil
}

// MarkCancelUnconfirmed 在取消确认超时后按取消原因收敛 run。
func (r *Registry) MarkCancelUnconfirmed(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[id]
	if run == nil || protocol.IsTerminalRunStatus(run.Status) {
		return nil
	}
	status := protocol.RunLost
	errMsg := ""
	if run.CancelReason == "timeout" {
		status = protocol.RunTimedOut
	} else {
		errMsg = "cancellation was not confirmed by Hand"
	}
	if err := r.transitionLocked(run, status, time.Now(), AuditTransition{Error: errMsg}); err != nil {
		return err
	}
	run.Error = errMsg
	return nil
}

// MarkLostByHand 将断开 Hand 的全部非终态 run 标记为 lost。
func (r *Registry) MarkLostByHand(handID string) error {
	return r.MarkLostByPeer(handID, "")
}

// MarkLostByPeer 仅收敛绑定到指定 Hand 连接的非终态 run。
func (r *Registry) MarkLostByPeer(handID, connectionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, run := range r.runs {
		connectionMatches := connectionID == "" || run.ConnectionID == "" || run.ConnectionID == connectionID
		if run.HandID == handID && connectionMatches && !protocol.IsTerminalRunStatus(run.Status) {
			if err := r.transitionLocked(run, protocol.RunLost, time.Now(), AuditTransition{EventType: "hand_disconnect"}); err != nil {
				return err
			}
		}
	}
	return nil
}

// MarkAllLost 将全部非终态 run 标记为 lost。
func (r *Registry) MarkAllLost(reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result error
	for _, run := range r.runs {
		if protocol.IsTerminalRunStatus(run.Status) {
			continue
		}
		if err := r.transitionLocked(run, protocol.RunLost, time.Now(), AuditTransition{EventType: reason, Error: reason}); err != nil {
			result = errors.Join(result, err)
			continue
		}
		run.Error = reason
	}
	return result
}
