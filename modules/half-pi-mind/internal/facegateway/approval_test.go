package facegateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
)

func waitPendingApproval(t *testing.T, broker *approval.Broker, conversationID string) protocol.ApprovalRequest {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		pending, err := broker.Pending(conversationID)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 1 {
			return pending[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("approval for %q did not become pending", conversationID)
		}
		time.Sleep(time.Millisecond)
	}
}

func nextEnvelope(t *testing.T, state *connection) protocol.Envelope {
	t.Helper()
	select {
	case env := <-state.queue:
		return env
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Face response")
		return protocol.Envelope{}
	}
}

func TestApprovalLifecycleProjectsPendingAuditAndOrderedResolution(t *testing.T) {
	t.Skip("legacy direct Core preflight; lifecycle approval is covered by ToolRuntime integration tests")
	fixture := newGatewayFixture(t, 32)
	const conversationID = "conv-approval"
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, "Approval"); err != nil {
		t.Fatal(err)
	}
	identity := protocol.FaceIdentity{
		ID: "face-1", Label: "operator",
		Scopes: []protocol.FaceScope{protocol.FaceScopeApprove, protocol.FaceScopeSessionsRead},
	}
	state := newGatewayTestConnection(32, identity)
	fixture.gateway.mu.Lock()
	fixture.gateway.connections[state.peer] = state
	fixture.gateway.mu.Unlock()
	fixture.gateway.handleSubscribe(state, identity, commandEnvelope(t, protocol.TypeFaceSubscribe, protocol.FaceSubscribe{
		RequestID: "subscribe-approval", ConversationIDs: []string{conversationID},
		EventTypes: []protocol.FaceEventType{
			protocol.FaceEventApprovalRequested, protocol.FaceEventApprovalResolved, protocol.FaceEventConversationChanged,
		},
	}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	const rawSecret = "top-secret-tool-argument"
	args := json.RawMessage(`{"command":"` + rawSecret + `"}`)
	digest := approval.ArgsDigest(args)
	checks := make(chan approval.Resolution, 1)
	go func() {
		checks <- fixture.approvals.Confirm(ctx, approval.Request{ConversationID: conversationID, RunID: "run-approval", Tool: "remote_approval_tool", ArgsDigest: digest})
	}()

	requestedEvent := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	changedEvent := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	if requestedEvent.Type != protocol.FaceEventApprovalRequested || changedEvent.Type != protocol.FaceEventConversationChanged {
		t.Fatalf("request events = %+v then %+v", requestedEvent, changedEvent)
	}
	requested, err := protocol.StrictDecode[protocol.ApprovalRequestedEventData](requestedEvent.Data)
	if err != nil {
		t.Fatal(err)
	}
	if requested.ConversationID != conversationID || requested.RequestID != "" || requested.RunID != "run-approval" ||
		requested.Tool != "remote_approval_tool" || requested.ArgsDigest != digest || strings.Contains(string(requestedEvent.Data), rawSecret) {
		t.Fatalf("approval request = %+v", requested)
	}
	snapshot, err := fixture.gateway.snapshot(identity, conversationID)
	if err != nil || len(snapshot.PendingApprovals) != 1 || snapshot.PendingApprovals[0].ApprovalID != requested.ApprovalID {
		t.Fatalf("pending snapshot = %+v, %v", snapshot.PendingApprovals, err)
	}

	resolve := protocol.FaceApprovalResolve{
		RequestID: "resolve-approval", ApprovalID: requested.ApprovalID,
		Decision: protocol.FaceApprovalAllowOnce, Reason: "approved remotely",
	}
	fixture.gateway.handleApprovalResolve(state, identity, commandEnvelope(t, protocol.TypeFaceApprovalResolve, resolve))
	accepted := nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	resolvedEvent := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	changedEvent = nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if accepted.Operation != protocol.FaceOperationApprovalResolve || accepted.ConversationID != conversationID ||
		resolvedEvent.Type != protocol.FaceEventApprovalResolved || changedEvent.Type != protocol.FaceEventConversationChanged ||
		result.Status != protocol.FaceResultSucceeded {
		t.Fatalf("resolution lifecycle = %+v, %+v, %+v, %+v", accepted, resolvedEvent, changedEvent, result)
	}
	resolvedData, err := protocol.StrictDecode[protocol.ApprovalResolvedEventData](resolvedEvent.Data)
	if err != nil || resolvedData.ApprovalID != requested.ApprovalID || resolvedData.Actor != identity.ID || resolvedData.Decision != resolve.Decision {
		t.Fatalf("resolved event = %+v, %v", resolvedData, err)
	}
	select {
	case check := <-checks:
		if check.Actor.ID != identity.ID || !check.Allowed() {
			t.Fatalf("approval check = %+v", check)
		}
	case <-time.After(time.Second):
		t.Fatal("tool check did not resume after approval")
	}
	audit, found, err := fixture.store.LookupApproval(requested.ApprovalID)
	if err != nil || !found || audit.Status != approval.StatusResolved || audit.Actor.ID != identity.ID ||
		audit.Actor.Label != identity.Label || audit.Actor.Source != "face" || audit.Decision != resolve.Decision {
		t.Fatalf("approval audit = %+v, %t, %v", audit, found, err)
	}
	snapshot, err = fixture.gateway.snapshot(identity, conversationID)
	if err != nil || len(snapshot.PendingApprovals) != 0 {
		t.Fatalf("resolved pending snapshot = %+v, %v", snapshot.PendingApprovals, err)
	}

	fixture.gateway.handleApprovalResolve(state, identity, commandEnvelope(t, protocol.TypeFaceApprovalResolve, resolve))
	faceErr := nextPayload[protocol.FaceError](t, state, protocol.TypeFaceError)
	if faceErr.Code != protocol.FaceErrorRequestConflict {
		t.Fatalf("duplicate resolution error = %+v", faceErr)
	}
}

func TestApprovalResolutionRejectsScopeExpiryAndForeignConversation(t *testing.T) {
	fixture := newGatewayFixture(t, 16)
	const conversationID = "conv-approval-errors"
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, "Errors"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan approval.Resolution, 1)
	go func() {
		results <- fixture.approvals.Confirm(ctx, approval.Request{
			ConversationID: conversationID, Tool: "write_file", Reason: "write", ArgsDigest: "sha256:scope",
		})
	}()
	pending := waitPendingApproval(t, fixture.approvals, conversationID)
	deniedIdentity := protocol.FaceIdentity{ID: "face-denied", Label: "denied"}
	denied := newGatewayTestConnection(4, deniedIdentity)
	fixture.gateway.handleApprovalResolve(denied, deniedIdentity, commandEnvelope(t, protocol.TypeFaceApprovalResolve, protocol.FaceApprovalResolve{
		RequestID: "resolve-denied", ApprovalID: pending.ApprovalID, Decision: protocol.FaceApprovalAllowOnce,
	}))
	if faceErr := nextPayload[protocol.FaceError](t, denied, protocol.TypeFaceError); faceErr.Code != protocol.FaceErrorForbidden {
		t.Fatalf("scope error = %+v", faceErr)
	}
	cancel()
	select {
	case resolution := <-results:
		if resolution.Decision != "" {
			t.Fatalf("scope-denied approval resolved as %+v", resolution)
		}
	case <-time.After(time.Second):
		t.Fatal("scope-denied approval did not cancel")
	}

	current := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	expiring, err := approval.New(approval.Config{
		Auditor: fixture.store, TTL: time.Minute, Now: func() time.Time { return current },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = expiring.Close() })
	expiring.Subscribe(fixture.gateway.PublishApprovalRequested, fixture.gateway.PublishApprovalFinished)
	fixture.gateway.approvals = expiring
	expiredResults := make(chan approval.Resolution, 1)
	go func() {
		expiredResults <- expiring.Confirm(context.Background(), approval.Request{
			ConversationID: conversationID, Tool: "write_file", Reason: "write", ArgsDigest: "sha256:expired",
		})
	}()
	expired := waitPendingApproval(t, expiring, conversationID)
	current = expired.ExpiresAt.Add(time.Nanosecond)
	allowedIdentity := protocol.FaceIdentity{ID: "face-allowed", Label: "allowed", Scopes: []protocol.FaceScope{protocol.FaceScopeApprove}}
	allowed := newGatewayTestConnection(8, allowedIdentity)
	fixture.gateway.handleApprovalResolve(allowed, allowedIdentity, commandEnvelope(t, protocol.TypeFaceApprovalResolve, protocol.FaceApprovalResolve{
		RequestID: "resolve-expired", ApprovalID: expired.ApprovalID, Decision: protocol.FaceApprovalAllowOnce,
	}))
	if faceErr := nextPayload[protocol.FaceError](t, allowed, protocol.TypeFaceError); faceErr.Code != protocol.FaceErrorApprovalExpired {
		t.Fatalf("expiry error = %+v", faceErr)
	}
	select {
	case resolution := <-expiredResults:
		if resolution.Decision != "" {
			t.Fatalf("expired approval resolved as %+v", resolution)
		}
	case <-time.After(time.Second):
		t.Fatal("expired approval did not terminate")
	}

	foreignCtx, foreignCancel := context.WithCancel(context.Background())
	defer foreignCancel()
	foreignResults := make(chan approval.Resolution, 1)
	go func() {
		foreignResults <- expiring.Confirm(foreignCtx, approval.Request{
			ConversationID: "foreign-conversation", Tool: "write_file", Reason: "write", ArgsDigest: "sha256:foreign",
		})
	}()
	foreign := waitPendingApproval(t, expiring, "foreign-conversation")
	fixture.gateway.handleApprovalResolve(allowed, allowedIdentity, commandEnvelope(t, protocol.TypeFaceApprovalResolve, protocol.FaceApprovalResolve{
		RequestID: "resolve-foreign", ApprovalID: foreign.ApprovalID, Decision: protocol.FaceApprovalAllowOnce,
	}))
	if faceErr := nextPayload[protocol.FaceError](t, allowed, protocol.TypeFaceError); faceErr.Code != protocol.FaceErrorApprovalNotFound {
		t.Fatalf("foreign approval error = %+v", faceErr)
	}
	foreignCancel()
	select {
	case <-foreignResults:
	case <-time.After(time.Second):
		t.Fatal("foreign approval did not cancel")
	}
}

