package hand

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

type taskStatus int

const (
	taskRunning taskStatus = iota
	taskDone
	taskCancelled
)

const (
	cancelTaskWaitTimeout  = 1500 * time.Millisecond
	completedTaskRetention = 10 * time.Minute
	maxRetainedTasks       = 1000
)

type task struct {
	tool            string
	cancel          context.CancelFunc
	done            chan struct{}
	status          taskStatus
	cancelRequested bool
	accepted        chan struct{}
	finishedAt      time.Time
}

func (h *Hand) acceptTask(runID, tool string, cancel context.CancelFunc) (bool, error) {
	h.tasksMu.Lock()
	h.pruneTasksLocked(time.Now())
	if existing, exists := h.tasks[runID]; exists {
		accepted := existing.accepted
		h.tasksMu.Unlock()
		<-accepted
		return false, nil
	}
	t := &task{tool: tool, cancel: cancel, done: make(chan struct{}), accepted: make(chan struct{}), status: taskRunning}
	h.tasks[runID] = t
	h.tasksMu.Unlock()

	err := h.sendRPCMessage(protocol.TypeRPCAccepted, protocol.RPCAccepted{
		RunID:     runID,
		StartedAt: time.Now().UnixMilli(),
	})
	h.tasksMu.Lock()
	if err != nil {
		delete(h.tasks, runID)
		cancel()
	}
	close(t.accepted)
	h.tasksMu.Unlock()
	return err == nil, err
}

func (h *Hand) taskCancellationRequested(runID string) bool {
	h.tasksMu.Lock()
	defer h.tasksMu.Unlock()
	t := h.tasks[runID]
	return t != nil && t.cancelRequested
}

func (h *Hand) finishTask(runID string, status taskStatus) {
	h.tasksMu.Lock()
	defer h.tasksMu.Unlock()
	t := h.tasks[runID]
	if t == nil || t.status != taskRunning {
		return
	}
	t.status = status
	t.finishedAt = time.Now()
	close(t.done)
}

func (h *Hand) handleRPCCancel(env protocol.Envelope) {
	cancelReq, err := protocol.DecodePayload[protocol.RPCCancel](&env)
	if err != nil || protocol.ValidateRPCCancel(cancelReq) != nil {
		return
	}

	h.tasksMu.Lock()
	t, exists := h.tasks[cancelReq.RunID]
	if !exists {
		h.tasksMu.Unlock()
		h.sendCancelResult(cancelReq.RunID, protocol.CancelUnknownRun, "")
		return
	}
	switch t.status {
	case taskDone:
		h.tasksMu.Unlock()
		h.sendCancelResult(cancelReq.RunID, protocol.CancelAlreadyDone, "")
		return
	case taskCancelled:
		h.tasksMu.Unlock()
		h.sendCancelResult(cancelReq.RunID, protocol.CancelCancelled, "")
		return
	}
	t.cancelRequested = true
	t.cancel()
	done := t.done
	h.tasksMu.Unlock()

	select {
	case <-done:
		h.tasksMu.Lock()
		status := t.status
		h.tasksMu.Unlock()
		if status == taskCancelled {
			h.sendCancelResult(cancelReq.RunID, protocol.CancelCancelled, "")
		} else {
			h.sendCancelResult(cancelReq.RunID, protocol.CancelAlreadyDone, "")
		}
	case <-time.After(cancelTaskWaitTimeout):
		h.sendCancelResult(cancelReq.RunID, protocol.CancelFailed, "task did not stop before cancel timeout")
	}
}

func (h *Hand) pruneTasksLocked(now time.Time) {
	for runID, task := range h.tasks {
		if task.status != taskRunning && now.Sub(task.finishedAt) >= completedTaskRetention {
			delete(h.tasks, runID)
		}
	}
	for len(h.tasks) >= maxRetainedTasks {
		var oldestID string
		var oldest time.Time
		for runID, task := range h.tasks {
			if task.status == taskRunning || task.finishedAt.IsZero() {
				continue
			}
			if oldestID == "" || task.finishedAt.Before(oldest) {
				oldestID, oldest = runID, task.finishedAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(h.tasks, oldestID)
	}
}

func (h *Hand) sendCancelResult(runID string, status protocol.CancelStatus, errMsg string) {
	if err := h.sendRPCMessage(protocol.TypeRPCCancelResult, protocol.RPCCancelResult{
		RunID:  runID,
		Status: status,
		Error:  errMsg,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "send rpc cancel result failed: %v\n", err)
	}
}
