// Package remoteexec 管理 Mind 侧远程执行生命周期。
package remoteexec

import (
	"fmt"
	"sync"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// Run 是一次远程执行的内存状态。
type Run struct {
	ID        string
	SessionID string
	HandID    string
	Tool      string
	Status    protocol.RunStatus

	CreatedAt  time.Time
	SentAt     time.Time
	AcceptedAt time.Time
	FinishedAt time.Time

	Result       *protocol.RPCResult
	Rejection    *protocol.RPCRejected
	Error        string
	CancelReason string

	done    chan struct{}
	waiters int
}

// Registry 按 run_id 原子管理远程执行状态。
type Registry struct {
	mu   sync.Mutex
	runs map[string]*Run
}

const (
	terminalRunRetention = 10 * time.Minute
	maxTerminalRuns      = 100
)

// NewRegistry 创建空的远程执行注册表。
func NewRegistry() *Registry {
	return &Registry{runs: make(map[string]*Run)}
}

// Create 创建 run，初始状态为 created。
func (r *Registry) Create(id, sessionID, handID, tool string) error {
	if id == "" || handID == "" || tool == "" {
		return fmt.Errorf("run id, hand id and tool are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneRunsLocked(time.Now())
	if _, exists := r.runs[id]; exists {
		return fmt.Errorf("run %q already exists", id)
	}
	r.runs[id] = &Run{
		ID: id, SessionID: sessionID, HandID: handID, Tool: tool,
		Status: protocol.RunCreated, CreatedAt: time.Now(), done: make(chan struct{}),
	}
	return nil
}

// Transition 执行一次合法状态迁移。
func (r *Registry) Transition(id string, to protocol.RunStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok {
		return fmt.Errorf("run %q not found", id)
	}
	return transitionLocked(run, to, time.Now())
}

// Done 返回 run 进入终态时关闭的通道。
func (r *Registry) Done(id string) (<-chan struct{}, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok {
		return nil, false
	}
	return run.done, true
}

// Wait 注册一个活跃等待者，release 后终态 run 才可被淘汰。
func (r *Registry) Wait(id string) (<-chan struct{}, func(), bool) {
	r.mu.Lock()
	run, ok := r.runs[id]
	if !ok {
		r.mu.Unlock()
		return nil, nil, false
	}
	run.waiters++
	done := run.done
	r.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			r.mu.Lock()
			if current := r.runs[id]; current != nil && current.waiters > 0 {
				current.waiters--
			}
			r.mu.Unlock()
		})
	}
	return done, release, true
}

// Snapshot 返回 run 的只读快照。
func (r *Registry) Snapshot(id string) (Run, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok {
		return Run{}, false
	}
	copy := *run
	if run.Result != nil {
		result := *run.Result
		copy.Result = &result
	}
	if run.Rejection != nil {
		rejection := *run.Rejection
		copy.Rejection = &rejection
	}
	copy.done = nil
	return copy, true
}

// ApplyAccepted 应用来自预期 Hand 的接收消息。
func (r *Registry) ApplyAccepted(handID string, msg protocol.RPCAccepted) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, err := r.runFromHandLocked(msg.RunID, handID)
	if err != nil {
		return err
	}
	if protocol.IsTerminalRunStatus(run.Status) {
		return nil
	}
	now := time.UnixMilli(msg.StartedAt)
	if run.Status == protocol.RunCancelRequested {
		run.AcceptedAt = now
		return nil
	}
	if err := transitionLocked(run, protocol.RunAccepted, now); err != nil {
		return err
	}
	run.AcceptedAt = now
	return transitionLocked(run, protocol.RunRunning, now)
}

// ApplyRejected 应用执行前拒绝并结束 run。
func (r *Registry) ApplyRejected(handID string, msg protocol.RPCRejected) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, err := r.runFromHandLocked(msg.RunID, handID)
	if err != nil {
		return err
	}
	if protocol.IsTerminalRunStatus(run.Status) {
		return nil
	}
	if msg.Code == protocol.RejectDuplicateRun && !run.AcceptedAt.IsZero() {
		return nil
	}
	if err := transitionLocked(run, protocol.RunRejected, time.Now()); err != nil {
		return err
	}
	rejection := msg
	run.Rejection = &rejection
	run.Error = msg.Reason
	return nil
}

