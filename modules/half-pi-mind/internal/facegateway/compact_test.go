package facegateway

import (
	"context"
	"sync"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/compact"
)

func TestCompactFeatureNegotiationIsConnectionScoped(t *testing.T) {
	fixture := newGatewayFixture(t, 16)
	identity := protocol.FaceIdentity{
		ID: "face-compact", Label: "terminal",
		Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead, protocol.FaceScopeSessionsWrite},
	}
	legacy := newGatewayTestConnection(16, identity)
	fixture.gateway.handleCapabilitiesGet(legacy, identity, commandEnvelope(t, protocol.TypeFaceCapabilitiesGet,
		protocol.FaceCapabilitiesGet{RequestID: "legacy"}))
	_ = nextPayload[protocol.FaceAccepted](t, legacy, protocol.TypeFaceAccepted)
	legacyResult := nextPayload[protocol.FaceResult](t, legacy, protocol.TypeFaceResult)
	legacyCapabilities, err := protocol.StrictDecode[protocol.FaceCapabilitiesResult](legacyResult.Data)
	if err != nil {
		t.Fatal(err)
	}
	if containsFeature(legacyCapabilities.Features, protocol.FaceFeatureContextCompaction) {
		t.Fatal("legacy capabilities request negotiated Compact")
	}

	modern := newGatewayTestConnection(16, identity)
	fixture.gateway.handleCapabilitiesGet(modern, identity, commandEnvelope(t, protocol.TypeFaceCapabilitiesGet,
		protocol.FaceCapabilitiesGet{RequestID: "modern", AcceptFeatures: []string{string(protocol.FaceFeatureContextCompaction), "future.v1"}}))
	_ = nextPayload[protocol.FaceAccepted](t, modern, protocol.TypeFaceAccepted)
	modernResult := nextPayload[protocol.FaceResult](t, modern, protocol.TypeFaceResult)
	modernCapabilities, err := protocol.StrictDecode[protocol.FaceCapabilitiesResult](modernResult.Data)
	if err != nil {
		t.Fatal(err)
	}
	if !containsFeature(modernCapabilities.Features, protocol.FaceFeatureContextCompaction) {
		t.Fatal("modern capabilities request did not negotiate Compact")
	}

	request := protocol.FaceSubscribe{
		RequestID: "subscribe", EventTypes: []protocol.FaceEventType{protocol.FaceEventCompactCompleted},
	}
	fixture.gateway.handleSubscribe(legacy, identity, commandEnvelope(t, protocol.TypeFaceSubscribe, request))
	if failure := nextPayload[protocol.FaceError](t, legacy, protocol.TypeFaceError); failure.Code != protocol.FaceErrorInvalidRequest {
		t.Fatalf("legacy Compact subscription code = %q", failure.Code)
	}
	fixture.gateway.handleSubscribe(modern, identity, commandEnvelope(t, protocol.TypeFaceSubscribe, request))
	if accepted := nextPayload[protocol.FaceAccepted](t, modern, protocol.TypeFaceAccepted); accepted.Operation != protocol.FaceOperationSubscribe {
		t.Fatalf("modern subscription operation = %q", accepted.Operation)
	}
}

