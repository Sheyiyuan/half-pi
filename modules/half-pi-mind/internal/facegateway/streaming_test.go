package facegateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func TestChatStreamBroadcastEndRecoveryAndOptIn(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{
		Response: llm.LLMResponse{Content: "你好 world"},
		Deltas:   []string{"你", "好", " ", "world"},
	})
	fixture := newGatewayFixtureWithProvider(t, 32, provider)
	createConversation(t, fixture, "stream-conversation")
	identity := protocol.FaceIdentity{
		ID: "stream-face", Label: "stream-face",
		Scopes: []protocol.FaceScope{protocol.FaceScopeChat, protocol.FaceScopeSessionsRead, protocol.FaceScopeHandsRead},
	}
	origin := newGatewayTestConnection(32, identity)
	observerIdentity := identity
	observerIdentity.ID, observerIdentity.Label = "observer-face", "observer-face"
	observer := newGatewayTestConnection(32, observerIdentity)
	quietIdentity := identity
	quietIdentity.ID, quietIdentity.Label = "quiet-face", "quiet-face"
	quiet := newGatewayTestConnection(32, quietIdentity)
	fixture.gateway.mu.Lock()
	fixture.gateway.connections[origin.peer] = origin
	fixture.gateway.connections[observer.peer] = observer
	fixture.gateway.connections[quiet.peer] = quiet
	fixture.gateway.mu.Unlock()
	subscribeForStream(t, fixture.gateway, origin, identity, "stream-conversation", []protocol.FaceTransientType{protocol.FaceTransientChatDelta})
	subscribeForStream(t, fixture.gateway, observer, observerIdentity, "stream-conversation", []protocol.FaceTransientType{protocol.FaceTransientChatDelta})
	subscribeForStream(t, fixture.gateway, quiet, quietIdentity, "stream-conversation", nil)

	fixture.gateway.handleChat(origin, identity, commandEnvelope(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: "stream-request", ConversationID: "stream-conversation", Content: "hello",
	}))
	_ = nextPayload[protocol.FaceAccepted](t, origin, protocol.TypeFaceAccepted)
	originDelta := nextPayload[protocol.FaceChatDelta](t, origin, protocol.TypeFaceChatDelta)
	observerDelta := nextPayload[protocol.FaceChatDelta](t, observer, protocol.TypeFaceChatDelta)
	if originDelta != observerDelta || originDelta.Delta != "你好 world" || originDelta.Seq != 1 || originDelta.Offset != 0 {
		t.Fatalf("stream deltas = origin %#v observer %#v", originDelta, observerDelta)
	}
	originEnd := nextPayload[protocol.FaceChatStreamEnd](t, origin, protocol.TypeFaceChatStreamEnd)
	observerEnd := nextPayload[protocol.FaceChatStreamEnd](t, observer, protocol.TypeFaceChatStreamEnd)
	if originEnd != observerEnd || !originEnd.Complete || originEnd.LastSeq != 1 || originEnd.ResponseCount != 1 {
		t.Fatalf("stream ends = origin %#v observer %#v", originEnd, observerEnd)
	}
	result := nextPayload[protocol.FaceResult](t, origin, protocol.TypeFaceResult)
	if result.Content != originDelta.Delta || result.Status != protocol.FaceResultSucceeded {
		t.Fatalf("authoritative result = %#v", result)
	}
	assertNoQueuedMessage(t, quiet)

	fixture.gateway.handleChatStreamGet(observer, observerIdentity, commandEnvelope(t, protocol.TypeFaceChatStreamGet, protocol.FaceChatStreamGet{
		RequestID: "recover-request", ConversationID: "stream-conversation", TargetRequestID: "stream-request",
	}))
	_ = nextPayload[protocol.FaceAccepted](t, observer, protocol.TypeFaceAccepted)
	recoveryResult := nextPayload[protocol.FaceResult](t, observer, protocol.TypeFaceResult)
	recovery, err := protocol.StrictDecode[protocol.ChatStreamGetResult](recoveryResult.Data)
	if err != nil || !recovery.Terminal || recovery.Status != protocol.FaceResultSucceeded || recovery.LastSeq != 1 ||
		len(recovery.Responses) != 1 || recovery.Responses[0].Content != result.Content || !recovery.Responses[0].Complete {
		t.Fatalf("stream recovery = %#v, %v", recovery, err)
	}
}

