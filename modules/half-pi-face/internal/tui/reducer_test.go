package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestFirstSendCreatesSubscribesSnapshotsAndChatsOnce(t *testing.T) {
	model, connection := readyModel(t)
	model.idSource = sequenceIDSource(t, "chat-request", "create-request", "subscribe-request", "snapshot-request")
	model.composer.SetValue("hello from the draft")
	cmd, err := model.submitComposer()
	if err != nil {
		t.Fatal(err)
	}
	runCommand(t, cmd)
	if len(connection.sent) != 1 || connection.sent[0].Type != protocol.TypeFaceConversationCreate {
		t.Fatalf("first send = %#v", connection.sent)
	}
	cmd = model.applyResult(protocol.FaceResult{
		RequestID: "create-request", Status: protocol.FaceResultSucceeded,
		Data: resultData(t, protocol.ConversationCreateResult{Conversation: validSummary("conversation-1")}),
	})
	runCommand(t, cmd)
	if len(connection.sent) != 2 || connection.sent[1].Type != protocol.TypeFaceSubscribe {
		t.Fatalf("after create = %#v", connection.sent)
	}
	cmd = model.applyAccepted(protocol.FaceAccepted{
		RequestID: "subscribe-request", Operation: protocol.FaceOperationSubscribe,
	})
	runCommand(t, cmd)
	if len(connection.sent) != 3 || connection.sent[2].Type != protocol.TypeFaceConversationSnapshot {
		t.Fatalf("after subscribe = %#v", connection.sent)
	}
	cmd = model.applySnapshotResponse(protocol.FaceSnapshot{RequestID: "snapshot-request", Snapshot: validSnapshot("conversation-1")})
	runCommand(t, cmd)
	if len(connection.sent) != 4 || connection.sent[3].Type != protocol.TypeFaceChat {
		t.Fatalf("after snapshot = %#v", connection.sent)
	}
	chat, err := protocol.DecodePayload[protocol.FaceChat](&connection.sent[3])
	if err != nil || chat.RequestID != "chat-request" || chat.Content != "hello from the draft" || chat.ConversationID != "conversation-1" {
		t.Fatalf("Chat payload = %+v, %v", chat, err)
	}
}

func TestSnapshotMessagesRenderInViewport(t *testing.T) {
	model, _ := readyModel(t)
	conversationID := "conversation-1"
	model.activeID = conversationID
	model.localDraft = nil
	model.conversations[conversationID] = newConversation(conversationID)
	model.pending["snapshot-request"] = pendingRequest{
		Operation: protocol.FaceOperationConversationSnapshot, ConversationID: conversationID,
	}
	snapshot := validSnapshot(conversationID)
	snapshot.Messages = []protocol.FaceMessage{
		{ID: 1, Role: "user", Content: "persisted prompt", Seq: 1},
		{ID: 2, Role: "assistant", Content: "persisted answer", Seq: 2},
		{ID: 3, Role: "tool", Content: strings.Repeat("tool output\n", 30), Seq: 3},
		{ID: 4, Role: "assistant", Content: "latest answer", Seq: 4},
	}
	model.applySnapshotResponse(protocol.FaceSnapshot{RequestID: "snapshot-request", Snapshot: snapshot})
	if view := ansi.Strip(model.View()); !strings.Contains(view, "latest answer") {
		t.Fatalf("snapshot messages missing: offset=%d total=%d height=%d viewport=%q\n%s",
			model.chatViewport.YOffset, model.chatViewport.TotalLineCount(), model.chatViewport.Height,
			ansi.Strip(model.chatViewport.View()), view)
	}
}

