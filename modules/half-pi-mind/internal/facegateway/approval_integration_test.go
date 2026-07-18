package facegateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/dispatcher"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
)

func TestEncryptedFaceApprovalAuthorizesBoundRemoteRPC(t *testing.T) {
	const (
		conversationID = "conv-remote-approval"
		rawCommand     = "rm temporary-file.txt"
	)
	provider := llm.NewScriptedProvider(
		llm.ScriptedStep{Response: llm.LLMResponse{ToolCalls: []llm.ToolCall{{
			ID: "use-hand-approved", Name: "use_hand",
			Args: `{"tool":"exec_command","args":{"command":"` + rawCommand + `"},"hand_id":"approval-hand","timeout_ms":30000}`,
		}}}},
		llm.ScriptedStep{Response: llm.LLMResponse{Content: "remote work completed"}},
	)
	fixture := newGatewayFixtureWithProvider(t, 64, provider)
	createConversation(t, fixture, conversationID)
	faceCredential, err := fixture.store.AddFaceToken("approval-face", []protocol.FaceScope{
		protocol.FaceScopeChat, protocol.FaceScopeApprove,
	})
	if err != nil {
		t.Fatal(err)
	}
	faceIdentity, err := fixture.store.FaceIdentityByLabel(faceCredential.Label)
	if err != nil || faceIdentity == nil {
		t.Fatalf("Face identity = %+v, %v", faceIdentity, err)
	}
	handCredential, err := fixture.store.AddHandCredential("approval-hand")
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

	if err := face.SendPayload("subscribe-approval", protocol.TypeFaceSubscribe, protocol.FaceSubscribe{
		RequestID: "subscribe-approval", ConversationIDs: []string{conversationID},
		EventTypes: []protocol.FaceEventType{protocol.FaceEventApprovalRequested, protocol.FaceEventApprovalResolved},
	}); err != nil {
		t.Fatal(err)
	}
	if _, accepted, err := wss.ReadPayload[protocol.FaceAccepted](face); err != nil || accepted.Operation != protocol.FaceOperationSubscribe {
		t.Fatalf("subscribe accepted = %+v, %v", accepted, err)
	}
	chat := protocol.FaceChat{RequestID: "chat-approval", ConversationID: conversationID, Content: "run approved work"}
	if err := face.SendPayload("chat-approval", protocol.TypeFaceChat, chat); err != nil {
		t.Fatal(err)
	}
	if _, accepted, err := wss.ReadPayload[protocol.FaceAccepted](face); err != nil || accepted.Operation != protocol.FaceOperationChat {
		t.Fatalf("Chat accepted = %+v, %v", accepted, err)
	}

	_, requestedEvent, err := wss.ReadPayload[protocol.FaceEvent](face)
	if err != nil || requestedEvent.Type != protocol.FaceEventApprovalRequested {
		t.Fatalf("approval requested event = %+v, %v", requestedEvent, err)
	}
	requested, err := protocol.StrictDecode[protocol.ApprovalRequestedEventData](requestedEvent.Data)
	if err != nil || requested.ConversationID != conversationID || requested.RequestID != chat.RequestID ||
		requested.RunID == "" || requested.Tool != "exec_command" || strings.Contains(string(requestedEvent.Data), rawCommand) {
		t.Fatalf("approval request = %+v, %v", requested, err)
	}
	resolve := protocol.FaceApprovalResolve{
		RequestID: "resolve-approval", ApprovalID: requested.ApprovalID,
		Decision: protocol.FaceApprovalAllowOnce, Reason: "approved over encrypted Face session",
	}
	if err := face.SendPayload("resolve-approval", protocol.TypeFaceApprovalResolve, resolve); err != nil {
		t.Fatal(err)
	}
	if _, accepted, err := wss.ReadPayload[protocol.FaceAccepted](face); err != nil || accepted.Operation != protocol.FaceOperationApprovalResolve {
		t.Fatalf("approval accepted = %+v, %v", accepted, err)
	}
	_, resolvedEvent, err := wss.ReadPayload[protocol.FaceEvent](face)
	if err != nil || resolvedEvent.Type != protocol.FaceEventApprovalResolved {
		t.Fatalf("approval resolved event = %+v, %v", resolvedEvent, err)
	}
	if _, result, err := wss.ReadPayload[protocol.FaceResult](face); err != nil || result.RequestID != resolve.RequestID || result.Status != protocol.FaceResultSucceeded {
		t.Fatalf("approval result = %+v, %v", result, err)
	}

	rpcEnvelope, err := hand.Read()
	if err != nil || rpcEnvelope.Type != protocol.TypeRPC {
		t.Fatalf("remote RPC = %q, %v", rpcEnvelope.Type, err)
	}
	rpc, err := protocol.DecodePayload[protocol.RPC](&rpcEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if rpc.RunID != requested.RunID || rpc.Approval == nil || rpc.Approval.Source != "face" ||
		rpc.Approval.Approver != faceIdentity.ID || rpc.Approval.ArgsDigest != requested.ArgsDigest {
		t.Fatalf("bound RPC approval = %+v", rpc.Approval)
	}
	if err := protocol.ValidateRPCApproval(rpc.Approval, rpc, handCredential.Label, time.Now()); err != nil {
		t.Fatalf("valid remote Approval rejected: %v", err)
	}
	tampered := rpc
	tampered.Args = map[string]any{"command": "rm different-file.txt"}
	if err := protocol.ValidateRPCApproval(tampered.Approval, tampered, handCredential.Label, time.Now()); !errors.Is(err, protocol.ErrApprovalDigestMismatch) {
		t.Fatalf("tampered RPC approval error = %v", err)
	}

	if err := hand.SendPayload("accepted-approved", protocol.TypeRPCAccepted, protocol.RPCAccepted{
		RunID: rpc.RunID, StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := hand.SendPayload("result-approved", protocol.TypeRPCResult, protocol.RPCResult{
		RunID: rpc.RunID, Success: true, Output: "done",
	}); err != nil {
		t.Fatal(err)
	}
	if _, result, err := wss.ReadPayload[protocol.FaceResult](face); err != nil || result.RequestID != chat.RequestID || result.Status != protocol.FaceResultSucceeded {
		t.Fatalf("Chat result = %+v, %v", result, err)
	}
	audit, found, err := fixture.store.LookupApproval(requested.ApprovalID)
	if err != nil || !found || audit.Status != approval.StatusResolved || audit.Actor.ID != faceIdentity.ID ||
		audit.Actor.Label != faceCredential.Label || audit.Decision != resolve.Decision {
		t.Fatalf("persisted approval = %+v, %t, %v", audit, found, err)
	}
}
