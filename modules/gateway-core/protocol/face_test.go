package protocol

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIsFaceMessageType(t *testing.T) {
	types := []string{
		TypeFaceChat, TypeFaceChatCancel, TypeFaceConversationList,
		TypeFaceConversationCreate, TypeFaceConversationSnapshot,
		TypeFaceConversationRename, TypeFaceSubscribe, TypeFaceApprovalResolve,
		TypeFaceRunGet, TypeFaceRunCancel, TypeFaceHandList, TypeFaceHandGet,
		TypeFaceTaskList, TypeFaceTaskGet, TypeFaceTaskLog, TypeFaceTaskCancel,
		TypeFaceAccepted, TypeFaceResult, TypeFaceError, TypeFaceSnapshot, TypeFaceEvent,
	}
	for _, typ := range types {
		if !IsFaceMessageType(typ) {
			t.Errorf("IsFaceMessageType(%q) = false", typ)
		}
	}
	for _, typ := range []string{"", TypeRPC, "face.unknown", "chat.started"} {
		if IsFaceMessageType(typ) {
			t.Errorf("IsFaceMessageType(%q) = true", typ)
		}
	}
}

func TestFaceProtocolEnumValues(t *testing.T) {
	scopes := []FaceScope{
		FaceScopeChat, FaceScopeSessionsRead, FaceScopeSessionsWrite, FaceScopeRunsRead,
		FaceScopeRunsCancel, FaceScopeApprove, FaceScopeHandsRead,
		FaceScopeTasksRead, FaceScopeTasksCancel,
	}
	if got, want := strings.Join(faceScopeStrings(scopes), ","), "face:chat,face:sessions:read,face:sessions:write,face:runs:read,face:runs:cancel,face:approve,face:hands:read,face:tasks:read,face:tasks:cancel"; got != want {
		t.Fatalf("scope values = %q, want %q", got, want)
	}

	errorCodes := []FaceErrorCode{
		FaceErrorInvalidRequest, FaceErrorUnauthorized, FaceErrorForbidden,
		FaceErrorConversationNotFound, FaceErrorRequestConflict, FaceErrorRequestInProgress,
		FaceErrorApprovalNotFound, FaceErrorApprovalExpired, FaceErrorRunNotFound,
		FaceErrorHandNotFound, FaceErrorTaskFailed, FaceErrorTaskCancelled, FaceErrorTaskTimedOut,
		FaceErrorTaskLost, FaceErrorTaskStale, FaceErrorTaskNotFound, FaceErrorHandOffline,
		FaceErrorLogUnavailable, FaceErrorBusy, FaceErrorCancelled, FaceErrorTimeout, FaceErrorInternal,
	}
	for _, code := range errorCodes {
		if !validFaceErrorCode(code) {
			t.Errorf("documented error code %q is not valid", code)
		}
	}
	for _, status := range []FaceResultStatus{FaceResultSucceeded, FaceResultFailed, FaceResultCancelled, FaceResultTimedOut} {
		if !validFaceResultStatus(status) {
			t.Errorf("documented result status %q is not valid", status)
		}
	}
	for _, decision := range []FaceApprovalDecision{FaceApprovalAllowOnce, FaceApprovalDenyOnce, FaceApprovalAllowSession, FaceApprovalDenySession} {
		if !validFaceApprovalDecision(decision) {
			t.Errorf("documented approval decision %q is not valid", decision)
		}
	}
}

