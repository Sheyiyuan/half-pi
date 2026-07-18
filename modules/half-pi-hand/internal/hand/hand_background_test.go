package hand

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/taskmanager"
)

func newTestTaskManager(t *testing.T) *taskmanager.Manager {
	t.Helper()
	manager, err := taskmanager.New(taskmanager.Config{
		Dir: t.TempDir(), MaxRunning: 4, MaxRuntime: time.Hour,
		MaxLogBytes: 1 << 20, Retention: 7 * 24 * time.Hour, MaxRetained: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Close() })
	return manager
}

func approvedBackgroundRPC(t *testing.T, id, handID, tool string, args map[string]any, runtime time.Duration) protocol.RPC {
	t.Helper()
	now := time.Now()
	rpc := protocol.RPC{
		RunID: id, Tool: tool, Args: args, DeadlineAt: now.Add(time.Minute).UnixMilli(),
		Background: &protocol.RPCBackgroundOptions{TaskID: id, MaxRuntimeMS: runtime.Milliseconds()},
	}
	digest, err := protocol.RPCApprovalDigest(rpc, handID)
	if err != nil {
		t.Fatal(err)
	}
	rpc.Approval = &protocol.Approval{
		Approved: true, Source: "mind", Mode: "normal", OneShot: true, ArgsDigest: digest,
		ApprovedAt: now.Add(-time.Second).UnixMilli(), ExpiresAt: now.Add(time.Minute).UnixMilli(),
	}
	return rpc
}

func TestHandBackgroundRequiresModeBoundApproval(t *testing.T) {
	const toolName = "r31_background_approval_tool"
	var executions atomic.Int32
	executor.Register(executor.Tool{Name: toolName, Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
		executions.Add(1)
		return &executor.ToolResult{Success: true}
	}})
	manager := newTestTaskManager(t)
	rejected := make(chan protocol.RPCRejected, 2)
	hubServer, _, _ := startReconnectableHand(t, "aaaaaaaaaaaaaaaa", manager, func(msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCRejected {
			value, _ := protocol.DecodePayload[protocol.RPCRejected](&msg)
			rejected <- value
		}
	})

	rpc := testRPC("0123456789abcdef", toolName, map[string]any{}, time.Second)
	rpc.Background = &protocol.RPCBackgroundOptions{TaskID: rpc.RunID, MaxRuntimeMS: 1000}
	env, _ := protocol.NewEnvelope("", protocol.TypeRPC, rpc)
	if err := hubServer.SendToType(hub.PeerHand, "aaaaaaaaaaaaaaaa", *env); err != nil {
		t.Fatal(err)
	}
	if result := <-rejected; result.Code != protocol.RejectApprovalRequired {
		t.Fatalf("missing approval = %+v", result)
	}

	foregroundDigest, _ := protocol.ApprovalDigest(rpc.RunID, "aaaaaaaaaaaaaaaa", rpc.Tool, rpc.Args)
	rpc.Approval = &protocol.Approval{Approved: true, Source: "mind", OneShot: true, ArgsDigest: foregroundDigest,
		ApprovedAt: time.Now().Add(-time.Second).UnixMilli(), ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}
	env, _ = protocol.NewEnvelope("", protocol.TypeRPC, rpc)
	if err := hubServer.SendToType(hub.PeerHand, "aaaaaaaaaaaaaaaa", *env); err != nil {
		t.Fatal(err)
	}
	if result := <-rejected; result.Code != protocol.RejectApprovalDigestMismatch {
		t.Fatalf("foreground approval = %+v", result)
	}
	if executions.Load() != 0 {
		t.Fatalf("unapproved executions = %d", executions.Load())
	}
}

