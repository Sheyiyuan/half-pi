package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestFaceTaskCommandsStrictValidation(t *testing.T) {
	tests := []struct {
		typ     string
		payload any
	}{
		{TypeFaceTaskList, FaceTaskList{RequestID: "req-1", ConversationID: "conv-1", HandID: "hand-1", Statuses: []TaskStatus{TaskPending, TaskRunning}, Cursor: "opaque", Limit: MaxFaceTaskListLimit}},
		{TypeFaceTaskGet, FaceTaskGet{RequestID: "req-2", ConversationID: "conv-1", TaskID: "task-1"}},
		{TypeFaceTaskLog, FaceTaskLog{RequestID: "req-3", ConversationID: "conv-1", TaskID: "task-1", Offset: 7, Limit: MaxTaskLogResponseBytes}},
		{TypeFaceTaskCancel, FaceTaskCancel{RequestID: "req-4", ConversationID: "conv-1", TaskID: "task-1", Reason: "user"}},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			payload, err := json.Marshal(tt.payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateFacePayload(tt.typ, payload); err != nil {
				t.Fatalf("valid payload rejected: %v", err)
			}
			if err := ValidateFacePayload(tt.typ, append(payload, []byte(` {}`)...)); err == nil {
				t.Fatal("trailing JSON accepted")
			}
			var object map[string]any
			if err := json.Unmarshal(payload, &object); err != nil {
				t.Fatal(err)
			}
			object["unknown"] = true
			unknown, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateFacePayload(tt.typ, unknown); err == nil {
				t.Fatal("unknown field accepted")
			}
		})
	}
}

func TestFaceTaskCommandBounds(t *testing.T) {
	invalid := []struct {
		typ     string
		payload string
	}{
		{TypeFaceTaskList, `{"request_id":"req-1"}`},
		{TypeFaceTaskList, `{"request_id":"req-1","conversation_id":"conv-1","limit":-1}`},
		{TypeFaceTaskList, `{"request_id":"req-1","conversation_id":"conv-1","limit":201}`},
		{TypeFaceTaskList, `{"request_id":"req-1","conversation_id":"conv-1","statuses":["running","running"]}`},
		{TypeFaceTaskList, `{"request_id":"req-1","conversation_id":"conv-1","statuses":["unknown"]}`},
		{TypeFaceTaskGet, `{"request_id":"req-1","conversation_id":"conv-1"}`},
		{TypeFaceTaskLog, `{"request_id":"req-1","conversation_id":"conv-1","task_id":"task-1","offset":-1,"limit":1}`},
		{TypeFaceTaskLog, `{"request_id":"req-1","conversation_id":"conv-1","task_id":"task-1","offset":0,"limit":0}`},
		{TypeFaceTaskLog, `{"request_id":"req-1","conversation_id":"conv-1","task_id":"task-1","offset":0,"limit":65537}`},
		{TypeFaceTaskCancel, `{"request_id":"req-1","conversation_id":"conv-1"}`},
	}
	for _, tt := range invalid {
		if err := ValidateFacePayload(tt.typ, json.RawMessage(tt.payload)); err == nil {
			t.Errorf("invalid %s payload accepted: %s", tt.typ, tt.payload)
		}
	}
}