func TestFacePayloadsRoundTripAndValidate(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	approval := ApprovalRequest{
		ApprovalID: "approval-1", ConversationID: "conv-1", RequestID: "req-1",
		RunID: "run-1", Tool: "exec_command", Reason: "sensitive command",
		ArgsDigest: "sha256:abc", ExpiresAt: now.Add(time.Minute),
	}
	snapshot := ConversationSnapshot{
		ConversationID: "conv-1", Name: "Work", Mode: "normal", ActiveHand: "hand-1",
		Messages: []FaceMessage{{
			ID: 1, Role: "user", Content: "test", RequestID: "req-1", Seq: 1, CreatedAt: now,
		}},
		PendingChats:     []ChatSummary{{RequestID: "req-2", StartedAt: now}},
		PendingApprovals: []ApprovalSummary{approval},
		ActiveRuns: []RemoteRunSummary{{
			RunID: "run-1", RequestID: "req-1", HandID: "hand-1", Tool: "exec_command",
			Status: RunRunning, DurationMs: 1000, CreatedAt: now,
		}},
		Tasks:           []TaskSummary{},
		SnapshotVersion: 4,
	}
	tests := []struct {
		typ     string
		payload any
	}{
		{TypeFaceChat, FaceChat{RequestID: "req-1", ConversationID: "conv-1", Content: "hello"}},
		{TypeFaceChatCancel, FaceChatCancel{RequestID: "req-2", TargetRequestID: "req-1", ConversationID: "conv-1", Reason: "user"}},
		{TypeFaceConversationList, FaceConversationList{RequestID: "req-3"}},
		{TypeFaceConversationCreate, FaceConversationCreate{RequestID: "req-4", Name: "Work"}},
		{TypeFaceConversationSnapshot, FaceConversationSnapshot{RequestID: "req-5", ConversationID: "conv-1"}},
		{TypeFaceConversationRename, FaceConversationRename{RequestID: "req-6", ConversationID: "conv-1", Name: "New name"}},
		{TypeFaceSubscribe, FaceSubscribe{RequestID: "req-7", ConversationIDs: []string{"conv-1"}, EventTypes: []FaceEventType{FaceEventChatStarted}}},
		{TypeFaceApprovalResolve, FaceApprovalResolve{RequestID: "req-8", ApprovalID: "approval-1", Decision: FaceApprovalAllowOnce}},
		{TypeFaceRunGet, FaceRunGet{RequestID: "req-9", ConversationID: "conv-1", RunID: "run-1"}},
		{TypeFaceRunCancel, FaceRunCancel{RequestID: "req-10", ConversationID: "conv-1", RunID: "run-1", Reason: "user"}},
		{TypeFaceHandList, FaceHandList{RequestID: "req-11"}},
		{TypeFaceHandGet, FaceHandGet{RequestID: "req-12", HandID: "hand-1"}},
		{TypeFaceAccepted, FaceAccepted{RequestID: "req-1", ConversationID: "conv-1", Operation: FaceOperationChat, SnapshotVersion: 4}},
		{TypeFaceResult, FaceResult{RequestID: "req-1", ConversationID: "conv-1", Status: FaceResultSucceeded, Content: "done", Data: json.RawMessage(`{"run_id":"run-1"}`)}},
		{TypeFaceResult, FaceResult{RequestID: "req-list", Status: FaceResultSucceeded, Data: json.RawMessage(`{"conversations":[]}`)}},
		{TypeFaceError, FaceError{RequestID: "req-1", ConversationID: "conv-1", Code: FaceErrorBusy, Message: "busy", Retryable: true}},
		{TypeFaceSnapshot, FaceSnapshot{RequestID: "req-5", Snapshot: snapshot}},
		{TypeFaceEvent, validFaceEvent(t, FaceEventRemoteRunChanged, RemoteRunChangedEventData{RunID: "run-1", HandID: "hand-1", Tool: "exec_command", Status: RunRunning, DurationMs: 25}, now)},
	}

	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			payload, err := json.Marshal(tt.payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateFacePayload(tt.typ, payload); err != nil {
				t.Fatalf("valid payload rejected: %v\npayload: %s", err, payload)
			}

			copyValue := reflect.New(reflect.TypeOf(tt.payload))
			if err := json.Unmarshal(payload, copyValue.Interface()); err != nil {
				t.Fatal(err)
			}
			roundTrip, err := json.Marshal(copyValue.Elem().Interface())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(payload, roundTrip) {
				t.Fatalf("round trip mismatch:\n got: %s\nwant: %s", roundTrip, payload)
			}
		})
	}
}

