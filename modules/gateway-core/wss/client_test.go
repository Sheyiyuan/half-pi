package wss_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
)

const (
	testToken = "00112233445566778899aabbccddeeff"
	testKey   = "ffeeddccbbaa99887766554433221100"
)

func startHub(t *testing.T, h *hub.Hub) (string, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			_ = h.ServeWS(conn)
		}
	}))
	return "ws" + strings.TrimPrefix(srv.URL, "http"), func() {
		h.Close()
		srv.Close()
	}
}

func faceCredentials(label string) wss.Credentials {
	return wss.Credentials{Label: label, Type: protocol.PeerFace, Token: testToken, ApplicationKey: testKey}
}

func TestClientFourStepEncryptedSession(t *testing.T) {
	h := hub.New()
	h.OnHandshake(func(key hub.PeerKey, register protocol.Register) (string, error) { return testKey, nil })
	received := make(chan protocol.Envelope, 1)
	h.OnMessage(func(peer *hub.Peer, env protocol.Envelope) { received <- env })
	url, closeServer := startHub(t, h)
	defer closeServer()

	conn, err := wss.NewClient(url).ConnectAndRegister(faceCredentials("face-1"))
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer conn.Conn.Close()
	if h.PeerByType(hub.PeerFace, "face-1") == nil {
		t.Fatal("registered face is not routable")
	}
	if err := conn.SendPayload("ping-1", protocol.TypePing, protocol.Ping{Timestamp: 42}); err != nil {
		t.Fatalf("SendPayload: %v", err)
	}
	select {
	case env := <-received:
		ping, err := protocol.DecodePayload[protocol.Ping](&env)
		if err != nil || ping.Timestamp != 42 || env.Seq != 1 {
			t.Fatalf("received invalid decrypted payload: %+v, %v", env, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for encrypted message")
	}

	if err := h.SendToType(hub.PeerFace, "face-1", protocol.Envelope{Type: protocol.TypePong, Payload: json.RawMessage(`{"ts":42}`)}); err != nil {
		t.Fatalf("SendToType: %v", err)
	}
	env, pong, err := wss.ReadPayload[protocol.Pong](conn)
	if err != nil || pong.Timestamp != 42 || env.Seq != 2 {
		t.Fatalf("ReadPayload = %+v, %+v, %v", env, pong, err)
	}
}

func TestClientRejectsWrongApplicationKey(t *testing.T) {
	h := hub.New()
	h.OnHandshake(func(key hub.PeerKey, register protocol.Register) (string, error) { return testKey, nil })
	url, closeServer := startHub(t, h)
	defer closeServer()
	credentials := faceCredentials("wrong-key")
	credentials.ApplicationKey = "11111111111111111111111111111111"
	_, err := wss.NewClient(url).ConnectAndRegister(credentials)
	var handshakeErr *wss.HandshakeError
	if !errors.As(err, &handshakeErr) || handshakeErr.Code != "authentication_failed" || !handshakeErr.Permanent() || handshakeErr.Retryable() {
		t.Fatalf("wrong key error = %v", err)
	}
	if h.Count() != 0 {
		t.Fatal("wrong-key peer became routable")
	}
}

func TestClientRejectsInvalidLocalCredentialsBeforeDial(t *testing.T) {
	credentials := faceCredentials("face-1")
	credentials.Token = "bad"
	if _, err := wss.NewClient("ws://127.0.0.1:1").ConnectAndRegister(credentials); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("invalid token error = %v", err)
	}
}

func TestClientInvalidEncryptedTrafficClosesConnection(t *testing.T) {
	h := hub.New()
	h.OnHandshake(func(key hub.PeerKey, register protocol.Register) (string, error) { return testKey, nil })
	url, closeServer := startHub(t, h)
	defer closeServer()

	client, err := wss.NewClient(url).ConnectAndRegister(faceCredentials("invalid-traffic"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.SendToType(hub.PeerFace, "invalid-traffic", protocol.Envelope{Type: protocol.TypePong, Payload: json.RawMessage(`{"ts":1}`)}); err != nil {
		t.Fatal(err)
	}
	client.Session.Accept(protocol.Envelope{Type: protocol.TypePong, SessionID: client.Session.SessionID, From: hub.DefaultHubID, To: "invalid-traffic", Seq: 2})
	if _, err := client.Read(); err == nil {
		t.Fatal("invalid encrypted traffic was accepted")
	}
	if err := client.SendPayload("after-invalid", protocol.TypePing, protocol.Ping{Timestamp: 1}); err == nil {
		t.Fatal("connection remained writable after invalid encrypted traffic")
	}
}

func TestClientSendFailureAfterStampClosesConnection(t *testing.T) {
	h := hub.New()
	h.OnHandshake(func(key hub.PeerKey, register protocol.Register) (string, error) { return testKey, nil })
	url, closeServer := startHub(t, h)
	defer closeServer()

	client, err := wss.NewClient(url).ConnectAndRegister(faceCredentials("send-failure"))
	if err != nil {
		t.Fatal(err)
	}
	oversized := protocol.Envelope{Type: protocol.TypePing, Payload: json.RawMessage(`"` + strings.Repeat("x", wss.MaxPlaintextPayload) + `"`)}
	if err := client.Send(oversized); err == nil {
		t.Fatal("oversized payload send succeeded")
	}
	if err := client.SendPayload("after-failure", protocol.TypePing, protocol.Ping{Timestamp: 1}); err == nil {
		t.Fatal("connection remained writable after stamped send failure")
	}
}
