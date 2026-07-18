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
	commands := []string{
		TypeFaceChat, TypeFaceChatCancel, TypeFaceChatStreamGet, TypeFaceCapabilitiesGet, TypeFaceConversationList,
		TypeFaceConversationCreate, TypeFaceConversationSnapshot,
		TypeFaceConversationRename, TypeFaceConversationMessages, TypeFaceSubscribe, TypeFaceApprovalResolve,
		TypeFaceRunGet, TypeFaceRunCancel, TypeFaceHandList, TypeFaceHandGet,
		TypeFaceTaskList, TypeFaceTaskGet, TypeFaceTaskLog, TypeFaceTaskCancel,
	}
	serverMessages := []string{
		TypeFaceAccepted, TypeFaceResult, TypeFaceError, TypeFaceSnapshot, TypeFaceEvent,
		TypeFaceChatDelta, TypeFaceChatStreamEnd, TypeFaceRunProgress,
	}
	for _, typ := range commands {
		if !IsFaceCommandType(typ) || IsFaceServerMessageType(typ) || !IsFaceMessageType(typ) {
			t.Errorf("command classification for %q is invalid", typ)
		}
	}
	for _, typ := range serverMessages {
		if IsFaceCommandType(typ) || !IsFaceServerMessageType(typ) || !IsFaceMessageType(typ) {
			t.Errorf("server message classification for %q is invalid", typ)
		}
	}
	for _, typ := range []string{"", TypeRPC, "face.unknown", "chat.started"} {
		if IsFaceCommandType(typ) || IsFaceServerMessageType(typ) || IsFaceMessageType(typ) {
			t.Errorf("unknown type %q was classified as Face protocol", typ)
		}
	}
}

