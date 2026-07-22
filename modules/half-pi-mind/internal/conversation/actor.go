package conversation

import (
	"context"
	"errors"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/compact"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

// OperationState 是 Actor 唯一 mutation lease 的公开状态。
type OperationState string

const (
	OperationIdle              OperationState = "idle"
	OperationChatPreparing     OperationState = "chat_preparing"
	OperationChatRunning       OperationState = "chat_running"
	OperationCompactingForChat OperationState = "compacting_for_chat"
	OperationCompactingManual  OperationState = "compacting_manual"
	OperationCompactingAuto    OperationState = "compacting_auto"
	OperationMutatingContext   OperationState = "mutating_context"
)

// OperationState 返回当前进程内 mutation lease 状态。
func (a *Actor) OperationState() OperationState {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	return a.operation
}

func (a *Actor) acquire(state OperationState, requestID string) bool {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	if a.operation != OperationIdle {
		return false
	}
	a.operation, a.requestID = state, requestID
	return true
}

func (a *Actor) transition(from, to OperationState) bool {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	if a.operation != from {
		return false
	}
	a.operation = to
	return true
}

func (a *Actor) release() {
	a.operationMu.Lock()
	a.operation, a.requestID = OperationIdle, ""
	a.operationMu.Unlock()
}

// Chat 通过 Actor 唯一 lease 执行一轮 Chat。
func (a *Actor) Chat(ctx context.Context, content string) (string, error) {
	return a.ChatWithTransport(ctx, content, agentcore.ChatTransport{})
}

// ChatWithTransport 通过 Actor 唯一 lease 执行流式 Chat。
func (a *Actor) ChatWithTransport(ctx context.Context, content string, transport agentcore.ChatTransport) (string, error) {
	requestID := requestctx.RequestID(ctx)
	if !a.acquire(OperationChatPreparing, requestID) {
		return "", ErrBusy
	}
	defer func() {
		a.release()
		a.schedulePendingWorker()
	}()
	return a.core.ChatWithTransport(ctx, content, transport)
}

// Compact 执行一次手动 Compact mutation。
func (a *Actor) Compact(ctx context.Context, request compact.CompactRequest) (compact.CompactResult, error) {
	if a.compactor == nil {
		return compact.CompactResult{}, compact.NewError(compact.ErrUnavailable)
	}
	if !a.acquire(OperationCompactingManual, request.RequestID) {
		return compact.CompactResult{}, ErrBusy
	}
	defer func() {
		a.release()
		a.schedulePendingWorker()
	}()
	request.SessionID = a.core.SessionID()
	request.Trigger = compact.TriggerManual
	status, err := a.compactor.Status(ctx, compact.CompactStatusRequest{SessionID: request.SessionID, OperationState: string(OperationCompactingManual)})
	if err != nil {
		return compact.CompactResult{}, err
	}
	if status.Pending {
		request.Pending = store.PendingExpectation{ID: status.PendingID, Attempt: status.PendingAttempt, Required: true}
	}
	result, err := a.compactor.Compact(ctx, request)
	if a.onChange != nil {
		a.onChange()
	}
	return result, err
}

// CompactStatus 返回已提交 Compact 状态；不取得 mutation lease。
func (a *Actor) CompactStatus(ctx context.Context) (compact.CompactStatus, error) {
	if a.compactor == nil {
		return compact.CompactStatus{OperationState: string(a.OperationState())}, nil
	}
	return a.compactor.Status(ctx, compact.CompactStatusRequest{
		SessionID: a.core.SessionID(), OperationState: string(a.OperationState()),
	})
}

// SetMode 在 Actor context-mutation lease 下切换安全模式。
func (a *Actor) SetMode(mode string) error {
	if !a.acquire(OperationMutatingContext, "") {
		return ErrBusy
	}
	defer a.release()
	return a.core.SetMode(mode)
}

// SetActiveHand 在 Actor context-mutation lease 下设置默认 Hand。
func (a *Actor) SetActiveHand(handID string) error {
	if !a.acquire(OperationMutatingContext, "") {
		return ErrBusy
	}
	defer a.release()
	return a.core.SetActiveHand(handID)
}

// SaveSession 在 Actor lease 下刷新尚未持久化的原始历史。
func (a *Actor) SaveSession() error {
	if !a.acquire(OperationMutatingContext, "") {
		return ErrBusy
	}
	defer a.release()
	return a.core.SaveSession()
}

// ObserveModelBudget 实现 agentcore.CompactionCoordinator。
func (a *Actor) ObserveModelBudget(ctx context.Context, observation agentcore.BudgetObservation) (bool, error) {
	if observation.FirstRequest {
		a.transition(OperationChatPreparing, OperationChatRunning)
	}
	if observation.EstimatedTokens < observation.HighLimit || !a.runtime.Automatic || a.compactor == nil {
		return false, nil
	}
	automatic, ok := a.compactor.(compact.AutomaticCompactor)
	if !ok {
		return false, nil
	}
	pending, err := automatic.EnsureAutomaticPending(ctx, a.core.SessionID())
	if err != nil {
		if observation.EstimatedTokens > observation.HardLimit {
			return false, budgetFailure(observation, err)
		}
		return false, nil
	}
	if a.onChange != nil && pending.Created {
		a.onChange()
	}
	if !observation.FirstRequest {
		return false, nil
	}
	if !a.transition(OperationChatRunning, OperationCompactingForChat) {
		return false, ErrBusy
	}
	if pending.Runtime.PendingNotBefore > time.Now().UnixMilli() {
		a.transition(OperationCompactingForChat, OperationChatRunning)
		if observation.EstimatedTokens > observation.HardLimit {
			return false, compact.NewError(compact.ErrRateLimited)
		}
		return false, nil
	}
	_, err = a.compactor.Compact(ctx, compact.CompactRequest{
		SessionID: a.core.SessionID(), RequestID: pending.Runtime.PendingCompactID,
		Target: compact.DefaultTarget{}, Trigger: compact.TriggerAutomatic,
		Pending: store.PendingExpectation{ID: pending.Runtime.PendingCompactID, Attempt: pending.Runtime.PendingAttempt, Required: true},
	})
	a.transition(OperationCompactingForChat, OperationChatRunning)
	if a.onChange != nil {
		a.onChange()
	}
	if err != nil {
		if observation.EstimatedTokens > observation.HardLimit {
			return false, budgetFailure(observation, err)
		}
		return false, nil
	}
	return true, nil
}

// PrepareToolBatchPending 为 Store 的工具批次事务生成 durable 自动 Compact mutation。
func (a *Actor) PrepareToolBatchPending(_ context.Context, observation agentcore.BudgetObservation) (*agentcore.ToolBatchPending, error) {
	if !a.runtime.Automatic || a.compactor == nil || observation.EstimatedTokens < observation.HighLimit {
		return nil, nil
	}
	pendingID, event := compact.NewAutomaticPendingMutation(a.core.SessionID())
	return &agentcore.ToolBatchPending{ID: pendingID, Event: event}, nil
}

func budgetFailure(observation agentcore.BudgetObservation, err error) error {
	if compact.ErrorCodeOf(err) == compact.ErrRateLimited {
		return err
	}
	if observation.Degraded {
		return compact.NewError(compact.ErrRepairRequired)
	}
	return compact.NewError(compact.ErrContextLimit)
}

func (a *Actor) schedulePendingWorker() {
	if !a.runtime.Automatic || a.compactor == nil {
		return
	}
	a.workerMu.Lock()
	if a.workerCtx == nil || a.workerCtx.Err() != nil {
		a.workerMu.Unlock()
		return
	}
	if a.workerLive {
		a.workerAgain = true
		a.workerMu.Unlock()
		return
	}
	a.workerLive = true
	a.workerWG.Add(1)
	a.workerMu.Unlock()
	go a.runPendingWorker()
}

func (a *Actor) runPendingWorker() {
	reschedule := false
	defer func() {
		defer a.workerWG.Done()
		a.workerMu.Lock()
		reschedule = reschedule || a.workerAgain
		a.workerAgain = false
		a.workerLive = false
		a.workerMu.Unlock()
		if reschedule {
			a.schedulePendingWorker()
		}
	}()
	ctx := a.workerCtx
	status, err := a.CompactStatus(ctx)
	if err != nil || !status.Pending {
		return
	}
	if delay := time.Until(status.PendingNotBefore); delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}
	if !a.acquire(OperationCompactingAuto, status.PendingID) {
		return
	}
	defer a.release()
	status, err = a.CompactStatus(ctx)
	if err != nil || !status.Pending {
		return
	}
	automatic, ok := a.compactor.(compact.AutomaticCompactor)
	if !ok {
		return
	}
	pending := store.PendingExpectation{ID: status.PendingID, Attempt: status.PendingAttempt, Required: true}
	if status.CurrentEstimatedTokens < status.HighLimit {
		_, _ = automatic.ClearAutomaticPending(ctx, a.core.SessionID(), pending)
		if a.onChange != nil {
			a.onChange()
		}
		return
	}
	_, runErr := a.compactor.Compact(ctx, compact.CompactRequest{
		SessionID: a.core.SessionID(), RequestID: status.PendingID,
		Target: compact.DefaultTarget{}, Trigger: compact.TriggerAutomatic, Pending: pending,
	})
	if a.onChange != nil {
		a.onChange()
	}
	if errors.Is(runErr, context.Canceled) {
		return
	}
	reschedule = compact.ErrorCodeOf(runErr) == compact.ErrRateLimited
}

func (a *Actor) stopPendingWorker() {
	a.workerMu.Lock()
	stop := a.workerStop
	a.workerMu.Unlock()
	if stop != nil {
		stop()
	}
}

func (a *Actor) waitPendingWorker(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		a.workerWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ agentcore.CompactionCoordinator = (*Actor)(nil)
var _ agentcore.ToolBatchCompactionCoordinator = (*Actor)(nil)
var _ compact.EnvironmentSource = (*Manager)(nil)
