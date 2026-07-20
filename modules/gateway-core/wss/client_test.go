package wss_test

import (
	"bytes"
	"context"
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

func TestConnectAndRegisterContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := wss.NewClient("ws://127.0.0.1:1/ws").ConnectAndRegisterContext(ctx, faceCredentials("cancelled"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ConnectAndRegisterContext error = %v", err)
	}
}

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
	h.OnHandshake(func(key hub.PeerKey) (hub.Authentication, error) {
		return hub.Authentication{Token: testToken, ApplicationKey: testKey, PrincipalID: key.Label}, nil
	})
	received := make(chan protocol.Envelope, 1)
	h.OnMessage(func(peer *hub.Peer, env protocol.Envelope) { received <- env })
	url, closeServer := startHub(t, h)
	defer closeServer()

	conn, err := wss.NewClient(url).ConnectAndRegister(faceCredentials("face-1"))
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer conn.Conn.Close()
	registerDeadline := time.Now().Add(time.Second)
	for h.PeerByType(hub.PeerFace, "face-1") == nil {
		if time.Now().After(registerDeadline) {
			t.Fatal("registered face did not become routable")
		}
		time.Sleep(time.Millisecond)
	}
	ready := conn.RegisteredEnvelope()
	registered, err := protocol.DecodePayload[protocol.Registered](&ready)
	if err != nil || ready.Type != protocol.TypeRegistered || ready.Seq != 1 ||
		registered.ClientID != "face-1" || registered.SessionID != ready.SessionID {
		t.Fatalf("registered ready envelope = %+v, %+v, %v", ready, registered, err)
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
	h.OnHandshake(func(key hub.PeerKey) (hub.Authentication, error) {
		return hub.Authentication{Token: testToken, ApplicationKey: testKey, PrincipalID: key.Label}, nil
	})
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

func TestClientRejectsWrongToken(t *testing.T) {
	h := hub.New()
	h.OnHandshake(func(key hub.PeerKey) (hub.Authentication, error) {
		return hub.Authentication{Token: testToken, ApplicationKey: testKey, PrincipalID: key.Label}, nil
	})
	url, closeServer := startHub(t, h)
	defer closeServer()
	credentials := faceCredentials("wrong-token")
	credentials.Token = "11111111111111111111111111111111"
	_, err := wss.NewClient(url).ConnectAndRegister(credentials)
	var handshakeErr *wss.HandshakeError
	if !errors.As(err, &handshakeErr) || handshakeErr.Code != "authentication_failed" || !handshakeErr.Permanent() || handshakeErr.Retryable() {
		t.Fatalf("wrong token error = %v", err)
	}
	if h.Count() != 0 {
		t.Fatal("wrong-token peer became routable")
	}
}

func TestHandshakeFramesDoNotExposeSecretsOrHandInfo(t *testing.T) {
	frames := make(chan [][]byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, registerRaw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		challenge := protocol.RegisterChallenge{
			ProtocolVersion: protocol.ProtocolVersion,
			HandshakeID:     "abcdefabcdefabcdefabcdefabcdefab",
			ServerID:        hub.DefaultHubID,
			SessionID:       "1234567890abcdef1234567890abcdef",
			Challenge:       "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
			ExpiresAt:       time.Now().Add(5 * time.Second).UnixMilli(),
			Algorithm:       protocol.HandshakeAlgorithm,
		}
		challengeEnv, err := protocol.NewEnvelope("challenge", protocol.TypeRegisterChallenge, challenge)
		if err != nil || conn.WriteJSON(challengeEnv) != nil {
			return
		}
		_, proofRaw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		frames <- [][]byte{registerRaw, proofRaw}
	}))
	defer srv.Close()

	hostname := "private-build-host"
	workDir := "/private/customer/workspace"
	credentials := wss.Credentials{
		Label: "wire-hand", Type: protocol.PeerHand,
		Token: testToken, ApplicationKey: testKey,
		Info: &protocol.HandInfo{OS: "linux", Arch: "amd64", Hostname: hostname, WorkDir: workDir},
	}
	_, err := wss.NewClient("ws" + strings.TrimPrefix(srv.URL, "http")).ConnectAndRegister(credentials)
	if err == nil {
		t.Fatal("incomplete test handshake unexpectedly succeeded")
	}

	select {
	case captured := <-frames:
		wire := bytes.Join(captured, nil)
		for name, secret := range map[string]string{
			"token": testToken, "application key": testKey, "hostname": hostname, "work dir": workDir,
		} {
			if bytes.Contains(wire, []byte(secret)) {
				t.Fatalf("raw handshake frames exposed %s", name)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for captured handshake frames")
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
	h.OnHandshake(func(key hub.PeerKey) (hub.Authentication, error) {
		return hub.Authentication{Token: testToken, ApplicationKey: testKey, PrincipalID: key.Label}, nil
	})
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
	h.OnHandshake(func(key hub.PeerKey) (hub.Authentication, error) {
		return hub.Authentication{Token: testToken, ApplicationKey: testKey, PrincipalID: key.Label}, nil
	})
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
