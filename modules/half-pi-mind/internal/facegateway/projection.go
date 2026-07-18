package facegateway

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type runProjection struct {
	conversationID string
	summary        protocol.RemoteRunSummary
}

func (g *Gateway) conversationSummary(session store.Session) (protocol.ConversationSummary, error) {
	count, err := g.store.GetMessageCount(session.ID)
	if err != nil {
		return protocol.ConversationSummary{}, err
	}
	return protocol.ConversationSummary{
		ConversationID: session.ID, Name: conversationName(session), Mode: session.Mode,
		ActiveHand: session.ActiveHand, MessageCount: count, UpdatedAt: session.UpdatedAt,
	}, nil
}

func conversationName(session store.Session) string {
	if name := strings.TrimSpace(session.Name); name != "" {
		return name
	}
	return session.ID
}

func (g *Gateway) snapshot(identity protocol.FaceIdentity, conversationID string) (protocol.ConversationSnapshot, error) {
	session, err := g.store.GetSession(conversationID)
	if err != nil {
		return protocol.ConversationSnapshot{}, err
	}
	if session == nil || session.GroupID != g.conversations.GroupID() {
		return protocol.ConversationSnapshot{}, fmt.Errorf("conversation %q not found", conversationID)
	}
	messages, err := g.store.GetMessages(conversationID)
	if err != nil {
		return protocol.ConversationSnapshot{}, err
	}
	activeRuns, err := g.activeRuns(conversationID)
	if err != nil {
		return protocol.ConversationSnapshot{}, err
	}
	snapshot := protocol.ConversationSnapshot{
		ConversationID: conversationID, Name: conversationName(*session), Mode: session.Mode,
		ActiveHand:   session.ActiveHand,
		Messages:     make([]protocol.FaceMessage, 0, len(messages)),
		PendingChats: []protocol.ChatSummary{}, PendingApprovals: []protocol.ApprovalSummary{},
		ActiveRuns: activeRuns, Tasks: []protocol.TaskSummary{},
		SnapshotVersion: g.version.Load(),
	}
	if hasScope(identity, protocol.FaceScopeChat) {
		snapshot.PendingChats = g.chats.activeChats(conversationID)
	}
	for _, message := range messages {
		snapshot.Messages = append(snapshot.Messages, protocol.FaceMessage{
			ID: message.ID, Role: message.Role, Content: message.Content,
			ToolID: message.ToolID, Seq: message.Seq, CreatedAt: message.CreatedAt,
		})
	}
	if hasScope(identity, protocol.FaceScopeTasksRead) {
		tasks, err := g.tasks.List(conversationID)
		if err != nil {
			return protocol.ConversationSnapshot{}, err
		}
		snapshot.Tasks, snapshot.TaskHistoryTruncated = snapshotTasks(tasks, protocol.DefaultFaceTaskHistoryLimit)
		snapshot.TaskHistoryLimit = protocol.DefaultFaceTaskHistoryLimit
	}
	return snapshot, nil
}

func (g *Gateway) activeRuns(conversationID string) ([]protocol.RemoteRunSummary, error) {
	byID := make(map[string]runProjection)
	seen := make(map[string]struct{})
	for _, run := range g.authority.Registry.SnapshotsBySession(conversationID) {
		seen[run.ID] = struct{}{}
		if !protocol.IsTerminalRunStatus(run.Status) {
			byID[run.ID] = projectMemoryRun(run)
		}
	}
	records, err := g.store.ListRemoteRunsBySession(conversationID)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if _, exists := seen[record.ID]; exists || protocol.IsTerminalRunStatus(record.Status) {
			continue
		}
		projection := projectStoredRun(record)
		byID[record.ID] = projection
	}
	result := make([]protocol.RemoteRunSummary, 0, len(byID))
	for _, run := range byID {
		result = append(result, run.summary)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].RunID < result[j].RunID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

func (g *Gateway) lookupRun(runID string) (runProjection, bool, error) {
	if run, ok := g.authority.Registry.Snapshot(runID); ok {
		return projectMemoryRun(run), true, nil
	}
	record, err := g.store.GetRemoteRun(runID)
	if errors.Is(err, sql.ErrNoRows) {
		return runProjection{}, false, nil
	}
	if err != nil {
		return runProjection{}, false, err
	}
	return projectStoredRun(record), true, nil
}

