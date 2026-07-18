package facegateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
)

type controlledChatLLM struct {
	started chan string
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (p *controlledChatLLM) Chat(ctx context.Context, request *llm.LLMRequest) (*llm.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	input := latestUserMessage(request.Messages)
	if input == "hold" {
		p.started <- input
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.release:
		}
	}
	return &llm.LLMResponse{Content: "reply:" + input}, nil
}

func (p *controlledChatLLM) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type cancellingChatLLM struct {
	started    chan struct{}
	cancelled  chan struct{}
	finish     chan struct{}
	onceStart  sync.Once
	onceCancel sync.Once
}

func (p *cancellingChatLLM) Chat(ctx context.Context, _ *llm.LLMRequest) (*llm.LLMResponse, error) {
	p.onceStart.Do(func() { close(p.started) })
	<-ctx.Done()
	p.onceCancel.Do(func() { close(p.cancelled) })
	<-p.finish
	return nil, ctx.Err()
}

func latestUserMessage(messages []llm.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == llm.RoleUser {
			return messages[index].Content
		}
	}
	return ""
}

func createConversation(t *testing.T, fixture *gatewayFixture, id string) {
	t.Helper()
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), id, id); err != nil {
		t.Fatal(err)
	}
}

func chatIdentity() protocol.FaceIdentity {
	return protocol.FaceIdentity{
		ID: "chat-principal", Label: "chat-face",
		Scopes: []protocol.FaceScope{protocol.FaceScopeChat, protocol.FaceScopeSessionsRead},
	}
}

func TestChatLifecycleIsIdempotentAndRejectsConflicts(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{Response: llm.LLMResponse{Content: "done"}})
	fixture := newGatewayFixtureWithProvider(t, 16, provider)
	createConversation(t, fixture, "conv-chat")
	identity := chatIdentity()
	state := newGatewayTestConnection(16, identity)
	request := protocol.FaceChat{RequestID: "chat-1", ConversationID: "conv-chat", Content: "hello"}

	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, request))
	accepted := nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	if accepted.Operation != protocol.FaceOperationChat {
		t.Fatalf("accepted = %+v", accepted)
	}
	result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if result.Status != protocol.FaceResultSucceeded || result.Content != "done" {
		t.Fatalf("Chat result = %+v", result)
	}

	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, request))
	replayed := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if replayed.RequestID != result.RequestID || replayed.Status != result.Status || replayed.Content != result.Content || provider.Calls() != 1 {
		t.Fatalf("replayed result = %+v, calls = %d", replayed, provider.Calls())
	}

	request.Content = "different"
	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, request))
	faceErr := nextPayload[protocol.FaceError](t, state, protocol.TypeFaceError)
	if faceErr.Code != protocol.FaceErrorRequestConflict {
		t.Fatalf("conflict response = %+v", faceErr)
	}
	assertNoQueuedMessage(t, state)

	messages, err := fixture.store.GetMessages("conv-chat")
	if err != nil || len(messages) != 2 || messages[0].Content != "hello" || messages[1].Content != "done" {
		t.Fatalf("persisted Chat = %+v, %v", messages, err)
	}
}

