package facegateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

const taskCursorVersion = 1

var errInvalidTaskCursor = errors.New("invalid task cursor")

type taskCursor struct {
	Version        int                   `json:"version"`
	ConversationID string                `json:"conversation_id"`
	HandID         string                `json:"hand_id,omitempty"`
	Statuses       []protocol.TaskStatus `json:"statuses"`
	UpdatedAt      int64                 `json:"updated_at"`
	TaskID         string                `json:"task_id"`
}

func (g *Gateway) listTasks(request protocol.FaceTaskList) (protocol.TaskListResult, error) {
	limit := request.Limit
	if limit == 0 {
		limit = protocol.DefaultFaceTaskListLimit
	}
	statuses := append([]protocol.TaskStatus(nil), request.Statuses...)
	slices.Sort(statuses)
	var cursor taskCursor
	if request.Cursor != "" {
		var err error
		cursor, err = g.decodeTaskCursor(request.Cursor)
		if err != nil || cursor.ConversationID != request.ConversationID || cursor.HandID != request.HandID || !slices.Equal(cursor.Statuses, statuses) {
			return protocol.TaskListResult{}, errInvalidTaskCursor
		}
	}
	tasks, err := g.tasks.List(request.ConversationID)
	if err != nil {
		return protocol.TaskListResult{}, err
	}
	sortTasks(tasks)
	statusFilter := make(map[protocol.TaskStatus]struct{}, len(statuses))
	for _, status := range statuses {
		statusFilter[status] = struct{}{}
	}
	filtered := make([]remoteexec.Task, 0, len(tasks))
	for _, task := range tasks {
		if request.HandID != "" && task.HandID != request.HandID {
			continue
		}
		if len(statusFilter) > 0 {
			if _, ok := statusFilter[task.Status]; !ok {
				continue
			}
		}
		if request.Cursor != "" && !taskAfterCursor(task, cursor) {
			continue
		}
		filtered = append(filtered, task)
	}
	pageSize := min(limit, len(filtered))
	result := protocol.TaskListResult{Tasks: make([]protocol.TaskSummary, 0, pageSize)}
	for _, task := range filtered[:pageSize] {
		result.Tasks = append(result.Tasks, projectTask(task))
	}
	if len(filtered) > pageSize {
		last := filtered[pageSize-1]
		result.NextCursor, err = g.encodeTaskCursor(taskCursor{
			Version: taskCursorVersion, ConversationID: request.ConversationID,
			HandID: request.HandID, Statuses: statuses,
			UpdatedAt: last.UpdatedAt.UnixNano(), TaskID: last.TaskID,
		})
		if err != nil {
			return protocol.TaskListResult{}, err
		}
	}
	return result, nil
}

func taskAfterCursor(task remoteexec.Task, cursor taskCursor) bool {
	anchor := time.Unix(0, cursor.UpdatedAt)
	return task.UpdatedAt.Before(anchor) || (task.UpdatedAt.Equal(anchor) && task.TaskID < cursor.TaskID)
}

func (g *Gateway) encodeTaskCursor(cursor taskCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode task cursor: %w", err)
	}
	mac := hmac.New(sha256.New, g.cursorKey[:])
	_, _ = mac.Write(payload)
	signed := append(payload, mac.Sum(nil)...)
	return base64.RawURLEncoding.EncodeToString(signed), nil
}

func (g *Gateway) decodeTaskCursor(encoded string) (taskCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) <= sha256.Size {
		return taskCursor{}, errInvalidTaskCursor
	}
	payload, signature := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, g.cursorKey[:])
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return taskCursor{}, errInvalidTaskCursor
	}
	cursor, err := protocol.StrictDecode[taskCursor](payload)
	if err != nil || cursor.Version != taskCursorVersion || cursor.ConversationID == "" || cursor.TaskID == "" || cursor.UpdatedAt <= 0 {
		return taskCursor{}, errInvalidTaskCursor
	}
	if !slices.IsSorted(cursor.Statuses) {
		return taskCursor{}, errInvalidTaskCursor
	}
	return cursor, nil
}
