package local

import (
	"context"
	"encoding/json"
	"fmt"
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

	runs := remoteexec.NewRegistry()
	h.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
		switch msg.Type {
		case protocol.TypeRPCAccepted:
			accepted, err := protocol.DecodePayload[protocol.RPCAccepted](&msg)
			if err == nil {
				_ = runs.ApplyAccepted(peer.ID, accepted)
			}
		case protocol.TypeRPCResult:
			result, err := protocol.DecodePayload[protocol.RPCResult](&msg)
			if err == nil {
				_ = runs.ApplyResult(peer.ID, result)
			}
		}
	})

	oldBridge := remoteBridge
	remoteBridge = &RemoteBridge{
		Hub:        h,
		Runs:       runs,
		ActiveHand: func() string { return "remote-hand" },
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
	if rpc.DeadlineAt <= time.Now().Add(3500*time.Millisecond).UnixMilli() || rpc.DeadlineAt > time.Now().Add(4100*time.Millisecond).UnixMilli() {
		t.Fatalf("rpc deadline is outside expected range: %d", rpc.DeadlineAt)
	}
	if err := protocol.ValidateApproval(rpc.Approval, rpc.RunID, "remote-hand", rpc.Tool, rpc.Args, time.Now()); err != nil {
		t.Fatalf("invalid Mind approval: %v", err)
	}

	accepted, err := protocol.NewEnvelope("", protocol.TypeRPCAccepted, protocol.RPCAccepted{
		RunID:     rpc.RunID,
		StartedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatalf("create accepted: %v", err)
	}
	if err := session.Send(*accepted); err != nil {
		t.Fatalf("send accepted: %v", err)
	}

	reply, err := protocol.NewEnvelope("", protocol.TypeRPCResult, protocol.RPCResult{
		RunID:   rpc.RunID,
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

	oldBridge := remoteBridge
	remoteBridge = &RemoteBridge{Hub: h, Runs: runs}
	defer func() { remoteBridge = oldBridge }()
	finished := make(chan struct{})
	go func() {
		requestRemoteCancel("cancel-run", "cancel-hand", "timeout")
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

	oldBridge := remoteBridge
	remoteBridge = &RemoteBridge{
		Hub: h, Runs: runs, ActiveHand: func() string { return "timeout-hand" },
		CheckAndConfirm: func(string, json.RawMessage, bool) (bool, string) { return false, "" },
	}
	defer func() { remoteBridge = oldBridge }()
	tool, _ := executor.FindTool("use_hand")
	resultCh := make(chan *executor.ToolResult, 1)
	go func() {
		args, _ := json.Marshal(map[string]any{
			"tool": "exec_command", "args": map[string]any{"command": "sleep 5"}, "timeout_ms": 50,
		})
		resultCh <- tool.Execute(context.Background(), args)
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

func TestUseHandConcurrentRunsDoNotCrossDeliver(t *testing.T) {
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
	session, err := wss.NewClient(wsURL).ConnectAndRegister("parallel-hand", hub.PeerHand, "", nil)
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
		case protocol.TypeRPCResult:
			result, decodeErr := protocol.DecodePayload[protocol.RPCResult](&msg)
			if decodeErr == nil {
				_ = runs.ApplyResult(peer.ID, result)
			}
		}
	})

	oldBridge := remoteBridge
	remoteBridge = &RemoteBridge{
		Hub: h, Runs: runs, ActiveHand: func() string { return "parallel-hand" },
		CheckAndConfirm: func(string, json.RawMessage, bool) (bool, string) { return false, "" },
	}
	defer func() { remoteBridge = oldBridge }()

	const runCount = 20
	serverErr := make(chan error, 1)
	go func() {
		for i := 0; i < runCount; i++ {
			env, readErr := session.Read()
			if readErr != nil {
				serverErr <- readErr
				return
			}
			rpc, decodeErr := protocol.DecodePayload[protocol.RPC](&env)
			if decodeErr != nil {
				serverErr <- decodeErr
				return
			}
			accepted, _ := protocol.NewEnvelope("", protocol.TypeRPCAccepted, protocol.RPCAccepted{RunID: rpc.RunID, StartedAt: time.Now().UnixMilli()})
			if sendErr := session.Send(*accepted); sendErr != nil {
				serverErr <- sendErr
				return
			}
			result, _ := protocol.NewEnvelope("", protocol.TypeRPCResult, protocol.RPCResult{
				RunID: rpc.RunID, Success: true, Output: fmt.Sprint(rpc.Args["value"]),
			})
			if sendErr := session.Send(*result); sendErr != nil {
				serverErr <- sendErr
				return
			}
		}
		serverErr <- nil
	}()

	tool, _ := executor.FindTool("use_hand")
	results := make(chan error, runCount)
	for i := 0; i < runCount; i++ {
		i := i
		go func() {
			args, _ := json.Marshal(map[string]any{
				"tool": "parallel_tool", "args": map[string]any{"value": i}, "timeout_ms": 2000,
			})
			result := tool.Execute(context.Background(), args)
			if !result.Success || !strings.Contains(result.Output, fmt.Sprintf("] %d", i)) {
				results <- fmt.Errorf("run %d received wrong result: %+v", i, result)
				return
			}
			results <- nil
		}()
	}
	var firstErr error
	for i := 0; i < runCount; i++ {
		if resultErr := <-results; resultErr != nil && firstErr == nil {
			firstErr = resultErr
		}
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	if firstErr != nil {
		t.Fatal(firstErr)
	}
}
