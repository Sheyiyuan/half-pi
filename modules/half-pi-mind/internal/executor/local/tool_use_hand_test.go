package local

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func TestUseHandRemoteUnknownToolKeepsHandChecks(t *testing.T) {
	h := hub.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		h.ServeWS(conn)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	session, err := wss.NewClient(wsURL).ConnectAndRegister("remote-hand", hub.PeerHand, "", nil)
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer session.Conn.Close()

	oldBridge := remoteBridge
	remoteBridge = &RemoteBridge{
		Hub:        h,
		ActiveHand: func() string { return "remote-hand" },
		PendingCall: func(id string, timeout time.Duration, expectedPeer string) (<-chan protocol.Envelope, func()) {
			ch := make(chan protocol.Envelope, 1)
			h.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
				result, err := protocol.DecodePayload[protocol.RPCResult](&msg)
				if err == nil && msg.Type == protocol.TypeRPCResult && result.ID == id && peer.ID == expectedPeer {
					ch <- msg
				}
			})
			return ch, func() {}
		},
		CheckAndConfirm: func(toolName string, args json.RawMessage, llmConfirm bool) (bool, string) {
			return false, ""
		},
	}
	defer func() { remoteBridge = oldBridge }()

	tool, ok := executor.FindTool("use_hand")
	if !ok {
		t.Fatal("use_hand tool not registered")
	}

	resultCh := make(chan *executor.ToolResult, 1)
	go func() {
		args, _ := json.Marshal(map[string]any{
			"tool":       "remote_only_tool",
			"args":       map[string]any{},
			"timeout_ms": 1000,
		})
		resultCh <- tool.Execute(context.Background(), args)
	}()

	var env protocol.Envelope
	if err := session.Conn.ReadJSON(&env); err != nil {
		t.Fatalf("read rpc: %v", err)
	}
	rpc, err := protocol.DecodePayload[protocol.RPC](&env)
	if err != nil {
		t.Fatalf("decode rpc: %v", err)
	}
	if rpc.SkipChecks {
		t.Fatal("remote-only tool should not skip Hand checks")
	}
	if rpc.TimeoutMs != 1000 {
		t.Fatalf("rpc timeout_ms = %d, want 1000", rpc.TimeoutMs)
	}

	reply, err := protocol.NewEnvelope("", protocol.TypeRPCResult, protocol.RPCResult{
		ID:      rpc.ID,
		Success: true,
		Output:  "ok",
	})
	if err != nil {
		t.Fatalf("create reply: %v", err)
	}
	if err := session.Send(*reply); err != nil {
		t.Fatalf("send reply: %v", err)
	}

	select {
	case result := <-resultCh:
		if !result.Success {
			t.Fatalf("use_hand result failed: %s", result.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting use_hand result")
	}
}
