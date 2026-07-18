package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestSnapshotSwitchesActiveConversationAndSubscribes(t *testing.T) {
	term, conn, output := newTestTerminal(t)
	term.active = "conversation-a"
	if err := term.openConversation("conversation-b"); err != nil {
		t.Fatal(err)
	}
	request := nextSent(t, conn)
	payload, err := protocol.DecodePayload[protocol.FaceConversationSnapshot](&request)
	if err != nil {
		t.Fatal(err)
	}
	accepted := testEnvelope(t, protocol.TypeFaceAccepted, protocol.FaceAccepted{
		RequestID: payload.RequestID, ConversationID: "conversation-b",
		Operation: protocol.FaceOperationConversationSnapshot, SnapshotVersion: 1,
	})
	if err := term.handleEnvelope(accepted); err != nil {
		t.Fatal(err)
	}
	snapshot := testEnvelope(t, protocol.TypeFaceSnapshot, protocol.FaceSnapshot{
		RequestID: payload.RequestID, Snapshot: validSnapshot("conversation-b"),
	})
	if err := term.handleEnvelope(snapshot); err != nil {
		t.Fatal(err)
	}
	if term.active != "conversation-b" {
		t.Fatalf("active conversation = %q", term.active)
	}
	subscribe := nextSent(t, conn)
	subscription, err := protocol.DecodePayload[protocol.FaceSubscribe](&subscribe)
	if err != nil {
		t.Fatal(err)
	}
	if len(subscription.ConversationIDs) != 1 || subscription.ConversationIDs[0] != "conversation-b" {
		t.Fatalf("subscription = %+v", subscription)
	}
	if !strings.Contains(output.String(), "conversation conversation-b") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestCreateResultOpensCreatedConversation(t *testing.T) {
	term, conn, _ := newTestTerminal(t)
	if err := term.createConversation("Project"); err != nil {
		t.Fatal(err)
	}
	command := nextSent(t, conn)
	request, _ := protocol.DecodePayload[protocol.FaceConversationCreate](&command)
	summary := protocol.ConversationSummary{
		ConversationID: "conversation-new", Name: "Project", Mode: "normal", UpdatedAt: time.Now().UTC(),
	}
	data, _ := json.Marshal(protocol.ConversationCreateResult{Conversation: summary})
	result := testEnvelope(t, protocol.TypeFaceResult, protocol.FaceResult{
		RequestID: request.RequestID, Status: protocol.FaceResultSucceeded, Data: data,
	})
	if err := term.handleEnvelope(result); err != nil {
		t.Fatal(err)
	}
	snapshotCommand := nextSent(t, conn)
	payload, err := protocol.DecodePayload[protocol.FaceConversationSnapshot](&snapshotCommand)
	if err != nil || payload.ConversationID != "conversation-new" {
		t.Fatalf("snapshot command = %+v, %v", payload, err)
	}
}

func TestRenderTypedResults(t *testing.T) {
	term, _, output := newTestTerminal(t)
	now := time.Now().UTC()
	conversation := protocol.ConversationSummary{
		ConversationID: "conversation-1", Name: "Project", Mode: "normal", UpdatedAt: now,
	}
	hand := protocol.HandSummary{
		HandID: "hand-1", Hostname: "host", OS: "linux", Arch: "amd64", Connected: true,
		Tools: []protocol.ToolInfo{{Name: "exec_command", Description: "execute"}},
	}
	run := protocol.RemoteRunSummary{
		RunID: "run-1", RequestID: "request-chat", HandID: "hand-1", Tool: "exec_command",
		Status: protocol.RunRunning, CreatedAt: now,
	}
	task := protocol.TaskSummary{
		TaskID: "task-1", ConversationID: "conversation-1", HandID: "hand-1", Tool: "exec_command",
		ArgsDigest: "digest", Status: protocol.TaskRunning, CreatedAt: now, UpdatedAt: now,
	}
	tests := []struct {
		operation protocol.FaceOperation
		content   string
		data      any
		want      string
	}{
		{protocol.FaceOperationChat, "answer", nil, "assistant: answer"},
		{protocol.FaceOperationChatCancel, "Chat cancellation requested", nil, "Chat cancellation requested"},
		{protocol.FaceOperationConversationList, "", protocol.ConversationListResult{Conversations: []protocol.ConversationSummary{conversation}}, "conversation-1"},
		{protocol.FaceOperationConversationRename, "", protocol.ConversationRenameResult{Conversation: conversation}, "renamed conversation"},
		{protocol.FaceOperationHandList, "", protocol.HandListResult{Hands: []protocol.HandSummary{hand}}, "tool exec_command"},
		{protocol.FaceOperationHandGet, "", protocol.HandGetResult{Hand: hand}, "hand hand-1"},
		{protocol.FaceOperationRunGet, "", protocol.RunGetResult{Run: run}, "run run-1"},
		{protocol.FaceOperationRunCancel, "Run cancellation requested", nil, "Run cancellation requested"},
		{protocol.FaceOperationTaskList, "", protocol.TaskListResult{Tasks: []protocol.TaskSummary{task}}, "task task-1"},
		{protocol.FaceOperationTaskGet, "", protocol.TaskGetResult{Task: task}, "task task-1"},
		{protocol.FaceOperationTaskLog, "", protocol.TaskLogResult{TaskID: "task-1", Data: []byte("output"), NextOffset: 6}, "output"},
		{protocol.FaceOperationTaskCancel, "", protocol.FaceTaskCancelResult{Outcome: string(protocol.TaskCancelAlreadyDone), Task: task}, "already_done"},
	}
	for i, test := range tests {
		var data json.RawMessage
		if test.data != nil {
			var err error
			data, err = json.Marshal(test.data)
			if err != nil {
				t.Fatal(err)
			}
		}
		before := output.Len()
		result := protocol.FaceResult{
			RequestID: "request-result", Status: protocol.FaceResultSucceeded,
			Content: test.content, Data: data,
		}
		if err := term.renderResult(test.operation, result); err != nil {
			t.Fatalf("render result %d (%s): %v", i, test.operation, err)
		}
		if got := output.String()[before:]; !strings.Contains(got, test.want) {
			t.Fatalf("render result %s = %q, want %q", test.operation, got, test.want)
		}
	}
}

func TestHandleEnvelopeRejectsUncorrelatedAndInvalidNestedResults(t *testing.T) {
	term, conn, _ := newTestTerminal(t)
	if err := term.listConversations(); err != nil {
		t.Fatal(err)
	}
	command := nextSent(t, conn)
	request, _ := protocol.DecodePayload[protocol.FaceConversationList](&command)
	uncorrelated := testEnvelope(t, protocol.TypeFaceAccepted, protocol.FaceAccepted{
		RequestID: request.RequestID, Operation: protocol.FaceOperationHandList,
	})
	if err := term.handleEnvelope(uncorrelated); err == nil {
		t.Fatal("accepted mismatched operation")
	}
	invalid := testEnvelope(t, protocol.TypeFaceResult, protocol.FaceResult{
		RequestID: request.RequestID, Status: protocol.FaceResultSucceeded,
		Data: json.RawMessage(`{"conversations":null}`),
	})
	if err := term.handleEnvelope(invalid); err == nil {
		t.Fatal("result accepted invalid operation-specific data")
	}
}

func TestRenderTypedEvents(t *testing.T) {
	term, _, output := newTestTerminal(t)
	term.active = "conversation-1"
	now := time.Now().UTC()
	task := protocol.TaskSummary{
		TaskID: "task-1", ConversationID: "conversation-1", HandID: "hand-1", Tool: "exec_command",
		ArgsDigest: "digest", Status: protocol.TaskRunning, CreatedAt: now, UpdatedAt: now,
	}
	approval := protocol.ApprovalRequest{
		ApprovalID: "approval-1", ConversationID: "conversation-1", RequestID: "request-1",
		Tool: "exec_command", Reason: "sensitive", ArgsDigest: "digest", ExpiresAt: now.Add(time.Minute),
	}
	tests := []struct {
		typ       protocol.FaceEventType
		requestID string
		data      any
		want      string
		global    bool
	}{
		{protocol.FaceEventChatStarted, "request-1", protocol.ChatStartedEventData{RequestID: "request-1"}, "chat request-1 started", false},
		{protocol.FaceEventChatToolCalled, "request-1", protocol.ChatToolCalledEventData{RequestID: "request-1", Tool: "list_hands", ArgsDigest: "digest"}, "called list_hands", false},
		{protocol.FaceEventChatToolCompleted, "request-1", protocol.ChatToolCompletedEventData{RequestID: "request-1", Tool: "list_hands", Success: true}, "success=true", false},
		{protocol.FaceEventChatCompleted, "request-1", protocol.ChatCompletedEventData{RequestID: "request-1"}, "chat request-1 completed", false},
		{protocol.FaceEventChatFailed, "request-1", protocol.ChatFailedEventData{RequestID: "request-1", Code: protocol.FaceErrorInternal}, "code=internal_error", false},
		{protocol.FaceEventChatCancelled, "request-1", protocol.ChatCancelledEventData{RequestID: "request-1", Reason: "user"}, "cancelled", false},
		{protocol.FaceEventApprovalRequested, "request-1", approval, "approval approval-1", false},
		{protocol.FaceEventApprovalResolved, "request-1", protocol.ApprovalResolvedEventData{ApprovalID: "approval-1", Decision: protocol.FaceApprovalAllowOnce, Actor: "face-1"}, "resolved allow_once", false},
		{protocol.FaceEventRemoteRunChanged, "request-1", protocol.RemoteRunChangedEventData{RunID: "run-1", HandID: "hand-1", Tool: "exec_command", Status: protocol.RunRunning}, "run run-1", false},
		{protocol.FaceEventHandConnected, "", protocol.HandConnectedEventData{HandID: "hand-1", Hostname: "host", OS: "linux", Arch: "amd64"}, "hand hand-1 connected", true},
		{protocol.FaceEventHandDisconnected, "", protocol.HandDisconnectedEventData{HandID: "hand-1"}, "hand hand-1 disconnected", true},
		{protocol.FaceEventConversationChanged, "", protocol.ConversationChangedEventData{ConversationID: "conversation-1", SnapshotVersion: 2}, "version=2", false},
		{protocol.FaceEventTaskChanged, "", task, "task task-1", false},
	}
	for i, test := range tests {
		data, err := json.Marshal(test.data)
		if err != nil {
			t.Fatal(err)
		}
		conversationID := "conversation-1"
		if test.global {
			conversationID = ""
		}
		event := protocol.FaceEvent{
			EventSeq: int64(i + 1), ConversationID: conversationID, RequestID: test.requestID,
			Type: test.typ, Source: "test", Level: protocol.FaceEventLevelInfo,
			Message: "event", Data: data, Timestamp: now,
		}
		before := output.Len()
		if err := term.handleEnvelope(testEnvelope(t, protocol.TypeFaceEvent, event)); err != nil {
			t.Fatalf("handle %s: %v", test.typ, err)
		}
		if got := output.String()[before:]; !strings.Contains(got, test.want) {
			t.Fatalf("render %s = %q, want %q", test.typ, got, test.want)
		}
	}
}

func TestSafeTextEscapesTerminalControlCharacters(t *testing.T) {
	input := "before\x1b[31m\n\r\t\u009bafter"
	got := safeText(input)
	for _, control := range []string{"\x1b", "\n", "\r", "\t", "\u009b"} {
		if strings.Contains(got, control) {
			t.Fatalf("safeText retained control %q in %q", control, got)
		}
	}
	for _, escaped := range []string{`\u001b`, `\n`, `\r`, `\t`, `\u009b`} {
		if !strings.Contains(got, escaped) {
			t.Fatalf("safeText(%q) = %q, missing %q", input, got, escaped)
		}
	}
}

func validSnapshot(conversationID string) protocol.ConversationSnapshot {
	return protocol.ConversationSnapshot{
		ConversationID: conversationID, Name: "Project", Mode: "normal",
		Messages: []protocol.FaceMessage{}, PendingChats: []protocol.ChatSummary{},
		PendingApprovals: []protocol.ApprovalSummary{}, ActiveRuns: []protocol.RemoteRunSummary{},
		Tasks: []protocol.TaskSummary{}, TaskHistoryLimit: protocol.DefaultFaceTaskHistoryLimit,
		SnapshotVersion: 1,
	}
}