func TestFaceStructuredEventsValidate(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	events := []struct {
		typ  FaceEventType
		data any
	}{
		{FaceEventChatStarted, ChatStartedEventData{RequestID: "req-1"}},
		{FaceEventChatToolCalled, ChatToolCalledEventData{RequestID: "req-1", Tool: "read_file", ArgsDigest: "sha256:abc"}},
		{FaceEventChatToolCompleted, ChatToolCompletedEventData{RequestID: "req-1", Tool: "read_file", Success: true}},
		{FaceEventChatCompleted, ChatCompletedEventData{RequestID: "req-1"}},
		{FaceEventChatFailed, ChatFailedEventData{RequestID: "req-1", Code: FaceErrorInternal}},
		{FaceEventChatCancelled, ChatCancelledEventData{RequestID: "req-1", Reason: "user"}},
		{FaceEventApprovalRequested, ApprovalRequestedEventData{ApprovalID: "approval-1", ConversationID: "conv-1", RequestID: "req-1", Tool: "write_file", Reason: "write", ArgsDigest: "sha256:abc", ExpiresAt: now.Add(time.Minute)}},
		{FaceEventApprovalResolved, ApprovalResolvedEventData{ApprovalID: "approval-1", Decision: FaceApprovalDenyOnce, Actor: "face-1"}},
		{FaceEventRemoteRunChanged, RemoteRunChangedEventData{RunID: "run-1", HandID: "hand-1", Tool: "read_file", Status: RunSucceeded, DurationMs: 20}},
		{FaceEventHandConnected, HandConnectedEventData{HandID: "hand-1", Hostname: "dev", OS: "linux", Arch: "amd64"}},
		{FaceEventHandDisconnected, HandDisconnectedEventData{HandID: "hand-1"}},
		{FaceEventConversationChanged, ConversationChangedEventData{ConversationID: "conv-1", SnapshotVersion: 2}},
	}
	for _, tt := range events {
		t.Run(string(tt.typ), func(t *testing.T) {
			event := validFaceEvent(t, tt.typ, tt.data, now)
			payload, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateFacePayload(TypeFaceEvent, payload); err != nil {
				t.Fatalf("valid event rejected: %v", err)
			}
		})
	}
}