func TestFaceCompactAdmissionBusyAndReplay(t *testing.T) {
	fixture := newGatewayFixture(t, 32)
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), "conv-compact", "Compact"); err != nil {
		t.Fatal(err)
	}
	actor, err := fixture.conversations.Get("conv-compact")
	if err != nil {
		t.Fatal(err)
	}
	compactor := &blockingCompactor{started: make(chan struct{}), release: make(chan struct{})}
	fixture.conversations.SetCompactor(compactor, compact.RuntimeConfig{})
	identity := protocol.FaceIdentity{
		ID: "face-compact", Label: "terminal",
		Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead, protocol.FaceScopeSessionsWrite},
	}
	state := newGatewayTestConnection(32, identity)
	state.features = map[protocol.FaceFeature]struct{}{protocol.FaceFeatureContextCompaction: {}}
	ratio := .60
	request := protocol.FaceConversationCompact{
		RequestID: "compact-1", ConversationID: "conv-compact",
		Target: protocol.FaceCompactTarget{Mode: protocol.FaceCompactTargetRatio, Ratio: &ratio},
	}
	fixture.gateway.handleConversationCompact(state, identity, commandEnvelope(t, protocol.TypeFaceConversationCompact, request))
	if accepted := nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted); accepted.Operation != protocol.FaceOperationConversationCompact {
		t.Fatalf("Compact accepted operation = %q", accepted.Operation)
	}
	<-compactor.started

	fixture.gateway.handleConversationCompact(state, identity, commandEnvelope(t, protocol.TypeFaceConversationCompact, request))
	if replay := nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted); replay.RequestID != request.RequestID {
		t.Fatalf("in-progress replay = %+v", replay)
	}
	busy := request
	busy.RequestID = "compact-busy"
	fixture.gateway.handleConversationCompact(state, identity, commandEnvelope(t, protocol.TypeFaceConversationCompact, busy))
	if failure := nextPayload[protocol.FaceError](t, state, protocol.TypeFaceError); failure.Code != protocol.FaceErrorBusy {
		t.Fatalf("busy Compact code = %q", failure.Code)
	}
	conflict := protocol.FaceChat{RequestID: request.RequestID, ConversationID: request.ConversationID, Content: "different operation"}
	fixture.gateway.handleChat(state, protocol.FaceIdentity{ID: identity.ID, Label: identity.Label, Scopes: []protocol.FaceScope{protocol.FaceScopeChat}},
		commandEnvelope(t, protocol.TypeFaceChat, conflict))
	if failure := nextPayload[protocol.FaceError](t, state, protocol.TypeFaceError); failure.Code != protocol.FaceErrorRequestConflict {
		t.Fatalf("cross-operation conflict code = %q", failure.Code)
	}

	close(compactor.release)
	result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if result.Status != protocol.FaceResultSucceeded {
		t.Fatalf("Compact result = %+v", result)
	}
	fixture.gateway.handleConversationCompact(state, identity, commandEnvelope(t, protocol.TypeFaceConversationCompact, request))
	replayed := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if replayed.Status != protocol.FaceResultSucceeded || compactor.calls != 1 || actor.OperationState() != "idle" {
		t.Fatalf("terminal replay = %+v, calls=%d, state=%s", replayed, compactor.calls, actor.OperationState())
	}
}

func TestFaceCompactEventAndResultOrdering(t *testing.T) {
	fixture := newGatewayFixture(t, 32)
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), "conv-events", "Compact events"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.conversations.Get("conv-events"); err != nil {
		t.Fatal(err)
	}
	fixture.conversations.SetCompactor(eventCompactor{}, compact.RuntimeConfig{})
	identity := protocol.FaceIdentity{
		ID: "face-events", Label: "events",
		Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead, protocol.FaceScopeSessionsWrite},
	}
	state := newGatewayTestConnection(32, identity)
	state.features = map[protocol.FaceFeature]struct{}{protocol.FaceFeatureContextCompaction: {}}
	state.subscribed = true
	state.filter = subscription{
		conversations: map[string]struct{}{"conv-events": {}},
		events: map[protocol.FaceEventType]struct{}{
			protocol.FaceEventCompactRequested: {}, protocol.FaceEventCompactStarted: {},
			protocol.FaceEventCompactCompleted: {}, protocol.FaceEventConversationChanged: {},
		},
	}
	fixture.gateway.mu.Lock()
	fixture.gateway.connections[state.peer] = state
	fixture.gateway.mu.Unlock()
	request := protocol.FaceConversationCompact{
		RequestID: "compact-events", ConversationID: "conv-events",
		Target: protocol.FaceCompactTarget{Mode: protocol.FaceCompactTargetDefault},
	}
	fixture.gateway.handleConversationCompact(state, identity, commandEnvelope(t, protocol.TypeFaceConversationCompact, request))
	if accepted := nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted); accepted.Operation != protocol.FaceOperationConversationCompact {
		t.Fatalf("accepted = %+v", accepted)
	}
	for _, eventType := range []protocol.FaceEventType{
		protocol.FaceEventCompactRequested, protocol.FaceEventCompactStarted,
		protocol.FaceEventCompactCompleted, protocol.FaceEventConversationChanged,
	} {
		event := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
		if event.Type != eventType {
			t.Fatalf("event type = %q, want %q", event.Type, eventType)
		}
	}
	if result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult); result.Status != protocol.FaceResultSucceeded {
		t.Fatalf("result = %+v", result)
	}
}

