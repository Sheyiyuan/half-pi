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
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

func TestRequestRemoteCancelSendsRPCAndMarksTimeout(t *testing.T) {
	h := hub.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			h.ServeWS(conn)
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	session, err := wss.NewClient(wsURL).ConnectAndRegister("cancel-hand", hub.PeerHand, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Conn.Close()

	runs := remoteexec.NewRegistry()
	if err := runs.Create("cancel-run", "session-1", "cancel-hand", "exec_command"); err != nil {
		t.Fatal(err)
	}
	if err := runs.Transition("cancel-run", protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := runs.Transition("cancel-run", protocol.RunSent); err != nil {
		t.Fatal(err)
	}
	h.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCCancelResult {
			result, decodeErr := protocol.DecodePayload[protocol.RPCCancelResult](&msg)
			if decodeErr == nil {
				_ = runs.ApplyCancelResult(peer.ID, result)
			}
		}
	})

	bridge := &RemoteBridge{Hub: h, Runs: runs}
	peer := h.Peer("cancel-hand")
	finished := make(chan struct{})
	go func() {
		requestRemoteCancel(bridge, peer, "cancel-run", "cancel-hand", "timeout")
		close(finished)
	}()

	var env protocol.Envelope
	if err := session.Conn.ReadJSON(&env); err != nil {
		t.Fatal(err)
	}
	if env.Type != protocol.TypeRPCCancel {
		t.Fatalf("message type = %q, want rpc_cancel", env.Type)
	}
	cancelReq, err := protocol.DecodePayload[protocol.RPCCancel](&env)
	if err != nil || cancelReq.RunID != "cancel-run" || cancelReq.Reason != "timeout" {
		t.Fatalf("unexpected cancel request: %+v, err=%v", cancelReq, err)
	}
	reply, _ := protocol.NewEnvelope("", protocol.TypeRPCCancelResult, protocol.RPCCancelResult{
		RunID: "cancel-run", Status: protocol.CancelCancelled,
	})
	if err := session.Send(*reply); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("requestRemoteCancel did not finish")
	}
	run, _ := runs.Snapshot("cancel-run")
	if run.Status != protocol.RunTimedOut {
		t.Fatalf("status = %s, want timed_out", run.Status)
	}
}

func TestUseHandTimeoutSendsCancel(t *testing.T) {
	h := hub.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			h.ServeWS(conn)
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	session, err := wss.NewClient(wsURL).ConnectAndRegister("timeout-hand", hub.PeerHand, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Conn.Close()

	runs := remoteexec.NewRegistry()
	h.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
		switch msg.Type {
		case protocol.TypeRPCAccepted:
			accepted, decodeErr := protocol.DecodePayload[protocol.RPCAccepted](&msg)
			if decodeErr == nil {
				_ = runs.ApplyAccepted(peer.ID, accepted)
			}
		case protocol.TypeRPCCancelResult:
			result, decodeErr := protocol.DecodePayload[protocol.RPCCancelResult](&msg)
			if decodeErr == nil {
				_ = runs.ApplyCancelResult(peer.ID, result)
			}
		}
	})

	bridge := &RemoteBridge{
		Hub: h, Runs: runs, ActiveHand: func() string { return "timeout-hand" },
		CheckAndConfirm: func(string, json.RawMessage, bool) (bool, string) { return false, "" },
	}
	tool, _ := executor.FindTool("use_hand")
	resultCh := make(chan *executor.ToolResult, 1)
	go func() {
		args, _ := json.Marshal(map[string]any{
			"tool": "exec_command", "args": map[string]any{"command": "sleep 5"}, "timeout_ms": 50,
		})
		resultCh <- tool.Execute(WithRemoteBridge(context.Background(), bridge), args)
	}()

	var rpcEnv protocol.Envelope
	if err := session.Conn.ReadJSON(&rpcEnv); err != nil {
		t.Fatal(err)
	}
	rpc, err := protocol.DecodePayload[protocol.RPC](&rpcEnv)
	if err != nil {
		t.Fatal(err)
	}
	accepted, _ := protocol.NewEnvelope("", protocol.TypeRPCAccepted, protocol.RPCAccepted{RunID: rpc.RunID, StartedAt: time.Now().UnixMilli()})
	if err := session.Send(*accepted); err != nil {
		t.Fatal(err)
	}

	var cancelEnv protocol.Envelope
	if err := session.Conn.ReadJSON(&cancelEnv); err != nil {
		t.Fatal(err)
	}
	if cancelEnv.Type != protocol.TypeRPCCancel {
		t.Fatalf("message type = %q, want rpc_cancel", cancelEnv.Type)
	}
	cancelReq, _ := protocol.DecodePayload[protocol.RPCCancel](&cancelEnv)
	if cancelReq.RunID != rpc.RunID || cancelReq.Reason != "timeout" {
		t.Fatalf("unexpected cancel request: %+v", cancelReq)
	}
	cancelResult, _ := protocol.NewEnvelope("", protocol.TypeRPCCancelResult, protocol.RPCCancelResult{
		RunID: rpc.RunID, Status: protocol.CancelCancelled,
	})
	if err := session.Send(*cancelResult); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-resultCh:
		if !strings.Contains(result.Error, "执行超时") {
			t.Fatalf("result error = %q, want timeout", result.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for use_hand result")
	}
}
