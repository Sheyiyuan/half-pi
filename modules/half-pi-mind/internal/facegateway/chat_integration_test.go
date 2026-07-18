package facegateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/dispatcher"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
)

func TestEncryptedFaceChatCancelPropagatesToRemoteRun(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{Response: llm.LLMResponse{
		ToolCalls: []llm.ToolCall{{
			ID: "use-hand-1", Name: "use_hand",
			Args: `{"tool":"exec_command","args":{"command":"sleep 30"},"hand_id":"chat-hand","timeout_ms":30000}`,
		}},
	}})
	fixture := newGatewayFixtureWithProvider(t, 64, provider)
	createConversation(t, fixture, "conv-remote-cancel")
	faceCredential, err := fixture.store.AddFaceToken("chat-face-wire", []protocol.FaceScope{protocol.FaceScopeChat})
	if err != nil {
		t.Fatal(err)
	}
	handCredential, err := fixture.store.AddHandCredential("chat-hand")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.Install(fixture.hub, fixture.store, fixture.authority, fixture.gateway)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, upgradeErr := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if upgradeErr == nil {
			_ = fixture.hub.ServeWS(conn)
		}
	}))
	t.Cleanup(server.Close)
	serverURL := "ws" + strings.TrimPrefix(server.URL, "http")

	hand, err := wss.NewClient(serverURL).ConnectAndRegister(wss.Credentials{
		Label: handCredential.Label, Type: protocol.PeerHand,
		Token: handCredential.Token, ApplicationKey: handCredential.ApplicationKey,
		Info: &protocol.HandInfo{Hostname: "dev", OS: "linux", Arch: "amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer hand.Conn.Close()
	face, err := wss.NewClient(serverURL).ConnectAndRegister(wss.Credentials{
		Label: faceCredential.Label, Type: protocol.PeerFace,
		Token: faceCredential.Token, ApplicationKey: faceCredential.ApplicationKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer face.Conn.Close()
	_ = face.Conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_ = hand.Conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	chat := protocol.FaceChat{
		RequestID: "face-chat-remote", ConversationID: "conv-remote-cancel", Content: "run remotely",
	}
	if err := face.SendPayload("chat-wire", protocol.TypeFaceChat, chat); err != nil {
		t.Fatal(err)
	}
	_, accepted, err := wss.ReadPayload[protocol.FaceAccepted](face)
	if err != nil || accepted.Operation != protocol.FaceOperationChat {
		t.Fatalf("Chat accepted = %+v, %v", accepted, err)
	}
	rpcEnvelope, err := hand.Read()
	if err != nil || rpcEnvelope.Type != protocol.TypeRPC {
		t.Fatalf("remote RPC = %q, %v", rpcEnvelope.Type, err)
	}
	rpc, err := protocol.DecodePayload[protocol.RPC](&rpcEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := hand.SendPayload("accepted-wire", protocol.TypeRPCAccepted, protocol.RPCAccepted{
		RunID: rpc.RunID, StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, fixture, rpc.RunID, protocol.RunRunning)

	cancel := protocol.FaceChatCancel{
		RequestID: "cancel-wire", TargetRequestID: chat.RequestID,
		ConversationID: chat.ConversationID, Reason: "user",
	}
	if err := face.SendPayload("cancel-wire", protocol.TypeFaceChatCancel, cancel); err != nil {
		t.Fatal(err)
	}
	_, cancelAccepted, err := wss.ReadPayload[protocol.FaceAccepted](face)
	if err != nil || cancelAccepted.Operation != protocol.FaceOperationChatCancel {
		t.Fatalf("cancel accepted = %+v, %v", cancelAccepted, err)
	}
	_, cancelResult, err := wss.ReadPayload[protocol.FaceResult](face)
	if err != nil || cancelResult.RequestID != cancel.RequestID || cancelResult.Status != protocol.FaceResultSucceeded {
		t.Fatalf("cancel result = %+v, %v", cancelResult, err)
	}
	cancelEnvelope, err := hand.Read()
	if err != nil || cancelEnvelope.Type != protocol.TypeRPCCancel {
		t.Fatalf("remote cancel = %q, %v", cancelEnvelope.Type, err)
	}
	remoteCancel, err := protocol.DecodePayload[protocol.RPCCancel](&cancelEnvelope)
	if err != nil || remoteCancel.RunID != rpc.RunID || remoteCancel.Reason != "user" {
		t.Fatalf("remote cancel payload = %+v, %v", remoteCancel, err)
	}
	if err := hand.SendPayload("cancel-result-wire", protocol.TypeRPCCancelResult, protocol.RPCCancelResult{
		RunID: rpc.RunID, Status: protocol.CancelCancelled,
	}); err != nil {
		t.Fatal(err)
	}
	_, chatResult, err := wss.ReadPayload[protocol.FaceResult](face)
	if err != nil || chatResult.RequestID != chat.RequestID || chatResult.Status != protocol.FaceResultCancelled {
		t.Fatalf("cancelled Chat result = %+v, %v", chatResult, err)
	}
	run, err := fixture.store.GetRemoteRun(rpc.RunID)
	if err != nil || run.RequestID != chat.RequestID || run.Status != protocol.RunCancelled {
		t.Fatalf("persisted run association = %+v, %v", run, err)
	}
}

func waitForRunStatus(t *testing.T, fixture *gatewayFixture, runID string, status protocol.RunStatus) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		run, ok := fixture.authority.Registry.Snapshot(runID)
		if ok && run.Status == status {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %q did not reach %s", runID, status)
		}
		time.Sleep(time.Millisecond)
	}
}
