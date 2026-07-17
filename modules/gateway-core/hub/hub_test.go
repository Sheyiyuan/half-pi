package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func newRegisterMsg(clientID, peerType string) []byte {
	return newRegisterMsgWithToken(clientID, peerType, "")
}

func newRegisterMsgWithToken(clientID, peerType, token string) []byte {
	payload, _ := json.Marshal(protocol.Register{
		ClientID: clientID,
		Token:    token,
		Type:     peerType,
	})
	env, _ := json.Marshal(protocol.Envelope{
		MsgID:   "1",
		Type:    protocol.TypeRegister,
		Payload: payload,
	})
	return env
}

func newTestServer(t *testing.T) (*httptest.Server, func(string) *websocket.Conn) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()
		conn.ReadMessage() // block until client closes
	}))
	return srv, func(wsURL string) *websocket.Conn {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return conn
	}
}

func TestNewHub(t *testing.T) {
	h := New()
	if h.Count() != 0 {
		t.Error("new hub should have 0 peers")
	}
}

func TestPeerWriterAcquisitionHonorsCancelledContext(t *testing.T) {
	peer := &Peer{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := peer.acquireWriter(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquireWriter error = %v, want context.Canceled", err)
	}
}

func TestHubCloseClosesPendingHandshake(t *testing.T) {
	h := New()
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(started)
		_ = h.ServeWS(conn)
	}))
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(srv.URL, "http", "ws", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-started
	h.Close()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("pending handshake connection remained open after Hub.Close")
	}
}

func TestClosedHubRejectsNewRegistration(t *testing.T) {
	srv, dial := newTestServer(t)
	defer srv.Close()
	conn := dial(strings.Replace(srv.URL, "http", "ws", 1))
	defer conn.Close()
	h := New()
	h.Close()
	if _, err := h.Register(newRegisterMsg("late-hand", "hand"), conn); err == nil {
		t.Fatal("closed Hub accepted a new registration")
	}
}

func TestHubRegisterAndRemove(t *testing.T) {
	srv, dial := newTestServer(t)
	defer srv.Close()

	conn := dial(strings.Replace(srv.URL, "http", "ws", 1))
	defer conn.Close()

	h := New()
	h.OnHandshake(func(peer *Peer, msg protocol.Envelope) error { return nil })

	reg := newRegisterMsg("test-hand", "hand")
	peer, err := h.Register(reg, conn)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if peer.ID != "test-hand" {
		t.Errorf("peer.ID = %q, want test-hand", peer.ID)
	}
	if peer.SessionID() == "" {
		t.Fatal("peer.SessionID should be generated")
	}
	if h.Count() != 1 {
		t.Errorf("Count = %d, want 1", h.Count())
	}

	peers := h.Peers()
	if len(peers) != 1 || peers[0] != "test-hand" {
		t.Errorf("Peers snapshot mismatch: %v", peers)
	}

	h.Remove("test-hand")
	if h.Count() != 0 {
		t.Errorf("Count after remove = %d, want 0", h.Count())
	}
}

func TestHubRegisterBadType(t *testing.T) {
	srv, dial := newTestServer(t)
	defer srv.Close()
	conn := dial(strings.Replace(srv.URL, "http", "ws", 1))
	defer conn.Close()

	h := New()
	payload, _ := json.Marshal(map[string]string{"x": "y"})
	env, _ := json.Marshal(protocol.Envelope{MsgID: "1", Type: "not_register", Payload: payload})
	_, err := h.Register(env, conn)
	if err == nil {
		t.Error("non-register first message should fail")
	}
}

func TestHubRegisterBadClientID(t *testing.T) {
	srv, dial := newTestServer(t)
	defer srv.Close()
	conn := dial(strings.Replace(srv.URL, "http", "ws", 1))
	defer conn.Close()

	h := New()
	reg := newRegisterMsg("", "hand")
	_, err := h.Register(reg, conn)
	if err == nil {
		t.Error("empty client_id should fail")
	}
}

func TestHubRegisterBadPeerType(t *testing.T) {
	srv, dial := newTestServer(t)
	defer srv.Close()
	conn := dial(strings.Replace(srv.URL, "http", "ws", 1))
	defer conn.Close()

	h := New()
	reg := newRegisterMsg("test", "alien")
	_, err := h.Register(reg, conn)
	if err == nil {
		t.Error("invalid peer type should fail")
	}
}