func TestConcurrentFacesOnlyOneResolutionIsAccepted(t *testing.T) {
	fixture := newGatewayFixture(t, 8)
	const conversationID = "conv-approval-race"
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, "Race"); err != nil {
		t.Fatal(err)
	}
	resolutions := make(chan approval.Resolution, 1)
	go func() {
		resolutions <- fixture.approvals.Confirm(context.Background(), approval.Request{
			ConversationID: conversationID, Tool: "write_file", Reason: "write", ArgsDigest: "sha256:race",
		})
	}()
	pending := waitPendingApproval(t, fixture.approvals, conversationID)
	states := []*connection{
		newGatewayTestConnection(4, protocol.FaceIdentity{ID: "face-1", Label: "one", Scopes: []protocol.FaceScope{protocol.FaceScopeApprove}}),
		newGatewayTestConnection(4, protocol.FaceIdentity{ID: "face-2", Label: "two", Scopes: []protocol.FaceScope{protocol.FaceScopeApprove}}),
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i, state := range states {
		wait.Add(1)
		go func(index int, state *connection) {
			defer wait.Done()
			<-start
			fixture.gateway.handleApprovalResolve(state, state.identity, commandEnvelope(t, protocol.TypeFaceApprovalResolve, protocol.FaceApprovalResolve{
				RequestID: "resolve-race-" + string(rune('1'+index)), ApprovalID: pending.ApprovalID,
				Decision: protocol.FaceApprovalAllowOnce,
			}))
		}(i, state)
	}
	close(start)
	wait.Wait()
	var accepted, conflicted int
	for _, state := range states {
		first := nextEnvelope(t, state)
		switch first.Type {
		case protocol.TypeFaceAccepted:
			accepted++
			if result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult); result.Status != protocol.FaceResultSucceeded {
				t.Fatalf("winner result = %+v", result)
			}
		case protocol.TypeFaceError:
			conflicted++
			faceErr, err := protocol.DecodePayload[protocol.FaceError](&first)
			if err != nil || faceErr.Code != protocol.FaceErrorRequestConflict {
				t.Fatalf("loser error = %+v, %v", faceErr, err)
			}
		default:
			t.Fatalf("unexpected first response %q", first.Type)
		}
	}
	if accepted != 1 || conflicted != 1 {
		t.Fatalf("accepted=%d conflicted=%d", accepted, conflicted)
	}
	select {
	case resolution := <-resolutions:
		if !resolution.Allowed() {
			t.Fatalf("race resolution = %+v", resolution)
		}
	case <-time.After(time.Second):
		t.Fatal("approval race did not resolve")
	}
}