func TestHandBackgroundSurvivesDisconnectAndReconnect(t *testing.T) {
	const toolName = "r31_reconnect_tool"
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var executions atomic.Int32
	executor.Register(executor.Tool{Name: toolName, Execute: func(ctx context.Context, _ json.RawMessage) *executor.ToolResult {
		executions.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			return &executor.ToolResult{Success: true, Output: "finished"}
		case <-ctx.Done():
			return &executor.ToolResult{Error: ctx.Err().Error()}
		}
	}})
	manager := newTestTaskManager(t)
	messages := make(chan protocol.Envelope, 20)
	hubServer, wsURL, stopFirst := startReconnectableHand(t, "bbbbbbbbbbbbbbbb", manager, func(msg protocol.Envelope) { messages <- msg })
	id := "1234567890abcdef"
	rpc := approvedBackgroundRPC(t, id, "bbbbbbbbbbbbbbbb", toolName, map[string]any{}, time.Minute)
	env, _ := protocol.NewEnvelope("", protocol.TypeRPC, rpc)
	if err := hubServer.SendToType(hub.PeerHand, "bbbbbbbbbbbbbbbb", *env); err != nil {
		t.Fatal(err)
	}
	waitMessageType(t, messages, protocol.TypeRPCResult)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background tool did not start")
	}
	stopFirst()
	deadline := time.Now().Add(time.Second)
	for hubServer.PeerByType(hub.PeerHand, "bbbbbbbbbbbbbbbb") != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	session, err := wss.NewClient(wsURL).ConnectAndRegister(testHandCredentials("bbbbbbbbbbbbbbbb"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { session.Conn.Close() })
	go NewWithTaskManager(session, nil, manager).Serve(ctx)
	deadline = time.Now().Add(time.Second)
	for hubServer.PeerByType(hub.PeerHand, "bbbbbbbbbbbbbbbb") == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	statusReq, _ := protocol.NewEnvelope("", protocol.TypeTaskStatusReq, protocol.TaskStatusReq{ID: "status-1", TaskID: id})
	if err := hubServer.SendToType(hub.PeerHand, "bbbbbbbbbbbbbbbb", *statusReq); err != nil {
		t.Fatal(err)
	}
	statusEnv := waitMessageType(t, messages, protocol.TypeTaskStatusResp)
	status, _ := protocol.DecodePayload[protocol.TaskStatusResp](&statusEnv)
	if status.Status != protocol.TaskRunning {
		t.Fatalf("status after reconnect = %+v", status)
	}
	close(release)
	deadline = time.Now().Add(time.Second)
	for {
		task, _ := manager.Status(id)
		if task.Status == protocol.TaskSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task did not finish: %+v", task)
		}
		time.Sleep(time.Millisecond)
	}
	if executions.Load() != 1 {
		t.Fatalf("executions = %d", executions.Load())
	}
	duplicate, _ := protocol.NewEnvelope("", protocol.TypeRPC, rpc)
	if err := hubServer.SendToType(hub.PeerHand, "bbbbbbbbbbbbbbbb", *duplicate); err != nil {
		t.Fatal(err)
	}
	resultEnv := waitMessageType(t, messages, protocol.TypeRPCResult)
	result, _ := protocol.DecodePayload[protocol.RPCResult](&resultEnv)
	if result.TaskStatus != protocol.TaskSucceeded || executions.Load() != 1 {
		t.Fatalf("duplicate result = %+v, executions = %d", result, executions.Load())
	}
}

func startReconnectableHand(t *testing.T, handID string, manager *taskmanager.Manager, onMessage func(protocol.Envelope)) (*hub.Hub, string, func()) {
	t.Helper()
	hubServer := hub.New()
	enableTestHandshake(hubServer)
	hubServer.OnMessage(func(_ *hub.Peer, msg protocol.Envelope) { onMessage(msg) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			hubServer.ServeWS(conn)
		}
	}))
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	session, err := wss.NewClient(wsURL).ConnectAndRegister(testHandCredentials(handID))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go NewWithTaskManager(session, nil, manager).Serve(ctx)
	t.Cleanup(func() { cancel(); session.Conn.Close() })
	time.Sleep(20 * time.Millisecond)
	return hubServer, wsURL, func() {
		session.Conn.Close()
		cancel()
	}
}

func waitMessageType(t *testing.T, messages <-chan protocol.Envelope, typ string) protocol.Envelope {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-messages:
			if msg.Type == typ {
				return msg
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %s", typ)
		}
	}
}