func TestHubRegisterBadHandshake(t *testing.T) {
	srv, dial := newTestServer(t)
	defer srv.Close()
	conn := dial(strings.Replace(srv.URL, "http", "ws", 1))
	defer conn.Close()

	h := New()
	h.OnHandshake(func(peer *Peer, msg protocol.Envelope) error {
		return fmt.Errorf("bad token")
	})

	reg := newRegisterMsg("bad-token", "hand")
	_, err := h.Register(reg, conn)
	if err == nil {
		t.Error("bad handshake should fail")
	}
	if h.Count() != 0 {
		t.Errorf("failed register should not add peer, got %d", h.Count())
	}
}

func TestHubDuplicateBadHandshakeDoesNotReplaceOld(t *testing.T) {
	srv, dial := newTestServer(t)
	defer srv.Close()

	conn1 := dial(strings.Replace(srv.URL, "http", "ws", 1))
	conn2 := dial(strings.Replace(srv.URL, "http", "ws", 1))
	defer conn1.Close()
	defer conn2.Close()

	h := New()
	h.OnHandshake(func(peer *Peer, msg protocol.Envelope) error {
		reg, err := protocol.DecodePayload[protocol.Register](&msg)
		if err != nil {
			return err
		}
		if reg.Token != "ok" {
			return fmt.Errorf("bad token")
		}
		return nil
	})

	good := newRegisterMsgWithToken("dup-hand", "hand", "ok")
	oldPeer, err := h.Register(good, conn1)
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}

	bad := newRegisterMsgWithToken("dup-hand", "hand", "bad")
	if _, err := h.Register(bad, conn2); err == nil {
		t.Fatal("bad duplicate register should fail")
	}
	if h.Count() != 1 {
		t.Fatalf("old peer should remain registered, count=%d", h.Count())
	}
	if h.peers["dup-hand"] != oldPeer {
		t.Fatal("bad duplicate register replaced old peer")
	}
}

func TestHubDuplicatePeer(t *testing.T) {
	srv, dial := newTestServer(t)
	defer srv.Close()

	conn1 := dial(strings.Replace(srv.URL, "http", "ws", 1))
	conn2 := dial(strings.Replace(srv.URL, "http", "ws", 1))
	defer conn2.Close()

	h := New()
	disconnected := make(chan *Peer, 1)
	h.OnDisconnect(func(peer *Peer) { disconnected <- peer })
	reg := newRegisterMsg("dup-hand", "hand")
	oldPeer, err := h.Register(reg, conn1)
	if err != nil {
		t.Fatal(err)
	}

	// Second register with same ID should replace old
	peer, err := h.Register(reg, conn2)
	if err != nil {
		t.Fatalf("re-register should succeed: %v", err)
	}
	if h.Count() != 1 {
		t.Errorf("duplicate should still be 1 peer, got %d", h.Count())
	}
	if peer.Conn != conn2 {
		t.Error("duplicate should use new connection")
	}
	if err := h.SendPeerContext(context.Background(), oldPeer, protocol.Envelope{Type: protocol.TypePing}); err == nil {
		t.Fatal("send to replaced peer should fail")
	}
	select {
	case old := <-disconnected:
		if old.Conn != conn1 {
			t.Fatal("disconnect callback received wrong peer")
		}
	case <-time.After(time.Second):
		t.Fatal("replaced peer did not emit disconnect callback")
	}
}