func TestCapabilitiesMessagesAndRunProgress(t *testing.T) {
	fixture := newGatewayFixture(t, 32)
	createConversation(t, fixture, "query-conversation")
	if err := fixture.store.AppendMessages("query-conversation", 0, []store.Message{
		{Role: "user", Content: "one", RequestID: "chat-1", Seq: 1},
		{Role: "assistant", Content: "two", RequestID: "chat-1", Seq: 2},
		{Role: "user", Content: "three", RequestID: "chat-2", Seq: 3},
	}); err != nil {
		t.Fatal(err)
	}
	identity := protocol.FaceIdentity{
		ID: "query-face", Label: "query-face",
		Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead, protocol.FaceScopeRunsOutput, protocol.FaceScopeHandsRead},
	}
	state := newGatewayTestConnection(32, identity)

	fixture.gateway.handleCapabilitiesGet(state, identity, commandEnvelope(t, protocol.TypeFaceCapabilitiesGet,
		protocol.FaceCapabilitiesGet{RequestID: "capabilities"}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	capResult := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	capabilities, err := protocol.StrictDecode[protocol.FaceCapabilitiesResult](capResult.Data)
	if err != nil || capabilities.Revision != protocol.FaceProtocolRevision || len(capabilities.Features) != 4 || capabilities.Identity.ID != identity.ID {
		t.Fatalf("capabilities = %#v, %v", capabilities, err)
	}

	fixture.gateway.handleConversationMessages(state, identity, commandEnvelope(t, protocol.TypeFaceConversationMessages,
		protocol.FaceConversationMessages{RequestID: "messages", ConversationID: "query-conversation", Limit: 2}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	messageResult := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	page, err := protocol.StrictDecode[protocol.ConversationMessagesResult](messageResult.Data)
	if err != nil || !page.HasMore || page.NextBeforeSeq != 2 || len(page.Messages) != 2 ||
		page.Messages[0].RequestID != "chat-1" || page.Messages[1].RequestID != "chat-2" {
		t.Fatalf("message page = %#v, %v", page, err)
	}

	fixture.gateway.mu.Lock()
	fixture.gateway.connections[state.peer] = state
	fixture.gateway.mu.Unlock()
	subscribeForStream(t, fixture.gateway, state, identity, "query-conversation", []protocol.FaceTransientType{protocol.FaceTransientRunProgress})
	fixture.gateway.PublishRunProgress(remoteexec.ProgressObservation{
		Run: remoteexec.Run{
			ID: "run-1", SessionID: "query-conversation", Status: protocol.RunRunning,
			Metadata: remoteexec.AuditMetadata{RequestID: "chat-2"},
		},
		Progress: protocol.RPCProgress{RunID: "run-1", Seq: 3, Kind: protocol.ProgressStderr, Data: "warning"},
		Gap:      true,
	})
	progress := nextPayload[protocol.FaceRunProgress](t, state, protocol.TypeFaceRunProgress)
	if progress.RequestID != "chat-2" || progress.Seq != 3 || !progress.Gap || progress.Data != "warning" {
		t.Fatalf("run progress = %#v", progress)
	}
	fixture.gateway.PublishRunProgress(remoteexec.ProgressObservation{
		Run:      remoteexec.Run{ID: "task-1", SessionID: "query-conversation", Status: protocol.RunRunning, DurableTask: true},
		Progress: protocol.RPCProgress{RunID: "task-1", Seq: 1, Kind: protocol.ProgressStdout, Data: "task"},
	})
	assertNoQueuedMessage(t, state)

	deniedIdentity := protocol.FaceIdentity{
		ID: "no-output-face", Label: "no-output-face", Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead},
	}
	denied := newGatewayTestConnection(4, deniedIdentity)
	fixture.gateway.handleSubscribe(denied, deniedIdentity, commandEnvelope(t, protocol.TypeFaceSubscribe, protocol.FaceSubscribe{
		RequestID: "subscribe-denied", ConversationIDs: []string{"query-conversation"},
		TransientTypes: []protocol.FaceTransientType{protocol.FaceTransientRunProgress},
	}))
	if faceErr := nextPayload[protocol.FaceError](t, denied, protocol.TypeFaceError); faceErr.Code != protocol.FaceErrorForbidden {
		t.Fatalf("run progress subscription error = %#v", faceErr)
	}
}

func TestChatStreamFailureAndCancellationEndIncomplete(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status protocol.FaceResultStatus
	}{
		{name: "provider failure", err: errors.New("provider failed"), status: protocol.FaceResultFailed},
		{name: "context cancellation", err: context.Canceled, status: protocol.FaceResultCancelled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &partialStreamProvider{err: test.err}
			fixture := newGatewayFixtureWithProvider(t, 32, provider)
			conversationID := "incomplete-stream"
			createConversation(t, fixture, conversationID)
			identity := protocol.FaceIdentity{
				ID: "stream-face", Label: "stream-face",
				Scopes: []protocol.FaceScope{protocol.FaceScopeChat, protocol.FaceScopeSessionsRead, protocol.FaceScopeHandsRead},
			}
			state := newGatewayTestConnection(32, identity)
			fixture.gateway.mu.Lock()
			fixture.gateway.connections[state.peer] = state
			fixture.gateway.mu.Unlock()
			subscribeForStream(t, fixture.gateway, state, identity, conversationID,
				[]protocol.FaceTransientType{protocol.FaceTransientChatDelta})

			fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, protocol.FaceChat{
				RequestID: "incomplete-request", ConversationID: conversationID, Content: "hello",
			}))
			_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
			delta := nextPayload[protocol.FaceChatDelta](t, state, protocol.TypeFaceChatDelta)
			end := nextPayload[protocol.FaceChatStreamEnd](t, state, protocol.TypeFaceChatStreamEnd)
			result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
			if delta.Delta != "partial" || end.Complete || end.Status != test.status || end.LastSeq != delta.Seq ||
				result.Status != test.status || result.Content != "" {
				t.Fatalf("incomplete stream = delta %#v end %#v result %#v", delta, end, result)
			}
		})
	}
}

