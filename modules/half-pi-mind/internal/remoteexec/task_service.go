package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// Task 是 Mind 持久化的后台任务快照，不含原始参数和 Hand 文件路径。
type Task struct {
	TaskID     string              `json:"task_id"`
	SessionID  string              `json:"session_id"`
	HandID     string              `json:"hand_id"`
	Tool       string              `json:"tool"`
	ArgsDigest string              `json:"-"`
	Status     protocol.TaskStatus `json:"status"`
	CreatedAt  time.Time           `json:"created_at"`
	StartedAt  time.Time           `json:"started_at,omitempty"`
	FinishedAt time.Time           `json:"finished_at,omitempty"`
	UpdatedAt  time.Time           `json:"updated_at"`
	LogBytes   int64               `json:"log_bytes"`
	Truncated  bool                `json:"truncated"`
	Stale      bool                `json:"stale"`
	Error      string              `json:"error,omitempty"`
}

// TaskStore 持久化后台任务元数据。
type TaskStore interface {
	CreateRemoteTask(Task) error
	UpdateRemoteTask(Task) error
	GetRemoteTask(string) (Task, error)
	ListRemoteTasksBySession(string) ([]Task, error)
}

var (
	// ErrTaskNotOwned 表示任务不属于当前会话。
	ErrTaskNotOwned = fmt.Errorf("task is not owned by current session")
	// ErrHandOffline 表示任务所属 Hand 当前离线。
	ErrHandOffline = fmt.Errorf("task Hand is offline")
)

// TaskService 是进程级后台任务服务。
type TaskService struct {
	hub        *hub.Hub
	pending    func(string, time.Duration, *hub.Peer) (<-chan protocol.Envelope, func())
	store      TaskStore
	locksMu    sync.Mutex
	locks      map[string]*taskLock
	observerMu sync.RWMutex
	observer   func(Task)
}

// OnChange 设置后台任务快照变化观察者。
func (s *TaskService) OnChange(observer func(Task)) {
	s.observerMu.Lock()
	s.observer = observer
	s.observerMu.Unlock()
}

type taskLock struct {
	mu   sync.Mutex
	refs int
}

// NewTaskService 创建共享后台任务服务。
func NewTaskService(authority *Authority, store TaskStore) *TaskService {
	return &TaskService{
		hub: authority.Hub, pending: authority.PendingPeerCall, store: store,
		locks: make(map[string]*taskLock),
	}
}

// CreateStartSnapshot 在发送后台 RPC 前持久化 stale 快照。
func (s *TaskService) CreateStartSnapshot(task Task) error {
	if task.TaskID == "" || task.ArgsDigest == "" {
		return fmt.Errorf("task id and args digest are required")
	}
	if task.SessionID == "" || task.HandID == "" || task.Tool == "" {
		return fmt.Errorf("session, hand and tool are required")
	}
	now := time.Now()
	task.Status = protocol.TaskPending
	task.CreatedAt, task.UpdatedAt, task.Stale = now, now, true
	if err := s.store.CreateRemoteTask(task); err != nil {
		return err
	}
	s.notify(task)
	return nil
}

// ApplyStartResult 保存 durable start run 返回的任务字段。
func (s *TaskService) ApplyStartResult(taskID, sessionID string, result protocol.RPCResult) (Task, error) {
	defer s.lockTask(taskID)()
	task, err := s.authorized(taskID, sessionID)
	if err != nil {
		return Task{}, err
	}
	if result.TaskID != taskID || result.TaskStatus == "" {
		return Task{}, fmt.Errorf("background start result does not identify task %q", taskID)
	}
	task.Status, task.Stale, task.UpdatedAt = result.TaskStatus, false, time.Now()
	if protocol.IsTerminalTaskStatus(result.TaskStatus) {
		// RPCResult only carries status; terminal timestamps and log metadata require a status query.
		task.Stale = true
	}
	if result.Error != "" {
		task.Error = result.Error
	}
	if err := s.store.UpdateRemoteTask(task); err != nil {
		return Task{}, err
	}
	s.notify(task)
	return task, nil
}

// MarkStartStale 保留可查询的合法任务状态并记录启动阶段的不确定结果。
func (s *TaskService) MarkStartStale(taskID, sessionID, message string) (Task, error) {
	defer s.lockTask(taskID)()
	task, err := s.authorized(taskID, sessionID)
	if err != nil {
		return Task{}, err
	}
	task.Stale, task.UpdatedAt = true, time.Now()
	if task.Error == "" {
		task.Error = message
	}
	if err := s.store.UpdateRemoteTask(task); err != nil {
		return Task{}, err
	}
	s.notify(task)
	return task, nil
}

