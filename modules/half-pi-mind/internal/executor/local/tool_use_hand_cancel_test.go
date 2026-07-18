package local

import (
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
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

type toggledAuditor struct {
	fail chan bool
}

func (a *toggledAuditor) CreateRemoteRun(remoteexec.AuditRun) error { return nil }

func (a *toggledAuditor) TransitionRemoteRun(remoteexec.AuditTransition) error {
	select {
	case fail := <-a.fail:
		a.fail <- fail
		if fail {
			return errors.New("audit unavailable")
		}
	default:
	}
	return nil
}

func TestRequestRemoteCancelSendsRPCAndMarksTimeout(t *testing.T) {
	h := hub.New()
	enableTestHandshake(h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			h.ServeWS(conn)
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	session, err := wss.NewClient(wsURL).ConnectAndRegister(testHandCredentials("cancel-hand"))
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
	peer := h.PeerByType(hub.PeerHand, "cancel-hand")
	finished := make(chan struct{})
	go func() {
		requestRemoteCancel(bridge, peer, "cancel-run", "cancel-hand", "timeout")
		close(finished)
	}()

	env, err := session.Read()
	if err != nil {
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

func TestRequestRemoteCancelStillSendsWhenAuditFails(t *testing.T) {
	h := hub.New()
	enableTestHandshake(h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			h.ServeWS(conn)
		}
	}))
	defer srv.Close()
	session, err := wss.NewClient("ws" + strings.TrimPrefix(srv.URL, "http")).ConnectAndRegister(testHandCredentials("audit-hand"))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Conn.Close()

	auditor := &toggledAuditor{fail: make(chan bool, 1)}
	runs := remoteexec.NewRegistry(auditor)
	if err := runs.Create("audit-cancel-run", "session-1", "audit-hand", "exec_command"); err != nil {
		t.Fatal(err)
	}
	if err := runs.Transition("audit-cancel-run", protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := runs.Transition("audit-cancel-run", protocol.RunSent); err != nil {
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
	auditor.fail <- true
	finished := make(chan struct{})
	go func() {
		requestRemoteCancel(&RemoteBridge{Hub: h, Runs: runs}, h.PeerByType(hub.PeerHand, "audit-hand"), "audit-cancel-run", "audit-hand", "timeout")
		close(finished)
	}()

	var env protocol.Envelope
	if err := session.Conn.ReadJSON(&env); err != nil || env.Type != protocol.TypeRPCCancel {
		t.Fatalf("cancel was not sent after audit failure: type=%q err=%v", env.Type, err)
	}
	<-auditor.fail
	auditor.fail <- false
	reply, _ := protocol.NewEnvelope("", protocol.TypeRPCCancelResult, protocol.RPCCancelResult{
		RunID: "audit-cancel-run", Status: protocol.CancelCancelled,
	})
	if err := session.Send(*reply); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancel did not finish after Hand acknowledgement")
	}
	run, _ := runs.Snapshot("audit-cancel-run")
	if run.Status != protocol.RunTimedOut {
		t.Fatalf("status = %s, want timed_out", run.Status)
	}
}

func TestUseHandTimeoutSendsCancel(t *testing.T) {
	h := hub.New()
	enableTestHandshake(h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			h.ServeWS(conn)
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	session, err := wss.NewClient(wsURL).ConnectAndRegister(testHandCredentials("timeout-hand"))
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

	rpcEnv, err := session.Read()
	if err != nil {
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

	cancelEnv, err := session.Read()
	if err != nil {
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