func TestFaceTaskResultDataStrictValidation(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	task := validTaskSummary(now, TaskRunning)
	tests := []struct {
		operation FaceOperation
		data      any
	}{
		{FaceOperationTaskList, TaskListResult{Tasks: []TaskSummary{task}, NextCursor: "next"}},
		{FaceOperationTaskGet, TaskGetResult{Task: task}},
		{FaceOperationTaskLog, TaskLogResult{TaskID: "task-1", Offset: 3, NextOffset: 6, Data: []byte{0, 1, 2}, EOF: false, Truncated: true}},
		{FaceOperationTaskCancel, FaceTaskCancelResult{Outcome: string(TaskCancelCancelled), Task: task}},
	}
	for _, tt := range tests {
		data, err := json.Marshal(tt.data)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateFaceResultData(tt.operation, data); err != nil {
			t.Errorf("valid %s data rejected: %v", tt.operation, err)
		}
		if err := ValidateFaceResultData(tt.operation, append(data, []byte(` null`)...)); err == nil {
			t.Errorf("trailing %s result data accepted", tt.operation)
		}
	}

	logJSON, err := json.Marshal(TaskLogResult{TaskID: "task-1", Data: []byte{0, 255}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(logJSON, []byte(base64.StdEncoding.EncodeToString([]byte{0, 255}))) {
		t.Fatalf("task log bytes are not base64 encoded: %s", logJSON)
	}

	invalid := []struct {
		operation FaceOperation
		data      string
	}{
		{FaceOperationTaskList, `{"tasks":null}`},
		{FaceOperationTaskLog, `{"task_id":"task-1","offset":0,"next_offset":0,"data":null,"eof":true,"truncated":false}`},
		{FaceOperationTaskLog, `{"task_id":"task-1","offset":9223372036854775807,"next_offset":0,"data":"eA==","eof":false,"truncated":false}`},
		{FaceOperationTaskGet, `{"task":{"task_id":"task-1"}}`},
		{FaceOperationTaskLog, `{"task_id":"task-1","offset":2,"next_offset":4,"data":"AA==","eof":false,"truncated":false}`},
		{FaceOperationTaskLog, `{"task_id":"task-1","offset":0,"next_offset":0,"data":"***","eof":false,"truncated":false}`},
		{FaceOperationTaskCancel, `{"outcome":"other","task":{"task_id":"task-1"}}`},
		{FaceOperationTaskGet, `{"task":{},"secret":"token"}`},
	}
	older := task
	older.TaskID = "task-2"
	older.UpdatedAt = task.UpdatedAt.Add(-time.Second)
	unsorted, err := json.Marshal(TaskListResult{Tasks: []TaskSummary{older, task}})
	if err != nil {
		t.Fatal(err)
	}
	invalid = append(invalid, struct {
		operation FaceOperation
		data      string
	}{FaceOperationTaskList, string(unsorted)})
	tooMany := make([]TaskSummary, MaxFaceTaskListLimit+1)
	for i := range tooMany {
		tooMany[i] = task
		tooMany[i].TaskID = string(rune(MaxFaceTaskListLimit + 1 - i))
	}
	tooManyJSON, err := json.Marshal(TaskListResult{Tasks: tooMany})
	if err != nil {
		t.Fatal(err)
	}
	invalid = append(invalid, struct {
		operation FaceOperation
		data      string
	}{FaceOperationTaskList, string(tooManyJSON)})
	for _, tt := range invalid {
		if err := ValidateFaceResultData(tt.operation, json.RawMessage(tt.data)); err == nil {
			t.Errorf("invalid %s result data accepted: %s", tt.operation, tt.data)
		}
	}
}

func TestTaskSummaryStatusErrorInvariant(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	statuses := []TaskStatus{TaskPending, TaskRunning, TaskSucceeded, TaskFailed, TaskCancelled, TaskTimedOut, TaskLost}
	for _, status := range statuses {
		task := validTaskSummary(now, status)
		data, err := json.Marshal(TaskGetResult{Task: task})
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateFaceResultData(FaceOperationTaskGet, data); err != nil {
			t.Errorf("status %s rejected: %v", status, err)
		}
		task.ErrorCode = FaceErrorTaskStale
		data, err = json.Marshal(TaskGetResult{Task: task})
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateFaceResultData(FaceOperationTaskGet, data); err == nil {
			t.Errorf("status %s accepted mismatched error code", status)
		}
	}
}

func TestFaceResultTruthTable(t *testing.T) {
	valid := []FaceResult{
		{RequestID: "req-1", Status: FaceResultSucceeded},
		{RequestID: "req-1", Status: FaceResultFailed, ErrorCode: FaceErrorTaskFailed, Error: "failed"},
		{RequestID: "req-1", Status: FaceResultCancelled, ErrorCode: FaceErrorCancelled, Error: "cancelled"},
		{RequestID: "req-1", Status: FaceResultTimedOut, ErrorCode: FaceErrorTimeout, Error: "timed out"},
	}
	for _, result := range valid {
		assertFacePayloadValidity(t, TypeFaceResult, result, true)
	}
	invalid := []FaceResult{
		{RequestID: "req-1", Status: FaceResultSucceeded, ErrorCode: FaceErrorInternal},
		{RequestID: "req-1", Status: FaceResultSucceeded, Error: "bad"},
		{RequestID: "req-1", Status: FaceResultFailed},
		{RequestID: "req-1", Status: FaceResultCancelled, ErrorCode: FaceErrorCancelled},
		{RequestID: "req-1", Status: FaceResultTimedOut, Error: "timed out"},
	}
	for _, result := range invalid {
		assertFacePayloadValidity(t, TypeFaceResult, result, false)
	}
}

func TestFaceFrozenSnapshotAndSubscriptionInvariants(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	base := ConversationSnapshot{
		ConversationID: "conv-1", Name: "Work", Mode: "normal", Messages: []FaceMessage{},
		PendingChats: []ChatSummary{}, PendingApprovals: []ApprovalSummary{}, ActiveRuns: []RemoteRunSummary{},
		Tasks: []TaskSummary{}, TaskHistoryLimit: MaxFaceTaskHistoryLimit, SnapshotVersion: 1,
	}
	assertFacePayloadValidity(t, TypeFaceSnapshot, FaceSnapshot{RequestID: "req-1", Snapshot: base}, true)

	invalidSnapshots := []ConversationSnapshot{base, base, base, base}
	invalidSnapshots[0].SnapshotVersion = 0
	invalidSnapshots[1].TaskHistoryLimit = MaxFaceTaskHistoryLimit + 1
	invalidSnapshots[2].Tasks = nil
	invalidSnapshots[3].ActiveRuns = []RemoteRunSummary{{RunID: "run-1", RequestID: "req-1", HandID: "hand-1", Tool: "read_file", Status: RunSucceeded, CreatedAt: now}}
	terminal := validTaskSummary(now, TaskSucceeded)
	tooManyTerminals := base
	tooManyTerminals.Tasks = []TaskSummary{terminal}
	tooManyTerminals.TaskHistoryLimit = 0
	invalidSnapshots = append(invalidSnapshots, tooManyTerminals)
	for _, snapshot := range invalidSnapshots {
		assertFacePayloadValidity(t, TypeFaceSnapshot, FaceSnapshot{RequestID: "req-1", Snapshot: snapshot}, false)
	}

	assertFacePayloadValidity(t, TypeFaceSubscribe, FaceSubscribe{RequestID: "req-1"}, true)
	assertFacePayloadValidity(t, TypeFaceSubscribe, FaceSubscribe{RequestID: "req-1", ConversationIDs: []string{"conv-1", "conv-1"}}, false)
	assertFacePayloadValidity(t, TypeFaceSubscribe, FaceSubscribe{RequestID: "req-1", EventTypes: []FaceEventType{FaceEventTaskChanged, FaceEventTaskChanged}}, false)
}

func TestFaceTaskEventCorrelation(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	task := validTaskSummary(now, TaskRunning)
	event := validFaceEvent(t, FaceEventTaskChanged, task, now)
	assertFacePayloadValidity(t, TypeFaceEvent, event, true)
	event.ConversationID = "conv-2"
	assertFacePayloadValidity(t, TypeFaceEvent, event, false)

	handEvent := validFaceEvent(t, FaceEventHandConnected, HandConnectedEventData{HandID: "hand-1", Hostname: "dev", OS: "linux", Arch: "amd64"}, now)
	handEvent.RequestID = "req-1"
	assertFacePayloadValidity(t, TypeFaceEvent, handEvent, false)

	chatEvent := validFaceEvent(t, FaceEventChatStarted, ChatStartedEventData{RequestID: "other"}, now)
	assertFacePayloadValidity(t, TypeFaceEvent, chatEvent, false)
	approvalEvent := validFaceEvent(t, FaceEventApprovalRequested, ApprovalRequestedEventData{
		ApprovalID: "approval-1", ConversationID: "other", RequestID: "req-1", Tool: "write_file",
		Reason: "write", ArgsDigest: "sha256:abc", ExpiresAt: now.Add(time.Minute),
	}, now)
	assertFacePayloadValidity(t, TypeFaceEvent, approvalEvent, false)
}

func TestApprovalRequestIDIsOptionalAndCorrelatedWhenPresent(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	approval := ApprovalRequestedEventData{
		ApprovalID: "approval-1", ConversationID: "conv-1", Tool: "write_file",
		Reason: "write", ArgsDigest: "sha256:abc", ExpiresAt: now.Add(time.Minute),
	}
	event := validFaceEvent(t, FaceEventApprovalRequested, approval, now)
	event.RequestID = ""
	assertFacePayloadValidity(t, TypeFaceEvent, event, true)

	approval.RequestID = "req-1"
	event = validFaceEvent(t, FaceEventApprovalRequested, approval, now)
	event.RequestID = "req-1"
	assertFacePayloadValidity(t, TypeFaceEvent, event, true)
	event.RequestID = "other"
	assertFacePayloadValidity(t, TypeFaceEvent, event, false)
}

func TestValidateFaceScopes(t *testing.T) {
	all := []FaceScope{FaceScopeChat, FaceScopeSessionsRead, FaceScopeSessionsWrite, FaceScopeRunsRead, FaceScopeRunsCancel, FaceScopeApprove, FaceScopeHandsRead, FaceScopeTasksRead, FaceScopeTasksCancel}
	if err := ValidateFaceScopes(all); err != nil {
		t.Fatalf("all scopes rejected: %v", err)
	}
	for _, scopes := range [][]FaceScope{nil, {FaceScopeTasksRead, FaceScopeTasksRead}, {"face:unknown"}} {
		if err := ValidateFaceScopes(scopes); err == nil {
			t.Errorf("invalid scopes accepted: %v", scopes)
		}
	}
}

func TestFaceTaskEnumsAreAccepted(t *testing.T) {
	operations := []FaceOperation{FaceOperationTaskList, FaceOperationTaskGet, FaceOperationTaskLog, FaceOperationTaskCancel}
	for _, operation := range operations {
		assertFacePayloadValidity(t, TypeFaceAccepted, FaceAccepted{RequestID: "req-1", Operation: operation}, true)
	}
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	assertFacePayloadValidity(t, TypeFaceEvent, validFaceEvent(t, FaceEventTaskChanged, validTaskSummary(now, TaskRunning), now), true)
}

func TestFaceTaskDTOsDoNotDeclareSessionID(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(FaceTaskList{}), reflect.TypeOf(FaceTaskGet{}), reflect.TypeOf(FaceTaskLog{}), reflect.TypeOf(FaceTaskCancel{}),
		reflect.TypeOf(TaskSummary{}), reflect.TypeOf(TaskListResult{}), reflect.TypeOf(TaskGetResult{}),
		reflect.TypeOf(TaskLogResult{}), reflect.TypeOf(FaceTaskCancelResult{}),
	}
	seen := make(map[reflect.Type]bool)
	for _, typ := range types {
		assertNoSessionIDJSONTag(t, typ, seen)
	}
}

func validTaskSummary(now time.Time, status TaskStatus) TaskSummary {
	task := TaskSummary{
		TaskID: "task-1", ConversationID: "conv-1", HandID: "hand-1", Tool: "exec_command",
		ArgsDigest: "sha256:abc", Status: status, CreatedAt: now, UpdatedAt: now, LogBytes: 3,
	}
	if status != TaskPending {
		task.StartedAt = &task.CreatedAt
	}
	if IsTerminalTaskStatus(status) {
		task.FinishedAt = &task.UpdatedAt
		task.ErrorCode = taskStatusErrorCode(status)
		if task.ErrorCode != "" {
			task.Error = "task ended"
		}
	}
	return task
}

func assertFacePayloadValidity(t *testing.T, typ string, value any, valid bool) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateFacePayload(typ, payload)
	if valid && err != nil {
		t.Errorf("valid payload rejected: %v\npayload: %s", err, payload)
	}
	if !valid && err == nil {
		t.Errorf("invalid payload accepted: %s", payload)
	}
}