// ApplyResult 应用最终执行结果。
func (r *Registry) ApplyResult(handID string, msg protocol.RPCResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, err := r.runFromHandLocked(msg.RunID, handID)
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
	if err := transitionLocked(run, status, time.Now()); err != nil {
		return err
	}
	result := msg
	run.Result = &result
	run.Error = msg.Error
	return nil
}

// ApplyCancelResult 应用 Hand 的取消响应。
func (r *Registry) ApplyCancelResult(handID string, msg protocol.RPCCancelResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, err := r.runFromHandLocked(msg.RunID, handID)
	if err != nil {
		return err
	}
	if protocol.IsTerminalRunStatus(run.Status) {
		return nil
	}
	switch msg.Status {
	case protocol.CancelCancelled:
		status := protocol.RunCancelled
		if run.CancelReason == "timeout" {
			status = protocol.RunTimedOut
		}
		return transitionLocked(run, status, time.Now())
	case protocol.CancelUnknownRun:
		run.Error = "Hand does not know this run"
		return transitionLocked(run, protocol.RunLost, time.Now())
	case protocol.CancelFailed:
		run.Error = msg.Error
	}
	return nil
}

// RequestCancel 将非终态 run 转为 cancel_requested。
func (r *Registry) RequestCancel(id, reason string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok || protocol.IsTerminalRunStatus(run.Status) || run.Status == protocol.RunCancelRequested {
		return false
	}
	if transitionLocked(run, protocol.RunCancelRequested, time.Now()) != nil {
		return false
	}
	run.CancelReason = reason
	return true
}

// MarkTimedOut 将仍未结束的 run 收敛为 timed_out。
func (r *Registry) MarkTimedOut(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[id]
	if run == nil || protocol.IsTerminalRunStatus(run.Status) {
		return
	}
	_ = transitionLocked(run, protocol.RunTimedOut, time.Now())
}

// MarkCancelUnconfirmed 在取消确认超时后按取消原因收敛 run。
func (r *Registry) MarkCancelUnconfirmed(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[id]
	if run == nil || protocol.IsTerminalRunStatus(run.Status) {
		return
	}
	status := protocol.RunLost
	if run.CancelReason == "timeout" {
		status = protocol.RunTimedOut
	} else {
		run.Error = "cancellation was not confirmed by Hand"
	}
	_ = transitionLocked(run, status, time.Now())
}

// MarkLostByHand 将断开 Hand 的全部非终态 run 标记为 lost。
func (r *Registry) MarkLostByHand(handID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, run := range r.runs {
		if run.HandID == handID && !protocol.IsTerminalRunStatus(run.Status) {
			_ = transitionLocked(run, protocol.RunLost, time.Now())
		}
	}
}

func (r *Registry) runFromHandLocked(runID, handID string) (*Run, error) {
	run, ok := r.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	if run.HandID != handID {
		return nil, fmt.Errorf("run %q source mismatch: got %q, want %q", runID, handID, run.HandID)
	}
	return run, nil
}

func (r *Registry) pruneRunsLocked(now time.Time) {
	terminalCount := 0
	for id, run := range r.runs {
		if !protocol.IsTerminalRunStatus(run.Status) || run.waiters > 0 {
			continue
		}
		if now.Sub(run.FinishedAt) >= terminalRunRetention {
			delete(r.runs, id)
			continue
		}
		terminalCount++
	}
	for terminalCount >= maxTerminalRuns {
		var oldestID string
		var oldest time.Time
		for id, run := range r.runs {
			if !protocol.IsTerminalRunStatus(run.Status) || run.waiters > 0 {
				continue
			}
			if oldestID == "" || run.FinishedAt.Before(oldest) {
				oldestID, oldest = id, run.FinishedAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(r.runs, oldestID)
		terminalCount--
	}
}

func transitionLocked(run *Run, to protocol.RunStatus, now time.Time) error {
	if run.Status == to {
		return nil
	}
	if !protocol.CanTransitionRun(run.Status, to) {
		return fmt.Errorf("invalid run transition %s -> %s", run.Status, to)
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