func TestChatBusyPendingSnapshotAndCrossConversationConcurrency(t *testing.T) {
	provider := &controlledChatLLM{started: make(chan string, 1), release: make(chan struct{})}
	fixture := newGatewayFixtureWithProvider(t, 32, provider)
	createConversation(t, fixture, "conv-a")
	createConversation(t, fixture, "conv-b")
	identity := chatIdentity()
	state := newGatewayTestConnection(32, identity)

	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: "chat-hold", ConversationID: "conv-a", Content: "hold",
	}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("blocking Chat did not start")
	}
	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: "chat-hold", ConversationID: "conv-a", Content: "hold",
	}))
	replayed := nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	if replayed.RequestID != "chat-hold" || provider.Calls() != 1 {
		t.Fatalf("active Chat replay = %+v, calls = %d", replayed, provider.Calls())
	}
	snapshot, err := fixture.gateway.snapshot(identity, "conv-a")
	if err != nil || len(snapshot.PendingChats) != 1 || snapshot.PendingChats[0].RequestID != "chat-hold" {
		t.Fatalf("pending snapshot = %+v, %v", snapshot.PendingChats, err)
	}
	readOnly := identity
	readOnly.Scopes = []protocol.FaceScope{protocol.FaceScopeSessionsRead}
	redacted, err := fixture.gateway.snapshot(readOnly, "conv-a")
	if err != nil || len(redacted.PendingChats) != 0 {
		t.Fatalf("read-only pending snapshot = %+v, %v", redacted.PendingChats, err)
	}

	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: "chat-busy", ConversationID: "conv-a", Content: "second",
	}))
	faceErr := nextPayload[protocol.FaceError](t, state, protocol.TypeFaceError)
	if faceErr.Code != protocol.FaceErrorBusy || !faceErr.Retryable {
		t.Fatalf("busy response = %+v", faceErr)
	}

	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: "chat-fast", ConversationID: "conv-b", Content: "fast",
	}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	fast := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if fast.RequestID != "chat-fast" || fast.Content != "reply:fast" {
		t.Fatalf("parallel Chat result = %+v", fast)
	}
	close(provider.release)
	held := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if held.RequestID != "chat-hold" || held.Content != "reply:hold" || provider.Calls() != 2 {
		t.Fatalf("held Chat result = %+v, calls = %d", held, provider.Calls())
	}
}

func TestFaceDisconnectDoesNotCancelAcceptedChat(t *testing.T) {
	provider := &controlledChatLLM{started: make(chan string, 1), release: make(chan struct{})}
	fixture := newGatewayFixtureWithProvider(t, 16, provider)
	createConversation(t, fixture, "conv-disconnect")
	identity := chatIdentity()
	state := newGatewayTestConnection(16, identity)
	fixture.gateway.mu.Lock()
	fixture.gateway.connections[state.peer] = state
	fixture.gateway.mu.Unlock()
	request := protocol.FaceChat{RequestID: "chat-disconnect", ConversationID: "conv-disconnect", Content: "hold"}
	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, request))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("disconnect Chat did not start")
	}
	fixture.gateway.HandleFaceDisconnect(state.peer)
	close(provider.release)
	waitForTerminalRequest(t, fixture.gateway.chats, requestKey{identityID: identity.ID, requestID: request.RequestID})

	reconnected := newGatewayTestConnection(4, identity)
	fixture.gateway.handleChat(reconnected, identity, commandEnvelope(t, protocol.TypeFaceChat, request))
	result := nextPayload[protocol.FaceResult](t, reconnected, protocol.TypeFaceResult)
	if result.Status != protocol.FaceResultSucceeded || result.Content != "reply:hold" || provider.Calls() != 1 {
		t.Fatalf("reconnected Chat replay = %+v, calls %d", result, provider.Calls())
	}
}

func TestChatCancelIsIdempotentAndSingleFlight(t *testing.T) {
	provider := &cancellingChatLLM{
		started: make(chan struct{}), cancelled: make(chan struct{}), finish: make(chan struct{}),
	}
	fixture := newGatewayFixtureWithProvider(t, 32, provider)
	createConversation(t, fixture, "conv-cancel")
	identity := chatIdentity()
	state := newGatewayTestConnection(32, identity)

	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: "chat-target", ConversationID: "conv-cancel", Content: "wait",
	}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("cancellable Chat did not start")
	}
	cancelRequest := protocol.FaceChatCancel{
		RequestID: "cancel-1", TargetRequestID: "chat-target", ConversationID: "conv-cancel", Reason: "user",
	}
	fixture.gateway.handleChatCancel(state, identity, commandEnvelope(t, protocol.TypeFaceChatCancel, cancelRequest))
	accepted := nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	if accepted.Operation != protocol.FaceOperationChatCancel {
		t.Fatalf("cancel accepted = %+v", accepted)
	}
	cancelResult := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if cancelResult.RequestID != "cancel-1" || cancelResult.Status != protocol.FaceResultSucceeded {
		t.Fatalf("cancel result = %+v", cancelResult)
	}
	select {
	case <-provider.cancelled:
	case <-time.After(time.Second):
		t.Fatal("Chat context was not cancelled")
	}

	fixture.gateway.handleChatCancel(state, identity, commandEnvelope(t, protocol.TypeFaceChatCancel, protocol.FaceChatCancel{
		RequestID: "cancel-2", TargetRequestID: "chat-target", ConversationID: "conv-cancel",
	}))
	faceErr := nextPayload[protocol.FaceError](t, state, protocol.TypeFaceError)
	if faceErr.Code != protocol.FaceErrorRequestInProgress {
		t.Fatalf("second cancellation = %+v", faceErr)
	}

	fixture.gateway.handleChatCancel(state, identity, commandEnvelope(t, protocol.TypeFaceChatCancel, cancelRequest))
	replayed := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if replayed.RequestID != cancelResult.RequestID || replayed.Status != cancelResult.Status || replayed.Content != cancelResult.Content {
		t.Fatalf("cancel replay = %+v, want %+v", replayed, cancelResult)
	}
	close(provider.finish)
	targetResult := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if targetResult.RequestID != "chat-target" || targetResult.Status != protocol.FaceResultCancelled || targetResult.ErrorCode != protocol.FaceErrorCancelled {
		t.Fatalf("cancelled Chat result = %+v", targetResult)
	}
}