func TestDeltaGapRecoveryAndTerminalReconciliation(t *testing.T) {
	model, connection := readyModel(t)
	model.localDraft = nil
	model.activeID = "conversation-1"
	model.conversations[model.activeID] = newConversation(model.activeID)
	model.idSource = sequenceIDSource(t, "recover-request", "snapshot-refresh")
	model.applyDelta(protocol.FaceChatDelta{
		ConversationID: model.activeID, RequestID: "chat-request", ResponseIndex: 1, Seq: 1, Offset: 0, Delta: "hello",
	})
	cmd := model.applyDelta(protocol.FaceChatDelta{
		ConversationID: model.activeID, RequestID: "chat-request", ResponseIndex: 1, Seq: 3, Offset: 11, Delta: "!",
	})
	runCommand(t, cmd)
	chat := model.conversations[model.activeID].Chats["chat-request"]
	if !chat.Recovering || len(connection.sent) != 1 || connection.sent[0].Type != protocol.TypeFaceChatStreamGet {
		t.Fatalf("gap did not start recovery: %+v", chat)
	}
	model.applyResult(protocol.FaceResult{
		RequestID: "recover-request", ConversationID: model.activeID, Status: protocol.FaceResultSucceeded,
		Data: resultData(t, protocol.ChatStreamGetResult{
			TargetRequestID: "chat-request", LastSeq: 2,
			Responses: []protocol.ChatStreamResponse{{ResponseIndex: 1, Content: "hello world"}},
		}),
	})
	if chat.LastSeq != 3 || chat.Responses[1].Content != "hello world!" || chat.Recovering {
		t.Fatalf("recovered Chat = %+v", chat)
	}
	model.pending["chat-request"] = pendingRequest{Operation: protocol.FaceOperationChat, ConversationID: model.activeID}
	model.outgoingChats["chat-request"] = &sendFlow{ChatRequestID: "chat-request", ConversationID: model.activeID, Text: "question"}
	model.applyResult(protocol.FaceResult{
		RequestID: "chat-request", ConversationID: model.activeID, Status: protocol.FaceResultSucceeded, Content: "final answer",
	})
	if !chat.Terminal || chat.Responses[1].Content != "final answer" {
		t.Fatalf("terminal Chat = %+v", chat)
	}
}

func TestDeltaOffsetUsesRawBytesWhileRenderingSafeText(t *testing.T) {
	model, _ := readyModel(t)
	model.localDraft = nil
	model.activeID = "conversation-1"
	model.conversations[model.activeID] = newConversation(model.activeID)
	first := "safe\x1b[31m"
	if cmd := model.applyDelta(protocol.FaceChatDelta{
		ConversationID: model.activeID, RequestID: "chat-request", ResponseIndex: 1,
		Seq: 1, Offset: 0, Delta: first,
	}); cmd != nil {
		t.Fatal("first delta unexpectedly started recovery")
	}
	if cmd := model.applyDelta(protocol.FaceChatDelta{
		ConversationID: model.activeID, RequestID: "chat-request", ResponseIndex: 1,
		Seq: 2, Offset: int64(len(first)), Delta: "text",
	}); cmd != nil {
		t.Fatal("raw byte offset was treated as a gap")
	}
	response := model.conversations[model.activeID].Chats["chat-request"].Responses[1]
	if response.Content != "safetext" || response.NextOffset != int64(len(first)+4) {
		t.Fatalf("safe response = %+v", response)
	}
}

func TestStaleConnectionGenerationIsIgnored(t *testing.T) {
	model, _ := readyModel(t)
	model.generation = 4
	before := model.status
	envelope, err := protocol.NewEnvelope("error", protocol.TypeFaceError, protocol.FaceError{
		Code: protocol.FaceErrorInternal, Message: "stale", Retryable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := model.Update(envelopeMsg{generation: 3, env: *envelope})
	if updated.(*Model).status != before {
		t.Fatalf("stale generation changed status to %q", updated.(*Model).status)
	}
}

func TestScopeHidesUnauthorizedActivity(t *testing.T) {
	model, _ := readyModel(t)
	model.scopes = map[protocol.FaceScope]struct{}{protocol.FaceScopeChat: {}}
	model.localDraft.Approvals["approval"] = protocol.ApprovalSummary{ApprovalID: "approval"}
	model.localDraft.Runs["run"] = protocol.RemoteRunSummary{RunID: "run"}
	if tabs := model.availableActivityTabs(); len(tabs) != 0 {
		t.Fatalf("unauthorized tabs = %#v", tabs)
	}
	if model.modal != nil {
		t.Fatal("unauthorized approval modal is visible")
	}
}