func TestFaceProtocolEnumValues(t *testing.T) {
	scopes := []FaceScope{
		FaceScopeChat, FaceScopeSessionsRead, FaceScopeSessionsWrite, FaceScopeRunsRead,
		FaceScopeRunsCancel, FaceScopeRunsOutput, FaceScopeApprove, FaceScopeHandsRead,
		FaceScopeTasksRead, FaceScopeTasksCancel,
	}
	if got, want := strings.Join(faceScopeStrings(scopes), ","), "face:chat,face:sessions:read,face:sessions:write,face:runs:read,face:runs:cancel,face:runs:output,face:approve,face:hands:read,face:tasks:read,face:tasks:cancel"; got != want {
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
		{TypeFaceChatStreamGet, FaceChatStreamGet{RequestID: "req-stream", ConversationID: "conv-1", TargetRequestID: "req-1"}},
		{TypeFaceCapabilitiesGet, FaceCapabilitiesGet{RequestID: "req-capabilities"}},
		{TypeFaceConversationList, FaceConversationList{RequestID: "req-3"}},
		{TypeFaceConversationCreate, FaceConversationCreate{RequestID: "req-4", Name: "Work"}},
		{TypeFaceConversationSnapshot, FaceConversationSnapshot{RequestID: "req-5", ConversationID: "conv-1"}},
		{TypeFaceConversationRename, FaceConversationRename{RequestID: "req-6", ConversationID: "conv-1", Name: "New name"}},
		{TypeFaceConversationMessages, FaceConversationMessages{RequestID: "req-messages", ConversationID: "conv-1", BeforeSeq: 8, Limit: 4}},
		{TypeFaceSubscribe, FaceSubscribe{RequestID: "req-7", ConversationIDs: []string{"conv-1"}, EventTypes: []FaceEventType{FaceEventChatStarted}, TransientTypes: []FaceTransientType{FaceTransientChatDelta, FaceTransientRunProgress}}},
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
		{TypeFaceChatDelta, FaceChatDelta{ConversationID: "conv-1", RequestID: "req-1", ResponseIndex: 1, Seq: 1, Offset: 0, Delta: "hello"}},
		{TypeFaceChatStreamEnd, FaceChatStreamEnd{ConversationID: "conv-1", RequestID: "req-1", LastSeq: 1, ResponseCount: 1, Complete: true, Status: FaceResultSucceeded}},
		{TypeFaceRunProgress, FaceRunProgress{ConversationID: "conv-1", RequestID: "req-1", RunID: "run-1", Seq: 1, Kind: ProgressStdout, Data: "output", Gap: false}},
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
		{"duplicate transient subscription", TypeFaceSubscribe, `{"request_id":"req-1","transient_types":["chat.delta","chat.delta"]}`},
		{"unknown transient subscription", TypeFaceSubscribe, `{"request_id":"req-1","transient_types":["other"]}`},
		{"negative messages cursor", TypeFaceConversationMessages, `{"request_id":"req-1","conversation_id":"conv-1","before_seq":-1}`},
		{"oversized messages page", TypeFaceConversationMessages, `{"request_id":"req-1","conversation_id":"conv-1","limit":501}`},
		{"empty delta", TypeFaceChatDelta, `{"conversation_id":"conv-1","request_id":"req-1","response_index":1,"seq":1,"offset":0,"delta":""}`},
		{"oversized delta", TypeFaceChatDelta, `{"conversation_id":"conv-1","request_id":"req-1","response_index":1,"seq":1,"offset":0,"delta":"` + strings.Repeat("x", MaxFaceChatDeltaBytes+1) + `"}`},
		{"delta outside stream", TypeFaceChatDelta, `{"conversation_id":"conv-1","request_id":"req-1","response_index":1,"seq":1,"offset":2097151,"delta":"xx"}`},
		{"invalid stream end", TypeFaceChatStreamEnd, `{"conversation_id":"conv-1","request_id":"req-1","last_seq":0,"response_count":1,"complete":true,"status":"failed"}`},
		{"invalid run progress", TypeFaceRunProgress, `{"conversation_id":"conv-1","run_id":"run-1","seq":0,"kind":"stdout","data":"x","gap":false}`},
		{"unknown approval decision", TypeFaceApprovalResolve, `{"request_id":"req-1","approval_id":"approval-1","decision":"always"}`},
		{"oversized approval reason", TypeFaceApprovalResolve, `{"request_id":"req-1","approval_id":"approval-1","decision":"allow_once","reason":"` + strings.Repeat("x", MaxFaceApprovalReasonBytes+1) + `"}`},
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

func TestValidateFaceResultDataForGatewayQueries(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	conversation := ConversationSummary{
		ConversationID: "conv-1", Name: "Work", Mode: "normal", MessageCount: 2, UpdatedAt: now,
	}
	hand := HandSummary{
		HandID: "hand-1", Hostname: "dev", OS: "linux", Arch: "amd64", Connected: true,
		Tools: []ToolInfo{},
	}
	run := RemoteRunSummary{
		RunID: "run-1", RequestID: "run-1", HandID: "hand-1", Tool: "read_file",
		Status: RunRunning, CreatedAt: now,
	}
	tests := []struct {
		operation FaceOperation
		data      any
	}{
		{FaceOperationCapabilitiesGet, FaceCapabilitiesResult{
			Revision: FaceProtocolRevision,
			Identity: FaceIdentity{ID: "face-1", Label: "terminal", Scopes: []FaceScope{FaceScopeChat, FaceScopeSessionsRead}},
			Features: []FaceFeature{FaceFeatureChatStream, FaceFeatureChatStreamResume, FaceFeatureRunProgress, FaceFeatureMessagePaging},
			Limits: FaceProtocolLimits{
				MaxChatContentBytes: MaxFaceChatContentBytes, MaxChatDeltaBytes: MaxFaceChatDeltaBytes,
				MaxChatStreamBytes: MaxFaceChatStreamBytes, MaxChatStreamChunks: MaxFaceChatStreamChunks,
				MaxMessageListLimit: MaxFaceMessageListLimit,
			},
		}},
		{FaceOperationChatStreamGet, ChatStreamGetResult{
			TargetRequestID: "req-chat", LastSeq: 2,
			Responses: []ChatStreamResponse{{ResponseIndex: 1, Content: "working", Complete: true}, {ResponseIndex: 2, Content: "done", Complete: true}},
			Terminal:  true, Status: FaceResultSucceeded,
		}},
		{FaceOperationConversationList, ConversationListResult{Conversations: []ConversationSummary{conversation}}},
		{FaceOperationConversationCreate, ConversationCreateResult{Conversation: conversation}},
		{FaceOperationConversationRename, ConversationRenameResult{Conversation: conversation}},
		{FaceOperationConversationMessages, ConversationMessagesResult{
			Messages:      []FaceMessage{{ID: 1, Role: "user", Content: "hello", RequestID: "req-chat", Seq: 1, CreatedAt: now}},
			NextBeforeSeq: 1, HasMore: true,
		}},
		{FaceOperationHandList, HandListResult{Hands: []HandSummary{hand}}},
		{FaceOperationHandGet, HandGetResult{Hand: hand}},
		{FaceOperationRunGet, RunGetResult{Run: run}},
	}
	for _, tt := range tests {
		t.Run(string(tt.operation), func(t *testing.T) {
			raw, err := json.Marshal(tt.data)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateFaceResultData(tt.operation, raw); err != nil {
				t.Fatalf("valid result rejected: %v", err)
			}
		})
	}

	if err := ValidateFaceResultData(FaceOperationConversationList, json.RawMessage(`{"conversations":null}`)); err == nil {
		t.Fatal("nil conversation collection accepted")
	}

	invalid := []struct {
		operation FaceOperation
		data      string
	}{
		{FaceOperationCapabilitiesGet, `{"revision":2,"identity":{"id":"face-1","label":"terminal","scopes":["face:chat"]},"features":["chat_stream.v1","chat_stream.v1"],"limits":{"max_chat_content_bytes":262144,"max_chat_delta_bytes":4096,"max_chat_stream_bytes":2097152,"max_chat_stream_chunks":2048,"max_message_list_limit":500}}`},
		{FaceOperationChatStreamGet, `{"target_request_id":"req-1","last_seq":1,"responses":[{"response_index":2,"content":"bad","complete":false}],"terminal":false}`},
		{FaceOperationConversationMessages, `{"messages":[{"id":1,"role":"user","content":"a","seq":2,"created_at":"2026-07-18T12:00:00Z"},{"id":2,"role":"assistant","content":"b","seq":1,"created_at":"2026-07-18T12:00:01Z"}],"has_more":false}`},
	}
	for _, tt := range invalid {
		if err := ValidateFaceResultData(tt.operation, json.RawMessage(tt.data)); err == nil {
			t.Errorf("malformed %s result accepted: %s", tt.operation, tt.data)
		}
	}
}

func TestFaceDTOsDoNotDeclareSessionID(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(FaceIdentity{}), reflect.TypeOf(FaceCommandMeta{}), reflect.TypeOf(FaceChat{}), reflect.TypeOf(FaceChatCancel{}),
		reflect.TypeOf(FaceChatStreamGet{}), reflect.TypeOf(FaceCapabilitiesGet{}),
		reflect.TypeOf(FaceConversationList{}), reflect.TypeOf(FaceConversationCreate{}),
		reflect.TypeOf(FaceConversationSnapshot{}), reflect.TypeOf(FaceConversationRename{}),
		reflect.TypeOf(FaceConversationMessages{}),
		reflect.TypeOf(FaceSubscribe{}), reflect.TypeOf(FaceApprovalResolve{}),
		reflect.TypeOf(FaceRunGet{}), reflect.TypeOf(FaceRunCancel{}),
		reflect.TypeOf(FaceHandList{}), reflect.TypeOf(FaceHandGet{}),
		reflect.TypeOf(FaceAccepted{}), reflect.TypeOf(FaceResult{}), reflect.TypeOf(FaceError{}),
		reflect.TypeOf(FaceSnapshot{}), reflect.TypeOf(FaceEvent{}),
		reflect.TypeOf(FaceChatDelta{}), reflect.TypeOf(FaceChatStreamEnd{}), reflect.TypeOf(FaceRunProgress{}),
		reflect.TypeOf(ConversationSummary{}), reflect.TypeOf(FaceMessage{}),
		reflect.TypeOf(ChatSummary{}), reflect.TypeOf(ApprovalRequest{}),
		reflect.TypeOf(RemoteRunSummary{}), reflect.TypeOf(HandSummary{}),
		reflect.TypeOf(ConversationSnapshot{}), reflect.TypeOf(ConversationListResult{}),
		reflect.TypeOf(ConversationCreateResult{}), reflect.TypeOf(ConversationRenameResult{}),
		reflect.TypeOf(FaceCapabilitiesResult{}), reflect.TypeOf(ChatStreamGetResult{}),
		reflect.TypeOf(ConversationMessagesResult{}),
		reflect.TypeOf(HandListResult{}), reflect.TypeOf(HandGetResult{}), reflect.TypeOf(RunGetResult{}),
		reflect.TypeOf(ChatStartedEventData{}),
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