func TestChatCancelPropagatesToLocalTool(t *testing.T) {
	const toolName = "facegateway_cancellable_tool"
	toolStarted := make(chan struct{})
	toolCancelled := make(chan struct{})
	executor.Register(executor.Tool{
		Name: toolName,
		Execute: func(ctx context.Context, _ json.RawMessage) *executor.ToolResult {
			close(toolStarted)
			<-ctx.Done()
			close(toolCancelled)
			return &executor.ToolResult{Error: ctx.Err().Error()}
		},
	})
	provider := llm.NewScriptedProvider(
		llm.ScriptedStep{Response: llm.LLMResponse{ToolCalls: []llm.ToolCall{{
			ID: "local-tool-call", Name: toolName, Args: `{}`,
		}}}},
		llm.ScriptedStep{Response: llm.LLMResponse{Content: "must not complete"}},
	)
	fixture := newGatewayFixtureWithProvider(t, 16, provider)
	createConversation(t, fixture, "conv-local-cancel")
	identity := chatIdentity()
	state := newGatewayTestConnection(16, identity)
	chat := protocol.FaceChat{
		RequestID: "chat-local-cancel", ConversationID: "conv-local-cancel", Content: "use local tool",
	}
	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, chat))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	select {
	case <-toolStarted:
	case <-time.After(time.Second):
		t.Fatal("local tool did not start")
	}

	cancel := protocol.FaceChatCancel{
		RequestID: "cancel-local-tool", TargetRequestID: chat.RequestID, ConversationID: chat.ConversationID,
	}
	fixture.gateway.handleChatCancel(state, identity, commandEnvelope(t, protocol.TypeFaceChatCancel, cancel))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	_ = nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	select {
	case <-toolCancelled:
	case <-time.After(time.Second):
		t.Fatal("local tool did not receive Chat cancellation")
	}
	result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if result.RequestID != chat.RequestID || result.Status != protocol.FaceResultCancelled || provider.Calls() != 1 {
		t.Fatalf("local tool cancellation result = %+v, calls = %d", result, provider.Calls())
	}
}

