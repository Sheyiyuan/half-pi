package remoteexec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

type memoryTaskStore struct {
	mu    sync.Mutex
	tasks map[string]Task
}

func (s *memoryTaskStore) CreateRemoteTask(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tasks == nil {
		s.tasks = make(map[string]Task)
	}
	s.tasks[task.TaskID] = task
	return nil
}
func (s *memoryTaskStore) UpdateRemoteTask(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.tasks[task.TaskID]
	if protocol.IsTerminalTaskStatus(current.Status) && current.Status != task.Status {
		return errors.New("terminal task cannot regress")
	}
	s.tasks[task.TaskID] = task
	return nil
}
func (s *memoryTaskStore) GetRemoteTask(id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return Task{}, errors.New("not found")
	}
	return task, nil
}
func (s *memoryTaskStore) ListRemoteTasksBySession(sessionID string) ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tasks []Task
	for _, task := range s.tasks {
		if task.SessionID == sessionID {
			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}

func TestTaskServiceDeniesCrossSessionAndKeepsOfflineSnapshotStale(t *testing.T) {
	store := &memoryTaskStore{tasks: map[string]Task{"task-1": {
		TaskID: "task-1", SessionID: "session-a", HandID: "hand-1", Tool: "tool",
		ArgsDigest: "digest", Status: protocol.TaskRunning, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}}}
	authority := NewAuthority(hub.New(), NewRegistry(), nil)
	service := NewTaskService(authority, store)
	if _, err := service.Get(context.Background(), "session-b", "task-1"); !errors.Is(err, ErrTaskNotOwned) {
		t.Fatalf("cross-session error = %v", err)
	}
	task, err := service.Get(context.Background(), "session-a", "task-1")
	if err != nil || !task.Stale || task.Status != protocol.TaskRunning {
		t.Fatalf("offline snapshot = %+v, err=%v", task, err)
	}
}

func TestTaskServiceGetReturnsCallerCancellationWhileMarkingStale(t *testing.T) {
	store := &memoryTaskStore{tasks: map[string]Task{"task-1": {
		TaskID: "task-1", SessionID: "session-a", HandID: "hand-1", Tool: "tool",
		Status: protocol.TaskPending, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}}}
	service := NewTaskService(NewAuthority(hub.New(), NewRegistry(), nil), store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Get(ctx, "session-a", "task-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get error = %v, want context cancellation", err)
	}
	task, _ := store.GetRemoteTask("task-1")
	if !task.Stale || task.Status != protocol.TaskPending {
		t.Fatalf("stale task = %+v", task)
	}
}

func TestTaskServiceStartRunCanSucceedBeforeTaskLaterFails(t *testing.T) {
	store := &memoryTaskStore{}
	authority := NewAuthority(hub.New(), NewRegistry(), nil)
	service := NewTaskService(authority, store)
	if err := service.CreateStartSnapshot(Task{
		TaskID: "task-1", SessionID: "session-a", HandID: "hand-1", Tool: "tool", ArgsDigest: "digest",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := service.ApplyStartResult("task-1", "session-a", protocol.RPCResult{
		RunID: "task-1", Success: true, TaskID: "task-1", TaskStatus: protocol.TaskRunning,
	})
	if err != nil || task.Status != protocol.TaskRunning || task.Stale {
		t.Fatalf("start snapshot = %+v, err=%v", task, err)
	}
	task.Status, task.Error, task.Stale, task.FinishedAt, task.UpdatedAt = protocol.TaskFailed, "later", false, time.Now(), time.Now()
	if err := store.UpdateRemoteTask(task); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetRemoteTask("task-1")
	if got.Status != protocol.TaskFailed || got.Error != "later" {
		t.Fatalf("later task snapshot = %+v", got)
	}
}

func TestTaskServiceTerminalStartResultRequiresReconciliation(t *testing.T) {
	store := &memoryTaskStore{}
	service := NewTaskService(NewAuthority(hub.New(), NewRegistry(), nil), store)
	if err := service.CreateStartSnapshot(Task{
		TaskID: "task-1", SessionID: "session-a", HandID: "hand-1", Tool: "tool", ArgsDigest: "digest",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := service.ApplyStartResult("task-1", "session-a", protocol.RPCResult{
		RunID: "task-1", Success: true, TaskID: "task-1", TaskStatus: protocol.TaskSucceeded,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !task.Stale || !task.FinishedAt.IsZero() {
		t.Fatalf("terminal start snapshot = %+v", task)
	}
}

func TestTaskServiceStaleSnapshotPreservesExecutionError(t *testing.T) {
	store := &memoryTaskStore{tasks: map[string]Task{"task-1": {
		TaskID: "task-1", SessionID: "session-a", HandID: "hand-1", Tool: "tool",
		Status: protocol.TaskFailed, Error: "execution failed", CreatedAt: time.Now(), FinishedAt: time.Now(), UpdatedAt: time.Now(),
	}}}
	service := NewTaskService(NewAuthority(hub.New(), NewRegistry(), nil), store)
	task, err := service.Get(context.Background(), "session-a", "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !task.Stale || task.Error != "execution failed" {
		t.Fatalf("offline snapshot = %+v", task)
	}
}

func TestTaskServiceFailStartCreatesTerminalTask(t *testing.T) {
	store := &memoryTaskStore{}
	service := NewTaskService(NewAuthority(hub.New(), NewRegistry(), nil), store)
	if err := service.CreateStartSnapshot(Task{
		TaskID: "task-1", SessionID: "session-a", HandID: "hand-1", Tool: "tool", ArgsDigest: "digest",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := service.FailStart("task-1", "session-a", "rejected")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != protocol.TaskLost || task.Stale || task.FinishedAt.IsZero() || task.Error != "rejected" {
		t.Fatalf("failed start snapshot = %+v", task)
	}
}

func TestTaskServiceLocksOnlySameTask(t *testing.T) {
	store := &memoryTaskStore{tasks: map[string]Task{
		"task-a": {TaskID: "task-a", SessionID: "session-a", Status: protocol.TaskPending},
		"task-b": {TaskID: "task-b", SessionID: "session-a", Status: protocol.TaskPending},
	}}
	service := NewTaskService(NewAuthority(hub.New(), NewRegistry(), nil), store)
	unlockA := service.lockTask("task-a")

	differentDone := make(chan error, 1)
	go func() {
		_, err := service.ApplyStartResult("task-b", "session-a", protocol.RPCResult{
			RunID: "task-b", Success: true, TaskID: "task-b", TaskStatus: protocol.TaskRunning,
		})
		differentDone <- err
	}()
	select {
	case err := <-differentDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("task-b was blocked by task-a lock")
	}

	sameDone := make(chan error, 1)
	go func() {
		_, err := service.ApplyStartResult("task-a", "session-a", protocol.RPCResult{
			RunID: "task-a", Success: true, TaskID: "task-a", TaskStatus: protocol.TaskRunning,
		})
		sameDone <- err
	}()
	select {
	case err := <-sameDone:
		t.Fatalf("task-a operation completed while its lock was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	unlockA()
	select {
	case err := <-sameDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("task-a operation remained blocked after unlock")
	}
}