func TestHubConcurrentDuplicatePeers(t *testing.T) {
	srv, dial := newTestServer(t)
	defer srv.Close()
	const peerCount = 20
	connections := make([]*websocket.Conn, peerCount)
	for i := range connections {
		connections[i] = dial(strings.Replace(srv.URL, "http", "ws", 1))
		defer connections[i].Close()
	}

	h := New()
	disconnected := make(chan *Peer, peerCount)
	h.OnDisconnect(func(peer *Peer) { disconnected <- peer })
	reg := newRegisterMsg("same-hand", "hand")
	errs := make(chan error, peerCount)
	var wg sync.WaitGroup
	for i := range connections {
		wg.Add(1)
		go func(conn *websocket.Conn) {
			defer wg.Done()
			_, err := h.Register(reg, conn)
			errs <- err
		}(connections[i])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if h.Count() != 1 {
		t.Fatalf("peer count = %d, want 1", h.Count())
	}
	if len(disconnected) != peerCount-1 {
		t.Fatalf("disconnect callbacks = %d, want %d", len(disconnected), peerCount-1)
	}
}

func TestHubOnMessage(t *testing.T) {
	h := New()
	var received *protocol.Envelope
	h.OnMessage(func(peer *Peer, msg protocol.Envelope) { received = &msg })

	msg := protocol.Envelope{MsgID: "1", Type: protocol.TypePing}
	if h.onMessage != nil {
		h.onMessage(nil, msg)
	}
	if received == nil || received.Type != protocol.TypePing {
		t.Error("OnMessage callback not invoked")
	}
}

func TestHubServeWSEndToEndReplay(t *testing.T) {
	h := New()
	received := make(chan protocol.Envelope, 2)
	h.OnMessage(func(peer *Peer, msg protocol.Envelope) {
		received <- msg
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = h.ServeWS(conn)
	}))
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(srv.URL, "http", "ws", 1), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, newRegisterMsg("hand-e2e", "hand")); err != nil {
		t.Fatalf("write register: %v", err)
	}

	var registeredEnv protocol.Envelope
	if err := conn.ReadJSON(&registeredEnv); err != nil {
		t.Fatalf("read registered: %v", err)
	}
	if registeredEnv.Type != protocol.TypeRegistered {
		t.Fatalf("registered type = %q", registeredEnv.Type)
	}
	registered, err := protocol.DecodePayload[protocol.Registered](&registeredEnv)
	if err != nil {
		t.Fatalf("decode registered: %v", err)
	}
	clientSession, err := protocol.NewSession("hand-e2e", registered.ServerID, registered.SessionID)
	if err != nil {
		t.Fatalf("client NewSession: %v", err)
	}
	if err := clientSession.Accept(registeredEnv); err != nil {
		t.Fatalf("accept registered: %v", err)
	}

	first, err := clientSession.Stamp(protocol.Envelope{MsgID: "client-1", Type: protocol.TypePing})
	if err != nil {
		t.Fatalf("stamp first: %v", err)
	}
	if err := conn.WriteJSON(first); err != nil {
		t.Fatalf("write first: %v", err)
	}
	select {
	case got := <-received:
		if got.MsgID != "client-1" || got.Seq != 1 {
			t.Fatalf("first received = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first message")
	}

	if err := conn.WriteJSON(first); err != nil {
		t.Fatalf("write replay: %v", err)
	}
	var rejected protocol.Envelope
	if err := conn.ReadJSON(&rejected); err != nil {
		t.Fatalf("read rejected: %v", err)
	}
	if err := clientSession.Accept(rejected); err != nil {
		t.Fatalf("accept rejected response: %v", err)
	}
	if rejected.Type != protocol.TypeError {
		t.Fatalf("rejected type = %q", rejected.Type)
	}
	errMsg, err := protocol.DecodePayload[protocol.ErrorMsg](&rejected)
	if err != nil {
		t.Fatalf("decode rejected: %v", err)
	}
	if errMsg.Code != "rejected" {
		t.Fatalf("error code = %q, want rejected", errMsg.Code)
	}

	second, err := clientSession.Stamp(protocol.Envelope{MsgID: "client-2", Type: protocol.TypePing})
	if err != nil {
		t.Fatalf("stamp second: %v", err)
	}
	if err := conn.WriteJSON(second); err != nil {
		t.Fatalf("write second: %v", err)
	}
	select {
	case got := <-received:
		if got.MsgID != "client-2" || got.Seq != 2 {
			t.Fatalf("second received = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for second message")
	}
}

func TestHubAcceptReplayGuard(t *testing.T) {
	h := New()
	session, err := protocol.NewSession(h.ID, "hand-1", "session-1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	peer := &Peer{ID: "hand-1", Type: PeerHand, session: session}

	first := protocol.Envelope{
		MsgID:     "msg-1",
		Type:      protocol.TypeRPCResult,
		SessionID: session.SessionID,
		From:      peer.ID,
		To:        h.ID,
		Seq:       1,
	}
	if err := h.Accept(peer, first); err != nil {
		t.Fatalf("first Accept: %v", err)
	}

	replay := first
	replay.MsgID = "msg-replay"
	if err := h.Accept(peer, replay); err == nil {
		t.Fatal("replayed seq should be rejected")
	}

	gap := first
	gap.MsgID = "msg-gap"
	gap.Seq = 3
	if err := h.Accept(peer, gap); err == nil {
		t.Fatal("out-of-order seq should be rejected")
	}

	second := first
	second.MsgID = "msg-2"
	second.Seq = 2
	if err := h.Accept(peer, second); err != nil {
		t.Fatalf("second Accept: %v", err)
	}
}

func TestHubAcceptRejectsWrongContext(t *testing.T) {
	h := New()
	session, err := protocol.NewSession(h.ID, "hand-1", "session-1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	peer := &Peer{ID: "hand-1", Type: PeerHand, session: session}

	tests := []struct {
		name string
		env  protocol.Envelope
	}{
		{
			name: "wrong session",
			env: protocol.Envelope{
				MsgID: "msg-1", Type: protocol.TypePing, SessionID: "other",
				From: peer.ID, To: h.ID, Seq: 1,
			},
		},
		{
			name: "wrong from",
			env: protocol.Envelope{
				MsgID: "msg-1", Type: protocol.TypePing, SessionID: session.SessionID,
				From: "other", To: h.ID, Seq: 1,
			},
		},
		{
			name: "wrong to",
			env: protocol.Envelope{
				MsgID: "msg-1", Type: protocol.TypePing, SessionID: session.SessionID,
				From: peer.ID, To: "other", Seq: 1,
			},
		},
		{
			name: "register after handshake",
			env: protocol.Envelope{
				MsgID: "msg-1", Type: protocol.TypeRegister, SessionID: session.SessionID,
				From: peer.ID, To: h.ID, Seq: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := h.Accept(peer, tt.env); err == nil {
				t.Fatal("Accept should reject invalid context")
			}
		})
	}
}

func TestPeerStampOutgoing(t *testing.T) {
	session, err := protocol.NewSession(DefaultHubID, "hand-1", "session-1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	peer := &Peer{ID: "hand-1", Type: PeerHand, session: session}

	first, err := peer.stampOutgoing(protocol.Envelope{MsgID: "msg-1", Type: protocol.TypePing})
	if err != nil {
		t.Fatalf("stampOutgoing: %v", err)
	}
	if first.SessionID != session.SessionID {
		t.Errorf("SessionID = %q, want %q", first.SessionID, session.SessionID)
	}
	if first.From != DefaultHubID {
		t.Errorf("From = %q, want %q", first.From, DefaultHubID)
	}
	if first.To != peer.ID {
		t.Errorf("To = %q, want %q", first.To, peer.ID)
	}
	if first.Seq != 1 {
		t.Errorf("Seq = %d, want 1", first.Seq)
	}

	second, err := peer.stampOutgoing(protocol.Envelope{MsgID: "msg-2", Type: protocol.TypePing})
	if err != nil {
		t.Fatalf("second stampOutgoing: %v", err)
	}
	if second.Seq != 2 {
		t.Errorf("second Seq = %d, want 2", second.Seq)
	}
}

func TestHubBroadcastExcludesSender(t *testing.T) {
	srv, dial := newTestServer(t)
	defer srv.Close()

	conn1 := dial(strings.Replace(srv.URL, "http", "ws", 1))
	conn2 := dial(strings.Replace(srv.URL, "http", "ws", 1))
	defer conn1.Close()
	defer conn2.Close()

	h := New()
	reg1 := newRegisterMsg("peer1", "hand")
	reg2 := newRegisterMsg("peer2", "hand")
	h.Register(reg1, conn1)
	h.Register(reg2, conn2)

	if h.Count() != 2 {
		t.Fatalf("Count = %d, want 2", h.Count())
	}

	env := protocol.Envelope{MsgID: "1", Type: protocol.TypePing}
	h.Broadcast(env, "peer1")
}
