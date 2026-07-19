package hub_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
)

const (
	token = "00112233445566778899aabbccddeeff"
	key   = "ffeeddccbbaa99887766554433221100"
)

func server(t *testing.T, h *hub.Hub) (string, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			_ = h.ServeWS(conn)
		}
	}))
	return "ws" + strings.TrimPrefix(srv.URL, "http"), func() { h.Close(); srv.Close() }
}

func credentials(label string, peerType protocol.PeerType) wss.Credentials {
	c := wss.Credentials{Label: label, Type: peerType, Token: token, ApplicationKey: key}
	if peerType == protocol.PeerHand {
		c.Info = &protocol.HandInfo{OS: "linux", Arch: "amd64", Hostname: "test"}
	}
	return c
}

func authenticatedHub() *hub.Hub {
	h := hub.New()
	h.OnHandshake(func(peerKey hub.PeerKey) (hub.Authentication, error) {
		return hub.Authentication{Token: token, ApplicationKey: key, PrincipalID: peerKey.Label}, nil
	})
	return h
}

func TestCompoundPeerKeyAllowsSameLabelAcrossTypes(t *testing.T) {
	h := authenticatedHub()
	url, closeServer := server(t, h)
	defer closeServer()
	face, err := wss.NewClient(url).ConnectAndRegister(credentials("shared", protocol.PeerFace))
	if err != nil {
		t.Fatal(err)
	}
	defer face.Conn.Close()
	hand, err := wss.NewClient(url).ConnectAndRegister(credentials("shared", protocol.PeerHand))
	if err != nil {
		t.Fatal(err)
	}
	defer hand.Conn.Close()
	if h.Count() != 2 || h.PeerByType(hub.PeerFace, "shared") == nil || h.PeerByType(hub.PeerHand, "shared") == nil {
		t.Fatalf("compound peers not retained: %+v", h.Peers())
	}
	if info := h.PeerByType(hub.PeerHand, "shared").Info; info == nil || info.Hostname != "test" {
		t.Fatalf("encrypted Hand info was not retained: %+v", info)
	}
	h.RemoveByType(hub.PeerFace, "shared")
	if h.PeerByType(hub.PeerFace, "shared") != nil || h.PeerByType(hub.PeerHand, "shared") == nil {
		t.Fatal("typed removal affected wrong peer")
	}
}

func TestConnectCallbackRunsAfterPeerPromotion(t *testing.T) {
	h := authenticatedHub()
	connected := make(chan *hub.Peer, 1)
	h.OnConnect(func(peer *hub.Peer) {
		if h.PeerByType(peer.Type, peer.ID) != peer {
			t.Errorf("connect callback ran before peer promotion")
		}
		connected <- peer
	})
	url, closeServer := server(t, h)
	defer closeServer()
	client, err := wss.NewClient(url).ConnectAndRegister(credentials("connected", protocol.PeerFace))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Conn.Close()
	select {
	case peer := <-connected:
		if peer.ID != "connected" || peer.Type != hub.PeerFace || peer.PrincipalID != "connected" {
			t.Fatalf("connected peer = %+v", peer.Key())
		}
	case <-time.After(time.Second):
		t.Fatal("connect callback was not called")
	}
}

func TestCloseClearsPeersAndNotifiesDisconnect(t *testing.T) {
	h := authenticatedHub()
	disconnected := make(chan hub.PeerKey, 1)
	h.OnDisconnect(func(peer *hub.Peer) {
		disconnected <- peer.Key()
	})
	url, closeServer := server(t, h)
	defer closeServer()
	client, err := wss.NewClient(url).ConnectAndRegister(credentials("closing", protocol.PeerFace))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Conn.Close()

	h.Close()
	if h.Count() != 0 || h.PeerByType(hub.PeerFace, "closing") != nil || len(h.Peers()) != 0 {
		t.Fatalf("closed Hub retained peers: %+v", h.Peers())
	}
	select {
	case key := <-disconnected:
		if key != (hub.PeerKey{Type: hub.PeerFace, Label: "closing"}) {
			t.Fatalf("disconnect key = %+v", key)
		}
	case <-time.After(time.Second):
		t.Fatal("Hub close did not notify disconnect")
	}
}

func TestDuplicateRejectsNewAndPreservesOld(t *testing.T) {
	h := authenticatedHub()
	url, closeServer := server(t, h)
	defer closeServer()
	old, err := wss.NewClient(url).ConnectAndRegister(credentials("duplicate", protocol.PeerFace))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Conn.Close()
	oldPeer := h.PeerByType(hub.PeerFace, "duplicate")
	_, err = wss.NewClient(url).ConnectAndRegister(credentials("duplicate", protocol.PeerFace))
	var handshakeErr *wss.HandshakeError
	if !errors.As(err, &handshakeErr) || handshakeErr.Code != "duplicate_peer" || handshakeErr.Permanent() || !handshakeErr.Retryable() {
		t.Fatalf("duplicate error = %v", err)
	}
	if h.PeerByType(hub.PeerFace, "duplicate") != oldPeer {
		t.Fatal("duplicate replaced old peer")
	}
	if err := old.SendPayload("still-live", protocol.TypePing, protocol.Ping{Timestamp: 1}); err != nil {
		t.Fatalf("old peer was disrupted: %v", err)
	}
}

