package remoteexec

import (
	"fmt"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// ApplyAccepted 应用来自预期 Hand 的接收消息。
func (r *Registry) ApplyAccepted(handID string, msg protocol.RPCAccepted) error {
	return r.ApplyAcceptedFrom(handID, "", msg)
}

// ApplyAcceptedFrom 应用来自指定 Hand 连接的接收消息。
func (r *Registry) ApplyAcceptedFrom(handID, connectionID string, msg protocol.RPCAccepted) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, err := r.runFromPeerLocked(msg.RunID, handID, connectionID)
	if err != nil {
		return err
	}
	if protocol.IsTerminalRunStatus(run.Status) {
		return nil
	}
	now := time.UnixMilli(msg.StartedAt)
	if run.Status == protocol.RunCancelRequested {
		if err := r.recordEventLocked(run, AuditTransition{
			EventType: "accepted_after_cancel", Message: "Hand accepted after cancellation was requested", Accepted: true, At: now,
		}); err != nil {
			return err
		}
		run.AcceptedAt = now
		return nil
	}
	if err := r.transitionLocked(run, protocol.RunAccepted, now, AuditTransition{}); err != nil {
		return err
	}
	run.AcceptedAt = now
	return r.transitionLocked(run, protocol.RunRunning, now, AuditTransition{})
}

// ApplyRejected 应用执行前拒绝并结束 run。
func (r *Registry) ApplyRejected(handID string, msg protocol.RPCRejected) error {
	return r.ApplyRejectedFrom(handID, "", msg)
}

// ApplyRejectedFrom 应用来自指定 Hand 连接的拒绝消息。
func (r *Registry) ApplyRejectedFrom(handID, connectionID string, msg protocol.RPCRejected) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, err := r.runFromPeerLocked(msg.RunID, handID, connectionID)
	if err != nil {
		return err
	}
	if protocol.IsTerminalRunStatus(run.Status) {
		return nil
	}
	if msg.Code == protocol.RejectDuplicateRun && !run.AcceptedAt.IsZero() {
		return nil
	}
	rejection := msg
	if err := r.transitionLocked(run, protocol.RunRejected, time.Now(), AuditTransition{
		RejectCode: msg.Code, Error: msg.Reason, Message: msg.Reason,
	}); err != nil {
		return err
	}
	run.Rejection = &rejection
	run.Error = msg.Reason
	return nil
}

// RejectLocal 记录 Mind 本地策略拒绝并终结 run。
func (r *Registry) RejectLocal(id string, code protocol.RejectCode, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok {
		return fmt.Errorf("run %q not found", id)
	}
	rejection := protocol.RPCRejected{RunID: id, Code: code, Reason: reason}
	if err := r.transitionLocked(run, protocol.RunRejected, time.Now(), AuditTransition{
		RejectCode: code, Error: reason, EventType: "local_rejection", Message: reason,
	}); err != nil {
		return err
	}
	run.Rejection = &rejection
	run.Error = reason
	return nil
}

// FailClosed 在审计或状态持久化失败时强制终结内存 run，避免留下无主执行。
// 该路径不再写审计；调用方必须保留并返回原始持久化错误。
func (r *Registry) FailClosed(id, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[id]
	if run == nil || protocol.IsTerminalRunStatus(run.Status) {
		return
	}
	run.Status = protocol.RunRejected
	run.FinishedAt = time.Now()
	run.Error = reason
	run.Rejection = &protocol.RPCRejected{RunID: id, Code: protocol.RejectInvalidRequest, Reason: reason}
	close(run.done)
}

// ApplyResult 应用最终执行结果。
func (r *Registry) ApplyResult(handID string, msg protocol.RPCResult) error {
	return r.ApplyResultFrom(handID, "", msg)
}

// ApplyResultFrom 应用来自指定 Hand 连接的最终结果。
func (r *Registry) ApplyResultFrom(handID, connectionID string, msg protocol.RPCResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, err := r.runFromPeerLocked(msg.RunID, handID, connectionID)
	if err != nil {
		return err
	}
	if protocol.IsTerminalRunStatus(run.Status) {
		return nil
	}
	status := protocol.RunFailed
	if msg.Success {
		status = protocol.RunSucceeded
	}
	if err := r.transitionLocked(run, status, time.Now(), AuditTransition{Error: msg.Error, Message: msg.Error}); err != nil {
		return err
	}
	result := msg
	run.Result = &result
	run.Error = msg.Error
	return nil
}

func (r *Registry) runFromPeerLocked(runID, handID, connectionID string) (*Run, error) {
	run, ok := r.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	if run.HandID != handID {
		return nil, fmt.Errorf("run %q source mismatch: got %q, want %q", runID, handID, run.HandID)
	}
	if run.ConnectionID != "" && connectionID != "" && run.ConnectionID != connectionID {
		return nil, fmt.Errorf("run %q connection mismatch", runID)
	}
	return run, nil
}

func (r *Registry) transitionLocked(run *Run, to protocol.RunStatus, now time.Time, audit AuditTransition) error {
	if run.Status == to {
		return nil
	}
	if !protocol.CanTransitionRun(run.Status, to) {
		return fmt.Errorf("invalid run transition %s -> %s", run.Status, to)
	}
	if r.auditor != nil {
		audit.RunID = run.ID
		audit.FromStatus = run.Status
		audit.ToStatus = to
		audit.At = now
		if err := r.auditor.TransitionRemoteRun(audit); err != nil {
			return auditFailure{err: err}
		}
	}
	run.Status = to
	switch to {
	case protocol.RunSent:
		run.SentAt = now
	case protocol.RunAccepted, protocol.RunRunning:
		if run.AcceptedAt.IsZero() {
			run.AcceptedAt = now
		}
	}
	if protocol.IsTerminalRunStatus(to) {
		run.FinishedAt = now
		close(run.done)
	}
	return nil
}

func (r *Registry) recordEventLocked(run *Run, audit AuditTransition) error {
	if r.auditor == nil {
		return nil
	}
	audit.RunID = run.ID
	audit.FromStatus = run.Status
	audit.ToStatus = run.Status
	if audit.At.IsZero() {
		audit.At = time.Now()
	}
	if err := r.auditor.TransitionRemoteRun(audit); err != nil {
		return auditFailure{err: err}
	}
	return nil
}
