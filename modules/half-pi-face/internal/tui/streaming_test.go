package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestCapabilityResultEnablesScopedTransientSubscription(t *testing.T) {
	term, conn, _ := newTestTerminal(t)
	term.active = "conversation-1"
	if err := term.getCapabilities(); err != nil {
		t.Fatal(err)
	}
	command := nextSent(t, conn)
	request, _ := protocol.DecodePayload[protocol.FaceCapabilitiesGet](&command)
	data, _ := json.Marshal(protocol.FaceCapabilitiesResult{
		Revision: protocol.FaceProtocolRevision,
		Identity: protocol.FaceIdentity{ID: "face-1", Label: "terminal", Scopes: []protocol.FaceScope{
			protocol.FaceScopeSessionsRead, protocol.FaceScopeRunsOutput,
		}},
		Features: []protocol.FaceFeature{protocol.FaceFeatureChatStream, protocol.FaceFeatureRunProgress},
		Limits: protocol.FaceProtocolLimits{
			MaxChatContentBytes: protocol.MaxFaceChatContentBytes, MaxChatDeltaBytes: protocol.MaxFaceChatDeltaBytes,
			MaxChatStreamBytes: protocol.MaxFaceChatStreamBytes, MaxChatStreamChunks: protocol.MaxFaceChatStreamChunks,
			MaxMessageListLimit: protocol.MaxFaceMessageListLimit,
		},
	})
	if err := term.handleEnvelope(testEnvelope(t, protocol.TypeFaceResult, protocol.FaceResult{
		RequestID: request.RequestID, Status: protocol.FaceResultSucceeded, Data: data,
	})); err != nil {
		t.Fatal(err)
	}
	subscribe := nextSent(t, conn)
	payload, err := protocol.DecodePayload[protocol.FaceSubscribe](&subscribe)
	if err != nil || len(payload.TransientTypes) != 2 || payload.TransientTypes[0] != protocol.FaceTransientChatDelta ||
		payload.TransientTypes[1] != protocol.FaceTransientRunProgress {
		t.Fatalf("subscription = %#v, %v", payload, err)
	}
}