// FailStart 将确定未被 Hand 接纳的后台启动收敛为本地 lost 终态。
func (s *TaskService) FailStart(taskID, sessionID, message string) (Task, error) {
	defer s.lockTask(taskID)()
	task, err := s.authorized(taskID, sessionID)
	if err != nil {
		return Task{}, err
	}
	now := time.Now()
	task.Status, task.FinishedAt, task.UpdatedAt = protocol.TaskLost, now, now
	task.Stale, task.Error = false, message
	if err := s.store.UpdateRemoteTask(task); err != nil {
		return Task{}, err
	}
	s.notify(task)
	return task, nil
}

// Get 返回任务快照；在线时先向当前同 ID Hand 对账。
func (s *TaskService) Get(ctx context.Context, sessionID, taskID string) (Task, error) {
	defer s.lockTask(taskID)()
	task, err := s.authorized(taskID, sessionID)
	if err != nil {
		return Task{}, err
	}
	if err := ctx.Err(); err != nil {
		_, _ = s.markStale(task, err.Error())
		return Task{}, err
	}
	peer, err := s.currentPeer(task.HandID)
	if err != nil {
		return s.markStale(task, "Hand offline")
	}
	return s.refresh(ctx, peer, task)
}

func (s *TaskService) refresh(ctx context.Context, peer *hub.Peer, task Task) (Task, error) {
	reqID := protocol.MustNewMsgID()
	env, err := s.call(ctx, peer, reqID, protocol.TypeTaskStatusReq, protocol.TaskStatusReq{ID: reqID, TaskID: task.TaskID})
	if err != nil {
		_, _ = s.markStale(task, err.Error())
		return Task{}, err
	}
	if env.Type == protocol.TypeError {
		err := decodeRemoteError(env)
		_, _ = s.markStale(task, err.Error())
		return Task{}, err
	}
	resp, err := protocol.DecodePayload[protocol.TaskStatusResp](&env)
	if err != nil || protocol.ValidateTaskStatusResp(resp) != nil || resp.ID != reqID || resp.TaskID != task.TaskID || resp.Tool != task.Tool {
		err := fmt.Errorf("invalid task status response")
		_, _ = s.markStale(task, err.Error())
		return Task{}, err
	}
	task.Status, task.CreatedAt = resp.Status, time.UnixMilli(resp.CreatedAt)
	task.StartedAt, task.FinishedAt = unixMilli(resp.StartedAt), unixMilli(resp.FinishedAt)
	task.LogBytes, task.Truncated, task.Error = resp.LogBytes, resp.Truncated, resp.Error
	task.Stale, task.UpdatedAt = false, time.Now()
	if err := s.store.UpdateRemoteTask(task); err != nil {
		return Task{}, err
	}
	s.notify(task)
	return task, nil
}

func (s *TaskService) markStale(task Task, message string) (Task, error) {
	task.Stale, task.UpdatedAt = true, time.Now()
	if err := s.store.UpdateRemoteTask(task); err != nil {
		return Task{}, err
	}
	s.notify(task)
	return task, nil
}

func (s *TaskService) notify(task Task) {
	s.observerMu.RLock()
	observer := s.observer
	s.observerMu.RUnlock()
	if observer != nil {
		observer(task)
	}
}

// List 返回当前会话拥有的任务。
func (s *TaskService) List(sessionID string) ([]Task, error) {
	return s.store.ListRemoteTasksBySession(sessionID)
}

// ReadLog 从 Hand 按有界字节范围读取日志。
func (s *TaskService) ReadLog(ctx context.Context, sessionID, taskID string, offset int64, limit int) (protocol.TaskLogResp, error) {
	task, err := s.authorized(taskID, sessionID)
	if err != nil {
		return protocol.TaskLogResp{}, err
	}
	reqID := protocol.MustNewMsgID()
	req := protocol.TaskLogReq{ID: reqID, TaskID: taskID, Offset: offset, Limit: limit}
	if err := protocol.ValidateTaskLogReq(req); err != nil {
		return protocol.TaskLogResp{}, err
	}
	peer, err := s.currentPeer(task.HandID)
	if err != nil {
		return protocol.TaskLogResp{}, err
	}
	env, err := s.call(ctx, peer, reqID, protocol.TypeTaskLogReq, req)
	if err != nil {
		return protocol.TaskLogResp{}, err
	}
	if env.Type == protocol.TypeError {
		return protocol.TaskLogResp{}, decodeRemoteError(env)
	}
	resp, err := protocol.DecodePayload[protocol.TaskLogResp](&env)
	if err != nil || protocol.ValidateTaskLogResp(resp) != nil || resp.ID != reqID || resp.TaskID != taskID || resp.Offset != offset {
		return protocol.TaskLogResp{}, fmt.Errorf("invalid task log response")
	}
	return resp, nil
}

