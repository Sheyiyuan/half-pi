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
)

func TestEncryptedGatewayConversationAndHandQueries(t *testing.T) {
	fixture := newGatewayFixture(t, 32)
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), "conv-1", "Work"); err != nil {
		t.Fatal(err)
	}
	faceCredential, err := fixture.store.AddFaceToken("face-client", []protocol.FaceScope{
		protocol.FaceScopeSessionsRead, protocol.FaceScopeHandsRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondFaceCredential, err := fixture.store.AddFaceToken("face-client-2", []protocol.FaceScope{protocol.FaceScopeSessionsRead})
	if err != nil {
		t.Fatal(err)
	}
	handCredential, err := fixture.store.AddHandCredential("hand-client")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.Install(fixture.hub, fixture.store, fixture.authority, fixture.gateway)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, upgradeErr := (&websocket.Upgrader{}).Upgrade(w, request, nil)
		if upgradeErr == nil {
			_ = fixture.hub.ServeWS(conn)
		}
	}))
	t.Cleanup(server.Close)
	serverURL := "ws" + strings.TrimPrefix(server.URL, "http")
	face, err := wss.NewClient(serverURL).ConnectAndRegister(wss.Credentials{
		Label: faceCredential.Label, Type: protocol.PeerFace,
		Token: faceCredential.Token, ApplicationKey: faceCredential.ApplicationKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer face.Conn.Close()
	secondFace, err := wss.NewClient(serverURL).ConnectAndRegister(wss.Credentials{
		Label: secondFaceCredential.Label, Type: protocol.PeerFace,
		Token: secondFaceCredential.Token, ApplicationKey: secondFaceCredential.ApplicationKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer secondFace.Conn.Close()

	if err := face.SendPayload("subscribe-wire", protocol.TypeFaceSubscribe, protocol.FaceSubscribe{
		RequestID: "subscribe-wire", EventTypes: []protocol.FaceEventType{protocol.FaceEventHandConnected},
	}); err != nil {
		t.Fatal(err)
	}
	env, accepted, err := wss.ReadPayload[protocol.FaceAccepted](face)
	if err != nil || env.Type != protocol.TypeFaceAccepted || accepted.Operation != protocol.FaceOperationSubscribe {
		t.Fatalf("subscribe response = %q %+v, %v", env.Type, accepted, err)
	}

	hand, err := wss.NewClient(serverURL).ConnectAndRegister(wss.Credentials{
		Label: handCredential.Label, Type: protocol.PeerHand,
		Token: handCredential.Token, ApplicationKey: handCredential.ApplicationKey,
		Info: &protocol.HandInfo{Hostname: "dev", OS: "linux", Arch: "amd64", WorkDir: "/workspace"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer hand.Conn.Close()
	_, handEvent, err := wss.ReadPayload[protocol.FaceEvent](face)
	if err != nil || handEvent.EventSeq != 1 || handEvent.Type != protocol.FaceEventHandConnected {
		t.Fatalf("Hand connect event = %+v, %v", handEvent, err)
	}

	if err := face.SendPayload("list-wire", protocol.TypeFaceConversationList, protocol.FaceConversationList{RequestID: "list-wire"}); err != nil {
		t.Fatal(err)
	}
	_, accepted, err = wss.ReadPayload[protocol.FaceAccepted](face)
	if err != nil || accepted.Operation != protocol.FaceOperationConversationList {
		t.Fatalf("list accepted = %+v, %v", accepted, err)
	}
	_, result, err := wss.ReadPayload[protocol.FaceResult](face)
	if err != nil || result.Status != protocol.FaceResultSucceeded {
		t.Fatalf("list result = %+v, %v", result, err)
	}
	list, err := protocol.StrictDecode[protocol.ConversationListResult](result.Data)
	if err != nil || len(list.Conversations) != 1 || list.Conversations[0].ConversationID != "conv-1" {
		t.Fatalf("wire conversation list = %+v, %v", list, err)
	}
	if err := secondFace.SendPayload("list-wire-2", protocol.TypeFaceConversationList, protocol.FaceConversationList{RequestID: "list-wire-2"}); err != nil {
		t.Fatal(err)
	}
	_, accepted, err = wss.ReadPayload[protocol.FaceAccepted](secondFace)
	if err != nil || accepted.Operation != protocol.FaceOperationConversationList {
		t.Fatalf("second Face list accepted = %+v, %v", accepted, err)
	}
	_, result, err = wss.ReadPayload[protocol.FaceResult](secondFace)
	if err != nil {
		t.Fatal(err)
	}
	secondList, err := protocol.StrictDecode[protocol.ConversationListResult](result.Data)
	if err != nil || len(secondList.Conversations) != 1 || secondList.Conversations[0].ConversationID != "conv-1" {
		t.Fatalf("second Face conversation list = %+v, %v", secondList, err)
	}

	if err := face.SendPayload("hand-list-wire", protocol.TypeFaceHandList, protocol.FaceHandList{RequestID: "hand-list-wire"}); err != nil {
		t.Fatal(err)
	}
	_, _, err = wss.ReadPayload[protocol.FaceAccepted](face)
	if err != nil {
		t.Fatal(err)
	}
	_, result, err = wss.ReadPayload[protocol.FaceResult](face)
	if err != nil {
		t.Fatal(err)
	}
	hands, err := protocol.StrictDecode[protocol.HandListResult](result.Data)
	if err != nil || len(hands.Hands) != 1 || hands.Hands[0].HandID != "hand-client" {
		t.Fatalf("wire Hand list = %+v, %v", hands, err)
	}

	if err := face.SendPayload("hand-get-wire", protocol.TypeFaceHandGet, protocol.FaceHandGet{
		RequestID: "hand-get-wire", HandID: "hand-client",
	}); err != nil {
		t.Fatal(err)
	}
	_, accepted, err = wss.ReadPayload[protocol.FaceAccepted](face)
	if err != nil || accepted.Operation != protocol.FaceOperationHandGet {
		t.Fatalf("Hand get accepted = %+v, %v", accepted, err)
	}
	handRequestEnv, err := hand.Read()
	if err != nil || handRequestEnv.Type != protocol.TypeHandInfoReq {
		t.Fatalf("Hand query = %q, %v", handRequestEnv.Type, err)
	}
	handRequest, err := protocol.DecodePayload[protocol.HandInfoReq](&handRequestEnv)
	if err != nil {
		t.Fatal(err)
	}
	if err := hand.SendPayload("hand-info-wire", protocol.TypeHandInfoResp, protocol.HandInfoResp{
		ID: handRequest.ID, Host: "dev", OS: "linux", WorkDir: "/workspace", Tools: []protocol.ToolInfo{},
	}); err != nil {
		t.Fatal(err)
	}
	_, result, err = wss.ReadPayload[protocol.FaceResult](face)
	if err != nil || result.Status != protocol.FaceResultSucceeded {
		t.Fatalf("Hand get result = %+v, %v", result, err)
	}
	handResult, err := protocol.StrictDecode[protocol.HandGetResult](result.Data)
	if err != nil || handResult.Hand.HandID != "hand-client" || handResult.Hand.Arch != "amd64" || handResult.Hand.WorkDir != "" {
		t.Fatalf("wire Hand detail = %+v, %v", handResult, err)
	}

	invalid := protocol.Envelope{
		MsgID: "invalid-wire", Type: protocol.TypeFaceConversationList,
		Payload: []byte(`{"request_id":"invalid-wire","extra":true}`),
	}
	if err := face.Send(invalid); err != nil {
		t.Fatal(err)
	}
	_, faceErr, err := wss.ReadPayload[protocol.FaceError](face)
	if err != nil || faceErr.Code != protocol.FaceErrorInvalidRequest {
		t.Fatalf("invalid wire response = %+v, %v", faceErr, err)
	}

	_ = face.Conn.Close()
	_ = secondFace.Conn.Close()
	deadline := time.Now().Add(time.Second)
	for {
		fixture.gateway.mu.Lock()
		count := len(fixture.gateway.connections)
		fixture.gateway.mu.Unlock()
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Face disconnect retained %d connection states", count)
		}
		time.Sleep(time.Millisecond)
	}
}