func projectMemoryRun(run remoteexec.Run) runProjection {
	finishedAt := timePointer(run.FinishedAt)
	duration := time.Since(run.CreatedAt).Milliseconds()
	if finishedAt != nil {
		duration = finishedAt.Sub(run.CreatedAt).Milliseconds()
	}
	if duration < 0 {
		duration = 0
	}
	requestID := run.Metadata.RequestID
	if requestID == "" {
		requestID = run.ID
	}
	return runProjection{conversationID: run.SessionID, summary: protocol.RemoteRunSummary{
		RunID: run.ID, RequestID: requestID, HandID: run.HandID, Tool: run.Tool,
		Status: run.Status, DurationMs: duration, CreatedAt: run.CreatedAt, FinishedAt: finishedAt,
	}}
}

func projectStoredRun(run store.RemoteRunRecord) runProjection {
	finishedAt := timePointer(run.FinishedAt)
	requestID := run.RequestID
	if requestID == "" {
		requestID = run.ID
	}
	return runProjection{conversationID: run.SessionID, summary: protocol.RemoteRunSummary{
		RunID: run.ID, RequestID: requestID, HandID: run.HandID, Tool: run.Tool,
		Status: run.Status, DurationMs: max(run.DurationMs, 0), CreatedAt: run.CreatedAt, FinishedAt: finishedAt,
	}}
}

func snapshotTasks(tasks []remoteexec.Task, terminalLimit int) ([]protocol.TaskSummary, bool) {
	sortTasks(tasks)
	result := make([]protocol.TaskSummary, 0, len(tasks))
	terminalCount := 0
	truncated := false
	for _, task := range tasks {
		if protocol.IsTerminalTaskStatus(task.Status) {
			if terminalCount >= terminalLimit {
				truncated = true
				continue
			}
			terminalCount++
		}
		result = append(result, projectTask(task))
	}
	return result, truncated
}

func projectTask(task remoteexec.Task) protocol.TaskSummary {
	createdAt, updatedAt := task.CreatedAt, task.UpdatedAt
	if updatedAt.Before(createdAt) {
		updatedAt = createdAt
	}
	startedAt := timePointer(task.StartedAt)
	if startedAt != nil && startedAt.Before(createdAt) {
		startedAt = nil
	}
	finishedAt := timePointer(task.FinishedAt)
	if protocol.IsTerminalTaskStatus(task.Status) && finishedAt == nil {
		finished := updatedAt
		if finished.Before(createdAt) {
			finished = createdAt
		}
		finishedAt = &finished
	}
	if !protocol.IsTerminalTaskStatus(task.Status) {
		finishedAt = nil
	}
	if finishedAt != nil {
		if finishedAt.Before(createdAt) {
			*finishedAt = createdAt
		}
		if startedAt != nil && finishedAt.Before(*startedAt) {
			*finishedAt = *startedAt
		}
		if updatedAt.Before(*finishedAt) {
			updatedAt = *finishedAt
		}
	}
	errorCode := taskErrorCode(task.Status)
	return protocol.TaskSummary{
		TaskID: task.TaskID, ConversationID: task.SessionID, HandID: task.HandID,
		Tool: task.Tool, ArgsDigest: task.ArgsDigest, Status: task.Status,
		CreatedAt: createdAt, StartedAt: startedAt, FinishedAt: finishedAt, UpdatedAt: updatedAt,
		LogBytes: max(task.LogBytes, 0), Truncated: task.Truncated, Stale: task.Stale,
		ErrorCode: errorCode, Error: taskErrorMessage(task.Status),
	}
}

func taskErrorMessage(status protocol.TaskStatus) string {
	switch status {
	case protocol.TaskFailed:
		return "task failed"
	case protocol.TaskCancelled:
		return "task cancelled"
	case protocol.TaskTimedOut:
		return "task timed out"
	case protocol.TaskLost:
		return "task state was lost"
	default:
		return ""
	}
}

func taskErrorCode(status protocol.TaskStatus) protocol.FaceErrorCode {
	switch status {
	case protocol.TaskFailed:
		return protocol.FaceErrorTaskFailed
	case protocol.TaskCancelled:
		return protocol.FaceErrorTaskCancelled
	case protocol.TaskTimedOut:
		return protocol.FaceErrorTaskTimedOut
	case protocol.TaskLost:
		return protocol.FaceErrorTaskLost
	default:
		return ""
	}
}

func sortTasks(tasks []remoteexec.Task) {
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].UpdatedAt.Equal(tasks[j].UpdatedAt) {
			return tasks[i].TaskID > tasks[j].TaskID
		}
		return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt)
	})
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}
