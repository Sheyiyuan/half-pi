package facegateway

import (
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

func TestRunCancelChecksScopeOwnershipAndUsesAuthority(t *testing.T) {
	fixture := newGatewayFixture(t, 8)
	for _, conversationID := range []string{"conv-run", "conv-other"} {
		if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, conversationID); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.authority.Registry.Create("run-cancel", "conv-run", "offline-hand", "exec_command"); err != nil {
		t.Fatal(err)
	}

	request := protocol.FaceRunCancel{RequestID: "cancel-run", ConversationID: "conv-run", RunID: "run-cancel"}
	deniedIdentity := protocol.FaceIdentity{ID: "face-denied", Label: "denied"}
	denied := newGatewayTestConnection(4, deniedIdentity)
	fixture.gateway.handleRunCancel(denied, deniedIdentity, commandEnvelope(t, protocol.TypeFaceRunCancel, request))
	if faceErr := nextPayload[protocol.FaceError](t, denied, protocol.TypeFaceError); faceErr.Code != protocol.FaceErrorForbidden {
		t.Fatalf("run scope error = %+v", faceErr)
	}

	identity := protocol.FaceIdentity{ID: "face-cancel", Label: "cancel", Scopes: []protocol.FaceScope{protocol.FaceScopeRunsCancel}}
	wrong := newGatewayTestConnection(4, identity)
	wrongRequest := request
	wrongRequest.ConversationID = "conv-other"
	fixture.gateway.handleRunCancel(wrong, identity, commandEnvelope(t, protocol.TypeFaceRunCancel, wrongRequest))
	if faceErr := nextPayload[protocol.FaceError](t, wrong, protocol.TypeFaceError); faceErr.Code != protocol.FaceErrorRunNotFound {
		t.Fatalf("run ownership error = %+v", faceErr)
	}

	offline := newGatewayTestConnection(4, identity)
	fixture.gateway.handleRunCancel(offline, identity, commandEnvelope(t, protocol.TypeFaceRunCancel, request))
	if accepted := nextPayload[protocol.FaceAccepted](t, offline, protocol.TypeFaceAccepted); accepted.Operation != protocol.FaceOperationRunCancel {
		t.Fatalf("run cancel accepted = %+v", accepted)
	}
	result := nextPayload[protocol.FaceResult](t, offline, protocol.TypeFaceResult)
	if result.Status != protocol.FaceResultFailed || result.ErrorCode != protocol.FaceErrorHandOffline {
		t.Fatalf("offline run cancellation = %+v", result)
	}
	run, _ := fixture.authority.Registry.Snapshot("run-cancel")
	if run.Status != protocol.RunCreated {
		t.Fatalf("offline Authority changed run to %s", run.Status)
	}
}

func TestTaskCancelRequiresReadAndCancelScopes(t *testing.T) {
	fixture := newGatewayFixture(t, 8)
	const conversationID = "conv-task-cancel"
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, "Task"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.tasks.CreateStartSnapshot(remoteexec.Task{
		TaskID: "task-cancel", SessionID: conversationID, HandID: "offline-hand",
		Tool: "exec_command", ArgsDigest: "sha256:task",
	}); err != nil {
		t.Fatal(err)
	}
	request := protocol.FaceTaskCancel{RequestID: "cancel-task", ConversationID: conversationID, TaskID: "task-cancel"}
	for _, identity := range []protocol.FaceIdentity{
		{ID: "read-only", Label: "read", Scopes: []protocol.FaceScope{protocol.FaceScopeTasksRead}},
		{ID: "cancel-only", Label: "cancel", Scopes: []protocol.FaceScope{protocol.FaceScopeTasksCancel}},
	} {
		state := newGatewayTestConnection(4, identity)
		fixture.gateway.handleTaskCancel(state, identity, commandEnvelope(t, protocol.TypeFaceTaskCancel, request))
		if faceErr := nextPayload[protocol.FaceError](t, state, protocol.TypeFaceError); faceErr.Code != protocol.FaceErrorForbidden {
			t.Fatalf("task scope error for %s = %+v", identity.ID, faceErr)
		}
	}

	identity := protocol.FaceIdentity{
		ID: "task-operator", Label: "operator",
		Scopes: []protocol.FaceScope{protocol.FaceScopeTasksRead, protocol.FaceScopeTasksCancel},
	}
	state := newGatewayTestConnection(4, identity)
	fixture.gateway.handleTaskCancel(state, identity, commandEnvelope(t, protocol.TypeFaceTaskCancel, request))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if result.Status != protocol.FaceResultFailed || result.ErrorCode != protocol.FaceErrorHandOffline {
		t.Fatalf("offline task cancellation = %+v", result)
	}
}

func TestRemoteRunChangedProjectsEveryStatusTransition(t *testing.T) {
	fixture := newGatewayFixture(t, 32)
	const conversationID = "conv-run-events"
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, "Runs"); err != nil {
		t.Fatal(err)
	}
	identity := protocol.FaceIdentity{
		ID: "run-reader", Label: "reader",
		Scopes: []protocol.FaceScope{protocol.FaceScopeRunsRead},
	}
	state := newGatewayTestConnection(32, identity)
	fixture.gateway.mu.Lock()
	fixture.gateway.connections[state.peer] = state
	fixture.gateway.mu.Unlock()
	fixture.gateway.handleSubscribe(state, identity, commandEnvelope(t, protocol.TypeFaceSubscribe, protocol.FaceSubscribe{
		RequestID: "subscribe-runs", ConversationIDs: []string{conversationID},
		EventTypes: []protocol.FaceEventType{protocol.FaceEventRemoteRunChanged},
	}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)

	if err := fixture.authority.Registry.CreateWithMetadata(
		"run-events", conversationID, "hand-events", "read_file", remoteexec.AuditMetadata{RequestID: "request-events"},
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authority.Registry.Transition("run-events", protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authority.Registry.Transition("run-events", protocol.RunSent); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authority.Registry.ApplyAccepted("hand-events", protocol.RPCAccepted{
		RunID: "run-events", StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authority.Registry.ApplyResult("hand-events", protocol.RPCResult{RunID: "run-events", Success: true}); err != nil {
		t.Fatal(err)
	}
	want := []protocol.RunStatus{
		protocol.RunCreated, protocol.RunApproved, protocol.RunSent,
		protocol.RunAccepted, protocol.RunRunning, protocol.RunSucceeded,
	}
	for sequence, status := range want {
		event := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
		data, err := protocol.StrictDecode[protocol.RemoteRunChangedEventData](event.Data)
		if err != nil || event.EventSeq != int64(sequence+1) || event.RequestID != "request-events" || data.Status != status {
			t.Fatalf("run event %d = %+v data=%+v err=%v", sequence, event, data, err)
		}
	}
}
