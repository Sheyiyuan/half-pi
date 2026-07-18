package facegateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/dispatcher"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

func startGatewayWireServer(t *testing.T, fixture *gatewayFixture) string {
	t.Helper()
	dispatcher.Install(fixture.hub, fixture.store, fixture.authority, fixture.gateway)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err == nil {
			_ = fixture.hub.ServeWS(conn)
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func connectWireHand(t *testing.T, serverURL string, credentialLabel, token, applicationKey string) *wss.SessionConn {
	t.Helper()
	hand, err := wss.NewClient(serverURL).ConnectAndRegister(wss.Credentials{
		Label: credentialLabel, Type: protocol.PeerHand, Token: token, ApplicationKey: applicationKey,
		Info: &protocol.HandInfo{Hostname: "dev", OS: "linux", Arch: "amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hand.Conn.Close() })
	_ = hand.Conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	return hand
}

func connectWireFace(t *testing.T, serverURL string, credentialLabel, token, applicationKey string) *wss.SessionConn {
	t.Helper()
	face, err := wss.NewClient(serverURL).ConnectAndRegister(wss.Credentials{
		Label: credentialLabel, Type: protocol.PeerFace, Token: token, ApplicationKey: applicationKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = face.Conn.Close() })
	_ = face.Conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	return face
}

func TestEncryptedFaceRunCancelUsesAuthorityAndPersistsTerminal(t *testing.T) {
	fixture := newGatewayFixture(t, 32)
	const conversationID = "conv-wire-run-cancel"
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, "Run cancel"); err != nil {
		t.Fatal(err)
	}
	faceCredential, err := fixture.store.AddFaceToken("run-cancel-face", []protocol.FaceScope{protocol.FaceScopeRunsCancel})
	if err != nil {
		t.Fatal(err)
	}
	handCredential, err := fixture.store.AddHandCredential("run-cancel-hand")
	if err != nil {
		t.Fatal(err)
	}
	serverURL := startGatewayWireServer(t, fixture)
	hand := connectWireHand(t, serverURL, handCredential.Label, handCredential.Token, handCredential.ApplicationKey)
	face := connectWireFace(t, serverURL, faceCredential.Label, faceCredential.Token, faceCredential.ApplicationKey)
	peer := fixture.hub.PeerByType(hub.PeerHand, handCredential.Label)
	if peer == nil {
		t.Fatal("Hand peer is not online")
	}
	if err := fixture.authority.Registry.CreateForPeer(
		"run-wire-cancel", conversationID, peer.ID, peer.SessionID(), "exec_command",
		remoteexec.AuditMetadata{RequestID: "originating-chat"},
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authority.Registry.Transition("run-wire-cancel", protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authority.Registry.Transition("run-wire-cancel", protocol.RunSent); err != nil {
		t.Fatal(err)
	}
	if err := fixture.authority.Registry.ApplyAcceptedFrom(peer.ID, peer.SessionID(), protocol.RPCAccepted{
		RunID: "run-wire-cancel", StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	request := protocol.FaceRunCancel{
		RequestID: "face-run-cancel", ConversationID: conversationID, RunID: "run-wire-cancel", Reason: "user",
	}
	if err := face.SendPayload("face-run-cancel", protocol.TypeFaceRunCancel, request); err != nil {
		t.Fatal(err)
	}
	if _, accepted, err := wss.ReadPayload[protocol.FaceAccepted](face); err != nil || accepted.Operation != protocol.FaceOperationRunCancel {
		t.Fatalf("run cancel accepted = %+v, %v", accepted, err)
	}
	cancelEnvelope, err := hand.Read()
	if err != nil || cancelEnvelope.Type != protocol.TypeRPCCancel {
		t.Fatalf("run cancel RPC = %q, %v", cancelEnvelope.Type, err)
	}
	cancelRequest, err := protocol.DecodePayload[protocol.RPCCancel](&cancelEnvelope)
	if err != nil || cancelRequest.RunID != request.RunID || cancelRequest.Reason != request.Reason {
		t.Fatalf("run cancel payload = %+v, %v", cancelRequest, err)
	}
	if err := hand.SendPayload("run-cancel-result", protocol.TypeRPCCancelResult, protocol.RPCCancelResult{
		RunID: request.RunID, Status: protocol.CancelCancelled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, result, err := wss.ReadPayload[protocol.FaceResult](face); err != nil || result.RequestID != request.RequestID || result.Status != protocol.FaceResultSucceeded {
		t.Fatalf("run cancel result = %+v, %v", result, err)
	}
	run, err := fixture.store.GetRemoteRun(request.RunID)
	if err != nil || run.Status != protocol.RunCancelled || run.SessionID != conversationID {
		t.Fatalf("persisted cancelled run = %+v, %v", run, err)
	}
}

func TestEncryptedFaceTaskCancelReconcilesStructuredTerminal(t *testing.T) {
	fixture := newGatewayFixture(t, 32)
	const conversationID = "conv-wire-task-cancel"
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, "Task cancel"); err != nil {
		t.Fatal(err)
	}
	faceCredential, err := fixture.store.AddFaceToken("task-cancel-face", []protocol.FaceScope{
		protocol.FaceScopeTasksRead, protocol.FaceScopeTasksCancel,
	})
	if err != nil {
		t.Fatal(err)
	}
	handCredential, err := fixture.store.AddHandCredential("task-cancel-hand")
	if err != nil {
		t.Fatal(err)
	}
	serverURL := startGatewayWireServer(t, fixture)
	hand := connectWireHand(t, serverURL, handCredential.Label, handCredential.Token, handCredential.ApplicationKey)
	face := connectWireFace(t, serverURL, faceCredential.Label, faceCredential.Token, faceCredential.ApplicationKey)
	if err := fixture.tasks.CreateStartSnapshot(remoteexec.Task{
		TaskID: "task-wire-cancel", SessionID: conversationID, HandID: handCredential.Label,
		Tool: "exec_command", ArgsDigest: "sha256:task-wire",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := fixture.store.GetRemoteTask("task-wire-cancel")
	if err != nil {
		t.Fatal(err)
	}

	request := protocol.FaceTaskCancel{
		RequestID: "face-task-cancel", ConversationID: conversationID, TaskID: task.TaskID, Reason: "user",
	}
	if err := face.SendPayload("face-task-cancel", protocol.TypeFaceTaskCancel, request); err != nil {
		t.Fatal(err)
	}
	if _, accepted, err := wss.ReadPayload[protocol.FaceAccepted](face); err != nil || accepted.Operation != protocol.FaceOperationTaskCancel {
		t.Fatalf("task cancel accepted = %+v, %v", accepted, err)
	}
	cancelEnvelope, err := hand.Read()
	if err != nil || cancelEnvelope.Type != protocol.TypeTaskCancel {
		t.Fatalf("task cancel RPC = %q, %v", cancelEnvelope.Type, err)
	}
	cancelRequest, err := protocol.DecodePayload[protocol.TaskCancel](&cancelEnvelope)
	if err != nil || cancelRequest.TaskID != request.TaskID || cancelRequest.Reason != request.Reason {
		t.Fatalf("task cancel payload = %+v, %v", cancelRequest, err)
	}
	if err := hand.SendPayload(cancelRequest.ID, protocol.TypeTaskCancelResult, protocol.TaskCancelResult{
		ID: cancelRequest.ID, TaskID: request.TaskID, Status: protocol.TaskCancelCancelled,
	}); err != nil {
		t.Fatal(err)
	}
	statusEnvelope, err := hand.Read()
	if err != nil || statusEnvelope.Type != protocol.TypeTaskStatusReq {
		t.Fatalf("task reconciliation request = %q, %v", statusEnvelope.Type, err)
	}
	statusRequest, err := protocol.DecodePayload[protocol.TaskStatusReq](&statusEnvelope)
	if err != nil || statusRequest.TaskID != request.TaskID {
		t.Fatalf("task status request = %+v, %v", statusRequest, err)
	}
	createdAt := task.CreatedAt.UnixMilli()
	startedAt := createdAt + 1
	finishedAt := startedAt + 1
	if err := hand.SendPayload(statusRequest.ID, protocol.TypeTaskStatusResp, protocol.TaskStatusResp{
		ID: statusRequest.ID, TaskID: request.TaskID, Tool: task.Tool, Status: protocol.TaskCancelled,
		CreatedAt: createdAt, StartedAt: startedAt, FinishedAt: finishedAt,
	}); err != nil {
		t.Fatal(err)
	}
	_, result, err := wss.ReadPayload[protocol.FaceResult](face)
	if err != nil || result.RequestID != request.RequestID || result.Status != protocol.FaceResultSucceeded {
		t.Fatalf("task cancel result = %+v, %v", result, err)
	}
	data, err := protocol.StrictDecode[protocol.FaceTaskCancelResult](result.Data)
	if err != nil || data.Outcome != string(protocol.TaskCancelCancelled) || data.Task.Status != protocol.TaskCancelled || data.Task.Stale {
		t.Fatalf("task cancel data = %+v, %v", data, err)
	}
	persisted, err := fixture.store.GetRemoteTask(request.TaskID)
	if err != nil || persisted.Status != protocol.TaskCancelled || persisted.Stale {
		t.Fatalf("persisted cancelled task = %+v, %v", persisted, err)
	}
}

func TestEncryptedFaceTaskCancelReportsHandFailure(t *testing.T) {
	fixture := newGatewayFixture(t, 32)
	const conversationID = "conv-wire-task-cancel-failed"
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, "Task cancel failed"); err != nil {
		t.Fatal(err)
	}
	faceCredential, err := fixture.store.AddFaceToken("task-cancel-failed-face", []protocol.FaceScope{
		protocol.FaceScopeTasksRead, protocol.FaceScopeTasksCancel,
	})
	if err != nil {
		t.Fatal(err)
	}
	handCredential, err := fixture.store.AddHandCredential("task-cancel-failed-hand")
	if err != nil {
		t.Fatal(err)
	}
	serverURL := startGatewayWireServer(t, fixture)
	hand := connectWireHand(t, serverURL, handCredential.Label, handCredential.Token, handCredential.ApplicationKey)
	face := connectWireFace(t, serverURL, faceCredential.Label, faceCredential.Token, faceCredential.ApplicationKey)
	if err := fixture.tasks.CreateStartSnapshot(remoteexec.Task{
		TaskID: "task-wire-cancel-failed", SessionID: conversationID, HandID: handCredential.Label,
		Tool: "exec_command", ArgsDigest: "sha256:task-wire-failed",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := fixture.store.GetRemoteTask("task-wire-cancel-failed")
	if err != nil {
		t.Fatal(err)
	}

	request := protocol.FaceTaskCancel{
		RequestID: "face-task-cancel-failed", ConversationID: conversationID, TaskID: task.TaskID, Reason: "user",
	}
	if err := face.SendPayload(request.RequestID, protocol.TypeFaceTaskCancel, request); err != nil {
		t.Fatal(err)
	}
	if _, accepted, err := wss.ReadPayload[protocol.FaceAccepted](face); err != nil || accepted.Operation != protocol.FaceOperationTaskCancel {
		t.Fatalf("task cancel accepted = %+v, %v", accepted, err)
	}
	cancelEnvelope, err := hand.Read()
	if err != nil || cancelEnvelope.Type != protocol.TypeTaskCancel {
		t.Fatalf("task cancel RPC = %q, %v", cancelEnvelope.Type, err)
	}
	cancelRequest, err := protocol.DecodePayload[protocol.TaskCancel](&cancelEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := hand.SendPayload(cancelRequest.ID, protocol.TypeTaskCancelResult, protocol.TaskCancelResult{
		ID: cancelRequest.ID, TaskID: request.TaskID, Status: protocol.TaskCancelFailed, Error: "permission denied",
	}); err != nil {
		t.Fatal(err)
	}
	statusEnvelope, err := hand.Read()
	if err != nil || statusEnvelope.Type != protocol.TypeTaskStatusReq {
		t.Fatalf("task reconciliation request = %q, %v", statusEnvelope.Type, err)
	}
	statusRequest, err := protocol.DecodePayload[protocol.TaskStatusReq](&statusEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := task.CreatedAt.UnixMilli()
	if err := hand.SendPayload(statusRequest.ID, protocol.TypeTaskStatusResp, protocol.TaskStatusResp{
		ID: statusRequest.ID, TaskID: request.TaskID, Tool: task.Tool, Status: protocol.TaskRunning,
		CreatedAt: createdAt, StartedAt: createdAt + 1,
	}); err != nil {
		t.Fatal(err)
	}
	_, result, err := wss.ReadPayload[protocol.FaceResult](face)
	if err != nil || result.RequestID != request.RequestID || result.Status != protocol.FaceResultFailed ||
		result.ErrorCode != protocol.FaceErrorTaskFailed || len(result.Data) != 0 {
		t.Fatalf("task cancel failure = %+v, %v", result, err)
	}
}
