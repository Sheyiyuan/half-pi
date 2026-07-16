package wss

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestClientConnectAndRegister(t *testing.T) {
	h := hub.New()
	received := make(chan protocol.Envelope, 1)
	h.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
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

	client := NewClient(strings.Replace(srv.URL, "http", "ws", 1))
	conn, err := client.ConnectAndRegister("face-1", hub.PeerFace, "", nil)
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer conn.Conn.Close()

	if conn.Session.SessionID == "" {
		t.Fatal("session id should be set")
	}
	if err := conn.Send(protocol.Envelope{Type: protocol.TypePing}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-received:
		if got.From != "face-1" || got.To != hub.DefaultHubID || got.Seq != 1 {
			t.Fatalf("bad received envelope: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}