// Cancel 请求 Hand 取消任务，不触发第二次交互审批。
func (s *TaskService) Cancel(ctx context.Context, sessionID, taskID, reason string) (protocol.TaskCancelResult, error) {
	defer s.lockTask(taskID)()
	if err := ctx.Err(); err != nil {
		return protocol.TaskCancelResult{}, err
	}
	task, err := s.authorized(taskID, sessionID)
	if err != nil {
		return protocol.TaskCancelResult{}, err
	}
	reqID := protocol.MustNewMsgID()
	req := protocol.TaskCancel{ID: reqID, TaskID: taskID, Reason: reason}
	peer, err := s.currentPeer(task.HandID)
	if err != nil {
		return protocol.TaskCancelResult{}, err
	}
	env, err := s.call(ctx, peer, reqID, protocol.TypeTaskCancel, req)
	if err != nil {
		_, staleErr := s.markStale(task, "task cancellation outcome requires reconciliation: "+err.Error())
		return protocol.TaskCancelResult{}, errors.Join(err, staleErr)
	}
	if env.Type == protocol.TypeError {
		err := decodeRemoteError(env)
		_, staleErr := s.markStale(task, "task cancellation outcome requires reconciliation: "+err.Error())
		return protocol.TaskCancelResult{}, errors.Join(err, staleErr)
	}
	resp, err := protocol.DecodePayload[protocol.TaskCancelResult](&env)
	if err != nil || protocol.ValidateTaskCancelResult(resp) != nil || resp.ID != reqID || resp.TaskID != taskID {
		err := fmt.Errorf("invalid task cancel response")
		_, staleErr := s.markStale(task, err.Error())
		return protocol.TaskCancelResult{}, errors.Join(err, staleErr)
	}
	refreshed, reconcileErr := s.refresh(ctx, peer, task)
	if reconcileErr != nil {
		return resp, fmt.Errorf("task cancel acknowledged as %s but status reconciliation is stale: %w", resp.Status, reconcileErr)
	}
	if resp.Status == protocol.TaskCancelCancelled && refreshed.Status != protocol.TaskCancelled {
		return resp, fmt.Errorf("task cancel acknowledged but reconciled status is %s", refreshed.Status)
	}
	if resp.Status == protocol.TaskCancelFailed {
		return resp, fmt.Errorf("Hand failed to cancel task: %s", resp.Error)
	}
	return resp, nil
}

func (s *TaskService) lockTask(taskID string) func() {
	s.locksMu.Lock()
	lock := s.locks[taskID]
	if lock == nil {
		lock = &taskLock{}
		s.locks[taskID] = lock
	}
	lock.refs++
	s.locksMu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.locksMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.locks, taskID)
		}
		s.locksMu.Unlock()
	}
}

func (s *TaskService) authorized(taskID, sessionID string) (Task, error) {
	task, err := s.store.GetRemoteTask(taskID)
	if err != nil {
		return Task{}, fmt.Errorf("get remote task: %w", err)
	}
	if sessionID == "" || task.SessionID != sessionID {
		return Task{}, ErrTaskNotOwned
	}
	return task, nil
}

func (s *TaskService) currentPeer(handID string) (*hub.Peer, error) {
	peer := s.hub.PeerByType(hub.PeerHand, handID)
	if peer == nil || peer.Type != hub.PeerHand {
		return nil, fmt.Errorf("%w: %q", ErrHandOffline, handID)
	}
	return peer, nil
}

func (s *TaskService) call(ctx context.Context, peer *hub.Peer, reqID, typ string, payload any) (protocol.Envelope, error) {
	ch, cancel := s.pending(reqID, 0, peer)
	defer cancel()
	env, err := protocol.NewEnvelope(reqID, typ, payload)
	if err != nil {
		return protocol.Envelope{}, err
	}
	if err := s.hub.SendPeerContext(ctx, peer, *env); err != nil {
		return protocol.Envelope{}, err
	}
	select {
	case response := <-ch:
		return response, nil
	case <-ctx.Done():
		return protocol.Envelope{}, ctx.Err()
	}
}

func decodeRemoteError(env protocol.Envelope) error {
	msg, err := protocol.DecodePayload[protocol.ErrorMsg](&env)
	if err != nil {
		return err
	}
	return fmt.Errorf("Hand error %s: %s", msg.Code, msg.Message)
}

func unixMilli(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
}