func TestValidateFacePayloadRejectsMalformedJSON(t *testing.T) {
	tests := []struct {
		name    string
		typ     string
		payload string
	}{
		{"unknown message type", "face.missing", `{}`},
		{"empty payload", TypeFaceChat, ``},
		{"unknown field", TypeFaceChat, `{"request_id":"req-1","conversation_id":"conv-1","content":"hi","extra":true}`},
		{"connection session in payload", TypeFaceChat, `{"request_id":"req-1","conversation_id":"conv-1","content":"hi","session_id":"wrong-layer"}`},
		{"trailing object", TypeFaceChat, `{"request_id":"req-1","conversation_id":"conv-1","content":"hi"} {}`},
		{"trailing scalar", TypeFaceChat, `{"request_id":"req-1","conversation_id":"conv-1","content":"hi"} true`},
		{"wrong field type", TypeFaceChat, `{"request_id":1,"conversation_id":"conv-1","content":"hi"}`},
		{"missing command request id", TypeFaceHandList, `{}`},
		{"missing conversation id", TypeFaceRunGet, `{"request_id":"req-1","run_id":"run-1"}`},
		{"empty subscription conversation", TypeFaceSubscribe, `{"request_id":"req-1","conversation_ids":[""]}`},
		{"unknown subscription event", TypeFaceSubscribe, `{"request_id":"req-1","event_types":["debug"]}`},
		{"unknown approval decision", TypeFaceApprovalResolve, `{"request_id":"req-1","approval_id":"approval-1","decision":"always"}`},
		{"unknown accepted operation", TypeFaceAccepted, `{"request_id":"req-1","operation":"other"}`},
		{"negative accepted version", TypeFaceAccepted, `{"request_id":"req-1","operation":"subscribe","snapshot_version":-1}`},
		{"unknown result status", TypeFaceResult, `{"request_id":"req-1","status":"running"}`},
		{"unknown result error code", TypeFaceResult, `{"request_id":"req-1","conversation_id":"conv-1","status":"failed","error_code":"other"}`},
		{"unknown error code", TypeFaceError, `{"code":"other","message":"bad","retryable":false}`},
		{"missing error message", TypeFaceError, `{"code":"invalid_request","retryable":false}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateFacePayload(tt.typ, json.RawMessage(tt.payload)); err == nil {
				t.Fatalf("malformed payload accepted: %s", tt.payload)
			}
		})
	}
}

func TestValidateFacePayloadRejectsMalformedSnapshot(t *testing.T) {
	valid := `{"request_id":"req-1","snapshot":{"conversation_id":"conv-1","name":"Work","mode":"normal","messages":[],"pending_chats":[],"pending_approvals":[],"active_runs":[],"tasks":[],"task_history_limit":0,"task_history_truncated":false,"snapshot_version":1}}`
	if err := ValidateFacePayload(TypeFaceSnapshot, json.RawMessage(valid)); err != nil {
		t.Fatalf("valid empty snapshot rejected: %v", err)
	}

	tests := []string{
		`{"request_id":"req-1","snapshot":{"name":"Work","mode":"normal","messages":[],"pending_chats":[],"pending_approvals":[],"active_runs":[],"snapshot_version":1}}`,
		`{"request_id":"req-1","snapshot":{"conversation_id":"conv-1","name":"Work","mode":"normal","pending_chats":[],"pending_approvals":[],"active_runs":[],"snapshot_version":1}}`,
		`{"request_id":"req-1","snapshot":{"conversation_id":"conv-1","name":"Work","mode":"normal","messages":[],"pending_chats":[],"pending_approvals":[],"active_runs":[],"snapshot_version":-1}}`,
		`{"request_id":"req-1","snapshot":{"conversation_id":"conv-1","name":"Work","mode":"normal","messages":[],"pending_chats":[],"pending_approvals":[],"active_runs":[{"run_id":"run-1","request_id":"req-1","hand_id":"hand-1","tool":"exec_command","status":"other","duration_ms":0,"created_at":"2026-07-17T12:00:00Z"}],"snapshot_version":1}}`,
	}
	for _, payload := range tests {
		if err := ValidateFacePayload(TypeFaceSnapshot, json.RawMessage(payload)); err == nil {
			t.Errorf("malformed snapshot accepted: %s", payload)
		}
	}
}

func TestValidateFacePayloadRejectsMalformedEventData(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		event FaceEvent
	}{
		{"nonpositive sequence", FaceEvent{Type: FaceEventChatStarted, Source: "mind", Level: FaceEventLevelInfo, Message: "started", Data: json.RawMessage(`{"request_id":"req-1"}`), Timestamp: now}},
		{"unknown event", FaceEvent{EventSeq: 1, Type: "debug", Source: "mind", Level: FaceEventLevelInfo, Message: "debug", Data: json.RawMessage(`{}`), Timestamp: now}},
		{"unknown level", FaceEvent{EventSeq: 1, Type: FaceEventChatStarted, Source: "mind", Level: "debug", Message: "started", Data: json.RawMessage(`{"request_id":"req-1"}`), Timestamp: now}},
		{"missing data", FaceEvent{EventSeq: 1, Type: FaceEventChatStarted, Source: "mind", Level: FaceEventLevelInfo, Message: "started", Timestamp: now}},
		{"event data missing field", FaceEvent{EventSeq: 1, Type: FaceEventChatToolCalled, Source: "mind", Level: FaceEventLevelInfo, Message: "called", Data: json.RawMessage(`{"request_id":"req-1","tool":"read_file"}`), Timestamp: now}},
		{"event data unknown field", FaceEvent{EventSeq: 1, Type: FaceEventHandDisconnected, Source: "mind", Level: FaceEventLevelInfo, Message: "left", Data: json.RawMessage(`{"hand_id":"hand-1","token":"secret"}`), Timestamp: now}},
		{"unknown nested error code", FaceEvent{EventSeq: 1, Type: FaceEventChatFailed, Source: "mind", Level: FaceEventLevelError, Message: "failed", Data: json.RawMessage(`{"request_id":"req-1","code":"other"}`), Timestamp: now}},
		{"unknown nested run status", FaceEvent{EventSeq: 1, Type: FaceEventRemoteRunChanged, Source: "mind", Level: FaceEventLevelInfo, Message: "changed", Data: json.RawMessage(`{"run_id":"run-1","hand_id":"hand-1","tool":"exec_command","status":"other","duration_ms":0}`), Timestamp: now}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := json.Marshal(tt.event)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateFacePayload(TypeFaceEvent, payload); err == nil {
				t.Fatalf("malformed event accepted: %s", payload)
			}
		})
	}
}

func TestFaceValidationIsStructuralOnly(t *testing.T) {
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	event := validFaceEvent(t, FaceEventApprovalRequested, ApprovalRequestedEventData{
		ApprovalID: "approval-1", ConversationID: "conv-1", RequestID: "req-1",
		Tool: "exec_command", Reason: "sensitive", ArgsDigest: "digest", ExpiresAt: past,
	}, past)
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFacePayload(TypeFaceEvent, payload); err != nil {
		t.Fatalf("structurally valid expired approval rejected: %v", err)
	}
}

func TestFaceDTOsDoNotDeclareSessionID(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(FaceIdentity{}), reflect.TypeOf(FaceCommandMeta{}), reflect.TypeOf(FaceChat{}), reflect.TypeOf(FaceChatCancel{}),
		reflect.TypeOf(FaceConversationList{}), reflect.TypeOf(FaceConversationCreate{}),
		reflect.TypeOf(FaceConversationSnapshot{}), reflect.TypeOf(FaceConversationRename{}),
		reflect.TypeOf(FaceSubscribe{}), reflect.TypeOf(FaceApprovalResolve{}),
		reflect.TypeOf(FaceRunGet{}), reflect.TypeOf(FaceRunCancel{}),
		reflect.TypeOf(FaceHandList{}), reflect.TypeOf(FaceHandGet{}),
		reflect.TypeOf(FaceAccepted{}), reflect.TypeOf(FaceResult{}), reflect.TypeOf(FaceError{}),
		reflect.TypeOf(FaceSnapshot{}), reflect.TypeOf(FaceEvent{}),
		reflect.TypeOf(ConversationSummary{}), reflect.TypeOf(FaceMessage{}),
		reflect.TypeOf(ChatSummary{}), reflect.TypeOf(ApprovalRequest{}),
		reflect.TypeOf(RemoteRunSummary{}), reflect.TypeOf(HandSummary{}),
		reflect.TypeOf(ConversationSnapshot{}), reflect.TypeOf(ConversationListResult{}),
		reflect.TypeOf(HandListResult{}), reflect.TypeOf(ChatStartedEventData{}),
		reflect.TypeOf(ChatToolCalledEventData{}), reflect.TypeOf(ChatToolCompletedEventData{}),
		reflect.TypeOf(ChatCompletedEventData{}), reflect.TypeOf(ChatFailedEventData{}),
		reflect.TypeOf(ChatCancelledEventData{}), reflect.TypeOf(ApprovalResolvedEventData{}),
		reflect.TypeOf(RemoteRunChangedEventData{}), reflect.TypeOf(HandConnectedEventData{}),
		reflect.TypeOf(HandDisconnectedEventData{}), reflect.TypeOf(ConversationChangedEventData{}),
	}
	seen := make(map[reflect.Type]bool)
	for _, typ := range types {
		assertNoSessionIDJSONTag(t, typ, seen)
	}
}

func faceScopeStrings(scopes []FaceScope) []string {
	values := make([]string, len(scopes))
	for i, scope := range scopes {
		values[i] = string(scope)
	}
	return values
}

func validFaceEvent(t *testing.T, typ FaceEventType, data any, timestamp time.Time) FaceEvent {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	event := FaceEvent{
		EventSeq: 1, ConversationID: "conv-1", RequestID: "req-1", Type: typ,
		Source: "mind", Level: FaceEventLevelInfo, Message: string(typ), Data: raw, Timestamp: timestamp,
	}
	if typ == FaceEventHandConnected || typ == FaceEventHandDisconnected {
		event.ConversationID = ""
		event.RequestID = ""
	}
	return event
}

func assertNoSessionIDJSONTag(t *testing.T, typ reflect.Type, seen map[reflect.Type]bool) {
	t.Helper()
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || typ.PkgPath() == "time" || seen[typ] {
		return
	}
	seen[typ] = true
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "session_id" {
			t.Errorf("%s.%s declares forbidden Face JSON field session_id", typ.Name(), field.Name)
		}
		assertNoSessionIDJSONTag(t, field.Type, seen)
	}
}