func TestChatDeltaGapRequestsRecoveryAndFinalResultReconciles(t *testing.T) {
	term, conn, output := newTestTerminal(t)
	term.active = "conversation-1"
	term.features = map[protocol.FaceFeature]struct{}{protocol.FaceFeatureChatStreamResume: {}}
	first := protocol.FaceChatDelta{
		ConversationID: "conversation-1", RequestID: "chat-1", ResponseIndex: 1, Seq: 1, Offset: 0, Delta: "hello",
	}
	if err := term.handleEnvelope(testEnvelope(t, protocol.TypeFaceChatDelta, first)); err != nil {
		t.Fatal(err)
	}
	gapped := protocol.FaceChatDelta{
		ConversationID: "conversation-1", RequestID: "chat-1", ResponseIndex: 1, Seq: 3, Offset: 11, Delta: "!",
	}
	if err := term.handleEnvelope(testEnvelope(t, protocol.TypeFaceChatDelta, gapped)); err != nil {
		t.Fatal(err)
	}
	recoverCommand := nextSent(t, conn)
	recoverRequest, err := protocol.DecodePayload[protocol.FaceChatStreamGet](&recoverCommand)
	if err != nil || recoverRequest.TargetRequestID != "chat-1" {
		t.Fatalf("recovery command = %#v, %v", recoverRequest, err)
	}
	recoveryData, _ := json.Marshal(protocol.ChatStreamGetResult{
		TargetRequestID: "chat-1", LastSeq: 2,
		Responses: []protocol.ChatStreamResponse{{ResponseIndex: 1, Content: "hello world", Complete: false}},
	})
	if err := term.handleEnvelope(testEnvelope(t, protocol.TypeFaceResult, protocol.FaceResult{
		RequestID: recoverRequest.RequestID, ConversationID: "conversation-1",
		Status: protocol.FaceResultSucceeded, Data: recoveryData,
	})); err != nil {
		t.Fatal(err)
	}
	stream := term.stream("chat-1")
	if stream.lastSeq != 3 || stream.responses[1] != "hello world!" || stream.recovering {
		t.Fatalf("recovered stream = %#v", stream)
	}
	term.pending["chat-1"] = pendingRequest{operation: protocol.FaceOperationChat, conversationID: "conversation-1"}
	if err := term.handleEnvelope(testEnvelope(t, protocol.TypeFaceResult, protocol.FaceResult{
		RequestID: "chat-1", ConversationID: "conversation-1", Status: protocol.FaceResultSucceeded, Content: "hello world!",
	})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "recovered: hello world") || !strings.Contains(output.String(), "assistant complete") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestSnapshotSubscriptionRecoversPendingChat(t *testing.T) {
	term, conn, _ := newTestTerminal(t)
	term.features = map[protocol.FaceFeature]struct{}{
		protocol.FaceFeatureChatStream:       {},
		protocol.FaceFeatureChatStreamResume: {},
	}
	term.scopes = map[protocol.FaceScope]struct{}{protocol.FaceScopeSessionsRead: {}}
	if err := term.openConversation("conversation-1"); err != nil {
		t.Fatal(err)
	}
	snapshotCommand := nextSent(t, conn)
	snapshotRequest, _ := protocol.DecodePayload[protocol.FaceConversationSnapshot](&snapshotCommand)
	if err := term.handleEnvelope(testEnvelope(t, protocol.TypeFaceSnapshot, protocol.FaceSnapshot{
		RequestID: snapshotRequest.RequestID,
		Snapshot: protocol.ConversationSnapshot{
			ConversationID: "conversation-1", Name: "Conversation", Mode: "normal",
			Messages: []protocol.FaceMessage{}, PendingChats: []protocol.ChatSummary{{RequestID: "pending-chat", StartedAt: time.Now()}},
			PendingApprovals: []protocol.ApprovalSummary{}, ActiveRuns: []protocol.RemoteRunSummary{}, Tasks: []protocol.TaskSummary{},
			SnapshotVersion: 1,
		},
	})); err != nil {
		t.Fatal(err)
	}
	subscribeCommand := nextSent(t, conn)
	subscribeRequest, _ := protocol.DecodePayload[protocol.FaceSubscribe](&subscribeCommand)
	if err := term.handleEnvelope(testEnvelope(t, protocol.TypeFaceAccepted, protocol.FaceAccepted{
		RequestID: subscribeRequest.RequestID, Operation: protocol.FaceOperationSubscribe,
	})); err != nil {
		t.Fatal(err)
	}
	recoveryCommand := nextSent(t, conn)
	recoveryRequest, err := protocol.DecodePayload[protocol.FaceChatStreamGet](&recoveryCommand)
	if err != nil || recoveryRequest.TargetRequestID != "pending-chat" || recoveryRequest.ConversationID != "conversation-1" {
		t.Fatalf("pending Chat recovery = %#v, %v", recoveryRequest, err)
	}
}

func TestStreamEndGapRequestsRecoveryAndSparseFinalResponseReconciles(t *testing.T) {
	term, conn, output := newTestTerminal(t)
	term.active = "conversation-1"
	term.features = map[protocol.FaceFeature]struct{}{protocol.FaceFeatureChatStreamResume: {}}
	stream := term.stream("chat-1")
	stream.responses[4] = "final answer"
	stream.lastSeq = 2
	if err := term.handleEnvelope(testEnvelope(t, protocol.TypeFaceChatStreamEnd, protocol.FaceChatStreamEnd{
		ConversationID: "conversation-1", RequestID: "chat-1", LastSeq: 3, ResponseCount: 4,
		Complete: true, Status: protocol.FaceResultSucceeded,
	})); err != nil {
		t.Fatal(err)
	}
	recoveryCommand := nextSent(t, conn)
	recoveryRequest, _ := protocol.DecodePayload[protocol.FaceChatStreamGet](&recoveryCommand)
	if recoveryRequest.TargetRequestID != "chat-1" {
		t.Fatalf("stream-end recovery = %#v", recoveryRequest)
	}
	delete(term.pending, recoveryRequest.RequestID)
	stream.recovering = false
	term.pending["chat-1"] = pendingRequest{operation: protocol.FaceOperationChat, conversationID: "conversation-1"}
	if err := term.handleEnvelope(testEnvelope(t, protocol.TypeFaceResult, protocol.FaceResult{
		RequestID: "chat-1", ConversationID: "conversation-1",
		Status: protocol.FaceResultSucceeded, Content: "final answer",
	})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "assistant complete") || strings.Contains(output.String(), "assistant final:") {
		t.Fatalf("sparse final response output = %q", output.String())
	}
}

func TestFailedStreamRecoveryCanBeRetried(t *testing.T) {
	term, _, _ := newTestTerminal(t)
	term.active = "conversation-1"
	stream := term.stream("chat-1")
	stream.recovering = true
	term.pending["recover-1"] = pendingRequest{
		operation: protocol.FaceOperationChatStreamGet, conversationID: "conversation-1", targetRequestID: "chat-1",
	}
	if err := term.handleEnvelope(testEnvelope(t, protocol.TypeFaceResult, protocol.FaceResult{
		RequestID: "recover-1", ConversationID: "conversation-1", Status: protocol.FaceResultFailed,
		ErrorCode: protocol.FaceErrorInvalidRequest, Error: "stream unavailable",
	})); err != nil {
		t.Fatal(err)
	}
	if stream.recovering {
		t.Fatal("failed stream recovery remained in progress")
	}
}

func TestRunProgressIsRenderedWithSanitizedText(t *testing.T) {
	term, _, output := newTestTerminal(t)
	term.active = "conversation-1"
	if err := term.handleEnvelope(testEnvelope(t, protocol.TypeFaceRunProgress, protocol.FaceRunProgress{
		ConversationID: "conversation-1", RunID: "run-1", Seq: 1,
		Kind: protocol.ProgressStdout, Data: "ok\x1b[31m", Gap: false,
	})); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "\x1b") || !strings.Contains(output.String(), `\u001b`) {
		t.Fatalf("unsafe progress output = %q", output.String())
	}
}