func TestOutboundSchedulerEvictsTransientForReliable(t *testing.T) {
	queue := newOutboundScheduler(2)
	if !queue.enqueue(protocol.Envelope{Type: protocol.TypeFaceChatDelta, Payload: []byte(`{"delta":"one"}`)}, true) ||
		!queue.enqueue(protocol.Envelope{Type: protocol.TypeFaceRunProgress, Payload: []byte(`{"data":"two"}`)}, true) {
		t.Fatal("transient setup failed")
	}
	if !queue.enqueue(protocol.Envelope{Type: protocol.TypeFaceResult, Payload: []byte(`{"status":"succeeded"}`)}, false) {
		t.Fatal("reliable message did not evict a transient")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first, ok := queue.pop(ctx)
	if !ok || first.Type != protocol.TypeFaceRunProgress {
		t.Fatalf("first retained item = %#v", first)
	}
	second, ok := queue.pop(ctx)
	if !ok || second.Type != protocol.TypeFaceResult {
		t.Fatalf("reliable item = %#v", second)
	}
}

func TestTransientOverflowDoesNotMarkConnectionSlow(t *testing.T) {
	gateway := &Gateway{}
	state := &connection{outbound: newOutboundScheduler(1)}
	state.mu.Lock()
	if !gateway.enqueueTransientLocked(state, protocol.Envelope{Type: protocol.TypeFaceChatDelta, Payload: []byte(`{"delta":"one"}`)}) {
		t.Fatal("first transient was not queued")
	}
	if gateway.enqueueTransientLocked(state, protocol.Envelope{Type: protocol.TypeFaceChatDelta, Payload: []byte(`{"delta":"two"}`)}) {
		t.Fatal("overflowing transient was queued")
	}
	if state.slow {
		t.Fatal("transient overflow marked connection slow")
	}
	if !gateway.enqueueLocked(state, protocol.Envelope{Type: protocol.TypeFaceResult, Payload: []byte(`{"status":"succeeded"}`)}) {
		t.Fatal("reliable message did not evict queued transient")
	}
	state.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	message, ok := state.outbound.pop(ctx)
	if !ok || message.Type != protocol.TypeFaceResult || state.slow {
		t.Fatalf("retained message = %#v slow=%t", message, state.slow)
	}
}

func subscribeForStream(t *testing.T, gateway *Gateway, state *connection, identity protocol.FaceIdentity, conversationID string, transient []protocol.FaceTransientType) {
	t.Helper()
	gateway.handleSubscribe(state, identity, commandEnvelope(t, protocol.TypeFaceSubscribe, protocol.FaceSubscribe{
		RequestID: "subscribe-" + identity.ID, ConversationIDs: []string{conversationID},
		EventTypes: []protocol.FaceEventType{protocol.FaceEventHandConnected}, TransientTypes: transient,
	}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
}

type partialStreamProvider struct {
	err error
}

func (p *partialStreamProvider) Chat(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error) {
	return nil, p.err
}

func (p *partialStreamProvider) ChatStream(_ context.Context, _ *llm.LLMRequest, onDelta llm.TextDeltaFunc) (*llm.LLMResponse, error) {
	if err := onDelta("partial"); err != nil {
		return nil, err
	}
	return nil, p.err
}