func TestScriptedChatPublishesRedactedToolLifecycle(t *testing.T) {
	const toolName = "facegateway_scripted_tool"
	executor.Register(executor.Tool{
		Name: toolName,
		Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
			return &executor.ToolResult{Success: true, Output: "tool-output"}
		},
	})
	provider := llm.NewScriptedProvider(
		llm.ScriptedStep{Response: llm.LLMResponse{ToolCalls: []llm.ToolCall{{
			ID: "tool-call-1", Name: toolName, Args: `{"value":"top-secret","confirm":false}`,
		}}}},
		llm.ScriptedStep{
			Check: func(request *llm.LLMRequest) error {
				if len(request.Messages) == 0 || request.Messages[len(request.Messages)-1].Role != llm.RoleTool ||
					request.Messages[len(request.Messages)-1].Content != "tool-output" {
					return fmt.Errorf("tool result missing from scripted request")
				}
				return nil
			},
			Response: llm.LLMResponse{Content: "final-answer"},
		},
	)
	fixture := newGatewayFixtureWithProvider(t, 32, provider)
	createConversation(t, fixture, "conv-tools")
	identity := chatIdentity()
	state := newGatewayTestConnection(32, identity)
	fixture.gateway.mu.Lock()
	fixture.gateway.connections[state.peer] = state
	fixture.gateway.mu.Unlock()
	fixture.gateway.handleSubscribe(state, identity, commandEnvelope(t, protocol.TypeFaceSubscribe, protocol.FaceSubscribe{
		RequestID: "subscribe-chat", ConversationIDs: []string{"conv-tools"},
		EventTypes: []protocol.FaceEventType{
			protocol.FaceEventChatStarted, protocol.FaceEventChatToolCalled,
			protocol.FaceEventChatToolCompleted, protocol.FaceEventChatCompleted,
		},
	}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)

	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: "chat-tools", ConversationID: "conv-tools", Content: "use a tool",
	}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	started := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	called := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	completed := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	terminal := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if started.Type != protocol.FaceEventChatStarted || called.Type != protocol.FaceEventChatToolCalled ||
		completed.Type != protocol.FaceEventChatToolCompleted || terminal.Type != protocol.FaceEventChatCompleted {
		t.Fatalf("Chat event order = %s, %s, %s, %s", started.Type, called.Type, completed.Type, terminal.Type)
	}
	callData, err := protocol.StrictDecode[protocol.ChatToolCalledEventData](called.Data)
	if err != nil || !strings.HasPrefix(callData.ArgsDigest, "sha256:") || strings.Contains(callData.ArgsDigest, "top-secret") {
		t.Fatalf("tool call projection = %+v, %v", callData, err)
	}
	completeData, err := protocol.StrictDecode[protocol.ChatToolCompletedEventData](completed.Data)
	if err != nil || !completeData.Success || result.Content != "final-answer" || provider.Calls() != 2 {
		t.Fatalf("tool lifecycle = %+v, result %+v, calls %d, %v", completeData, result, provider.Calls(), err)
	}
}

func TestChatFailureProjectionRedactsProviderError(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{Err: errors.New("provider leaked top-secret")})
	fixture := newGatewayFixtureWithProvider(t, 16, provider)
	createConversation(t, fixture, "conv-failure")
	identity := chatIdentity()
	state := newGatewayTestConnection(16, identity)
	fixture.gateway.mu.Lock()
	fixture.gateway.connections[state.peer] = state
	fixture.gateway.mu.Unlock()
	fixture.gateway.handleSubscribe(state, identity, commandEnvelope(t, protocol.TypeFaceSubscribe, protocol.FaceSubscribe{
		RequestID: "subscribe-failure", ConversationIDs: []string{"conv-failure"},
		EventTypes: []protocol.FaceEventType{protocol.FaceEventChatFailed},
	}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)

	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: "chat-failure", ConversationID: "conv-failure", Content: "fail safely",
	}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	event := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if event.Type != protocol.FaceEventChatFailed || event.Message != "Chat failed" || strings.Contains(string(event.Data), "top-secret") {
		t.Fatalf("failure event leaked provider error: %+v", event)
	}
	if result.Status != protocol.FaceResultFailed || result.ErrorCode != protocol.FaceErrorInternal ||
		result.Error != "Chat failed" || result.Content != "" || strings.Contains(result.Error, "top-secret") {
		t.Fatalf("failure result = %+v", result)
	}
}

func TestChatDeadlineProjectionIsStable(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{Err: context.DeadlineExceeded})
	fixture := newGatewayFixtureWithProvider(t, 8, provider)
	createConversation(t, fixture, "conv-timeout")
	identity := chatIdentity()
	state := newGatewayTestConnection(8, identity)
	fixture.gateway.handleChat(state, identity, commandEnvelope(t, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: "chat-timeout", ConversationID: "conv-timeout", Content: "time out",
	}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if result.Status != protocol.FaceResultTimedOut || result.ErrorCode != protocol.FaceErrorTimeout ||
		result.Error != "Chat timed out" || result.Content != "" {
		t.Fatalf("timeout result = %+v", result)
	}
}

func waitForTerminalRequest(t *testing.T, registry *chatRegistry, key requestKey) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		registry.mu.Lock()
		record := registry.requests[key]
		terminal := record != nil && record.terminal
		registry.mu.Unlock()
		if terminal {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("request did not reach a terminal state")
		}
		time.Sleep(time.Millisecond)
	}
}