type blockingCompactor struct {
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
	calls   int
}

func (c *blockingCompactor) Status(context.Context, compact.CompactStatusRequest) (compact.CompactStatus, error) {
	return compact.CompactStatus{
		Enabled: true, OperationState: "idle", InputBudget: 1000, HighLimit: 800,
		Warnings: []string{}, ProjectionVersion: compact.ProjectionVersion, PolicyVersion: "compact-v1", Profile: "default",
	}, nil
}

func (c *blockingCompactor) Compact(ctx context.Context, request compact.CompactRequest) (compact.CompactResult, error) {
	c.mu.Lock()
	c.calls++
	if c.calls == 1 {
		close(c.started)
	}
	c.mu.Unlock()
	select {
	case <-c.release:
	case <-ctx.Done():
		return compact.CompactResult{}, ctx.Err()
	}
	return compact.CompactResult{
		SummaryID: "summary-1", FromSeq: 1, ToSeq: 2, BeforeEstimatedTokens: 900, AfterEstimatedTokens: 400,
		RetainedFromSeq: 3, RetainedToSeq: 4, GenerationMode: "full", ContextVersion: 5,
		TargetResult: compact.TokenTargetResult{TargetTokens: 600, TargetMet: true},
	}, nil
}

func containsFeature(features []protocol.FaceFeature, target protocol.FaceFeature) bool {
	for _, feature := range features {
		if feature == target {
			return true
		}
	}
	return false
}

type eventCompactor struct{}

func (eventCompactor) Status(context.Context, compact.CompactStatusRequest) (compact.CompactStatus, error) {
	return compact.CompactStatus{
		Enabled: true, OperationState: "idle", InputBudget: 1000, HighLimit: 800,
		Warnings: []string{}, ProjectionVersion: compact.ProjectionVersion, PolicyVersion: "compact-v1", Profile: "default",
	}, nil
}

func (eventCompactor) Compact(_ context.Context, request compact.CompactRequest) (compact.CompactResult, error) {
	digest := "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	request.Observe(compact.RequestedEvent{SessionID: request.SessionID, RequestID: request.RequestID, Trigger: compact.TriggerManual})
	request.Observe(compact.StartedEvent{
		SessionID: request.SessionID, RequestID: request.RequestID, Trigger: compact.TriggerManual,
		FromSeq: 1, ToSeq: 2, GenerationMode: "full", SourceDigest: digest, Attempt: 1,
	})
	request.Observe(compact.CompletedEvent{
		SessionID: request.SessionID, RequestID: request.RequestID, Trigger: compact.TriggerManual,
		SummaryID: "summary-events", FromSeq: 1, ToSeq: 2, BeforeEstimatedTokens: 900,
		AfterEstimatedTokens: 400, SummaryBytes: 100, SourceDigest: digest, DurationMS: 2,
		ContextVersion: 5, StateChanged: true,
	})
	return compact.CompactResult{
		SummaryID: "summary-events", FromSeq: 1, ToSeq: 2, BeforeEstimatedTokens: 900, AfterEstimatedTokens: 400,
		RetainedFromSeq: 3, RetainedToSeq: 4, GenerationMode: "full", ContextVersion: 5,
		TargetResult: compact.TokenTargetResult{TargetTokens: 600, TargetMet: true},
	}, nil
}
