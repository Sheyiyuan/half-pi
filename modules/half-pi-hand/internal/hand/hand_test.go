package hand

import (
	"context"
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

func TestHandRPCIntegration(t *testing.T) {
	h := hub.New()
	resultCh := make(chan protocol.RPCResult, 1)
	h.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCResult {
			result, err := protocol.DecodePayload[protocol.RPCResult](&msg)
			if err == nil {
				resultCh <- result
			}
		}
	})

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
	session, err := wss.NewClient(wsURL).ConnectAndRegister("test-hand", "hand", "")
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer session.Conn.Close()

	hand := New(session)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hand.Serve(ctx)

	time.Sleep(50 * time.Millisecond)

	rpcEnv, err := protocol.NewEnvelope("", protocol.TypeRPC, protocol.RPC{
		ID:   "rpc-1",
		Tool: "exec_command",
		Args: map[string]any{"command": "echo hello"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := h.Send("test-hand", *rpcEnv); err != nil {
		t.Fatalf("send RPC: %v", err)
	}

	select {
	case result := <-resultCh:
		if result.ID != "rpc-1" {
			t.Errorf("rpc id = %q, want rpc-1", result.ID)
		}
		if !result.Success {
			t.Errorf("rpc failed: %s", result.Error)
		}
		if !strings.Contains(result.Output, "hello") {
			t.Errorf("output = %q, want contains 'hello'", result.Output)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for RPC result")
	}
}

func TestHandUnknownTool(t *testing.T) {
	h := hub.New()
	resultCh := make(chan protocol.RPCResult, 1)
	h.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCResult {
			result, err := protocol.DecodePayload[protocol.RPCResult](&msg)
			if err == nil {
				resultCh <- result
			}
		}
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, _ := upgrader.Upgrade(w, r, nil)
		h.ServeWS(conn)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	session, err := wss.NewClient(wsURL).ConnectAndRegister("test-hand-2", "hand", "")
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer session.Conn.Close()

	hand := New(session)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hand.Serve(ctx)

	time.Sleep(50 * time.Millisecond)

	rpcEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC, protocol.RPC{
		ID:   "rpc-unknown",
		Tool: "nonexistent_tool",
		Args: map[string]any{},
	})
	h.Send("test-hand-2", *rpcEnv)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("unknown tool should fail")
		}
		if result.Error == "" {
			t.Error("expected error for unknown tool")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestHandBlockedCommand(t *testing.T) {
	h := hub.New()
	resultCh := make(chan protocol.RPCResult, 1)
	h.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCResult {
			result, err := protocol.DecodePayload[protocol.RPCResult](&msg)
			if err == nil {
				resultCh <- result
			}
		}
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, _ := upgrader.Upgrade(w, r, nil)
		h.ServeWS(conn)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	session, err := wss.NewClient(wsURL).ConnectAndRegister("test-hand-3", "hand", "")
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer session.Conn.Close()

	hand := New(session)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hand.Serve(ctx)

	time.Sleep(50 * time.Millisecond)

	rpcEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC, protocol.RPC{
		ID:   "rpc-blocked",
		Tool: "exec_command",
		Args: map[string]any{"command": "rm -rf /"},
	})
	h.Send("test-hand-3", *rpcEnv)

	select {
	case result := <-resultCh:
		if result.ID != "rpc-blocked" {
			t.Errorf("rpc id = %q", result.ID)
		}
		if !result.Success {
			t.Errorf("blocked rpc should still have Success=true (block message): %s", result.Error)
		}
		if !strings.Contains(result.Output, "拒绝") {
			t.Errorf("expected blocked message, got: %q", result.Output)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}