func TestProofStagePeerIsNotVisible(t *testing.T) {
	h := authenticatedHub()
	url, closeServer := server(t, h)
	defer closeServer()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reg, _ := protocol.NewEnvelope("r", protocol.TypeRegister, protocol.Register{ProtocolVersion: protocol.ProtocolVersion, ClientID: "pending", Type: protocol.PeerFace})
	if err := conn.WriteJSON(reg); err != nil {
		t.Fatal(err)
	}
	var challenge protocol.Envelope
	if err := conn.ReadJSON(&challenge); err != nil {
		t.Fatal(err)
	}
	if challenge.Type != protocol.TypeRegisterChallenge || h.Count() != 0 || h.PeerByType(hub.PeerFace, "pending") != nil {
		t.Fatal("peer became visible before proof")
	}
}

func TestRemoveByTypeRevokesAuthenticatedHandshake(t *testing.T) {
	h := hub.New()
	authenticated := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	h.OnHandshake(func(peerKey hub.PeerKey) (hub.Authentication, error) {
		once.Do(func() { close(authenticated) })
		<-release
		return hub.Authentication{Token: token, ApplicationKey: key, PrincipalID: peerKey.Label}, nil
	})
	url, closeServer := server(t, h)
	defer closeServer()

	done := make(chan error, 1)
	go func() {
		_, err := wss.NewClient(url).ConnectAndRegister(credentials("revoked", protocol.PeerFace))
		done <- err
	}()
	select {
	case <-authenticated:
	case <-time.After(2 * time.Second):
		t.Fatal("authentication did not start")
	}
	h.RemoveByType(hub.PeerFace, "revoked")
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("revoked handshake succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("revoked handshake did not stop")
	}
	if h.PeerByType(hub.PeerFace, "revoked") != nil {
		t.Fatal("revoked handshake became routable")
	}
}

func TestPostRegistrationPlaintextClosesConnection(t *testing.T) {
	h := authenticatedHub()
	url, closeServer := server(t, h)
	defer closeServer()
	client, err := wss.NewClient(url).ConnectAndRegister(credentials("plaintext", protocol.PeerFace))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := client.Session.Stamp(protocol.Envelope{Type: protocol.TypePing, Payload: []byte(`{"ts":1}`)})
	if err := client.Conn.WriteJSON(plain); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.PeerByType(hub.PeerFace, "plaintext") != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if h.PeerByType(hub.PeerFace, "plaintext") != nil {
		t.Fatal("plaintext business message did not close peer")
	}
}

func TestOversizedFrameClosesConnection(t *testing.T) {
	h := authenticatedHub()
	url, closeServer := server(t, h)
	defer closeServer()
	client, err := wss.NewClient(url).ConnectAndRegister(credentials("oversized", protocol.PeerFace))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Conn.WriteMessage(websocket.TextMessage, make([]byte, wss.MaxFrameSize+1)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.PeerByType(hub.PeerFace, "oversized") != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if h.PeerByType(hub.PeerFace, "oversized") != nil {
		t.Fatal("oversized frame did not close peer")
	}
}

func TestRegisterStrictDecodeAndVersion(t *testing.T) {
	h := authenticatedHub()
	url, closeServer := server(t, h)
	defer closeServer()
	for _, payload := range []string{
		`{"protocol_version":1,"client_id":"bad","type":"face"}`,
		`{"protocol_version":2,"client_id":"bad","type":"face","unknown":1}`,
		`{"protocol_version":2,"client_id":"bad","type":"face","token":"` + token + `"}`,
		`{"protocol_version":2,"client_id":"bad","type":"hand","info":null}`,
	} {
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatal(err)
		}
		env := protocol.Envelope{MsgID: "r", Type: protocol.TypeRegister, Payload: []byte(payload)}
		if err := conn.WriteJSON(env); err != nil {
			t.Fatal(err)
		}
		var response protocol.Envelope
		if err := conn.ReadJSON(&response); err != nil || response.Type != protocol.TypeError {
			t.Fatalf("strict register response = %+v, %v", response, err)
		}
		conn.Close()
	}
}

func TestServerSendFailureAfterStampClosesConnection(t *testing.T) {
	h := authenticatedHub()
	url, closeServer := server(t, h)
	defer closeServer()
	client, err := wss.NewClient(url).ConnectAndRegister(credentials("send-failure", protocol.PeerFace))
	if err != nil {
		t.Fatal(err)
	}

	oversized := protocol.Envelope{Type: protocol.TypePong, Payload: []byte(`"` + strings.Repeat("x", wss.MaxPlaintextPayload) + `"`)}
	if err := h.SendToType(hub.PeerFace, "send-failure", oversized); err == nil {
		t.Fatal("oversized payload send succeeded")
	}
	client.Conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := client.Conn.ReadMessage(); err == nil {
		t.Fatal("connection remained readable after stamped send failure")
	}
}
