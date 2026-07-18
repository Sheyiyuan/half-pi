package hand

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/Sheyiyuan/half-pi/modules/half-pi-hand/internal/config"
)

func testRPC(runID, tool string, args map[string]any, timeout time.Duration) protocol.RPC {
	return protocol.RPC{
		RunID:      runID,
		Tool:       tool,
		Args:       args,
		DeadlineAt: time.Now().Add(timeout).UnixMilli(),
	}
}

func startTestHand(t *testing.T, handID string, cfg *config.Config, onMessage func(protocol.Envelope)) *hub.Hub {
	t.Helper()
	h := hub.New()
	h.OnMessage(func(_ *hub.Peer, msg protocol.Envelope) {
		onMessage(msg)
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			h.ServeWS(conn)
		}
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	session, err := wss.NewClient(wsURL).ConnectAndRegister(handID, hub.PeerHand, "", nil)
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	t.Cleanup(func() { session.Conn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go New(session, cfg).Serve(ctx)
	time.Sleep(50 * time.Millisecond)
	return h
}

func TestHandRPCIntegration(t *testing.T) {
	h := hub.New()
	resultCh := make(chan protocol.RPCResult, 1)
	messageTypes := make(chan string, 2)
	h.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCAccepted || msg.Type == protocol.TypeRPCResult {
			messageTypes <- msg.Type
		}
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
	session, err := wss.NewClient(wsURL).ConnectAndRegister("test-hand", "hand", "", nil)
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer session.Conn.Close()

	hand := New(session, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hand.Serve(ctx)

	time.Sleep(50 * time.Millisecond)

	rpcEnv, err := protocol.NewEnvelope("", protocol.TypeRPC,
		testRPC("rpc-1", "exec_command", map[string]any{"command": "echo hello"}, 3*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	if err := h.Send("test-hand", *rpcEnv); err != nil {
		t.Fatalf("send RPC: %v", err)
	}
	if got := <-messageTypes; got != protocol.TypeRPCAccepted {
		t.Fatalf("first RPC message = %q, want %q", got, protocol.TypeRPCAccepted)
	}

	select {
	case result := <-resultCh:
		if got := <-messageTypes; got != protocol.TypeRPCResult {
			t.Fatalf("second RPC message = %q, want %q", got, protocol.TypeRPCResult)
		}
		if result.RunID != "rpc-1" {
			t.Errorf("run id = %q, want rpc-1", result.RunID)
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

func TestHandGenericToolDoesNotReceiveProgress(t *testing.T) {
	const toolName = "r2_generic_progress_tool"
	var callbackPresent atomic.Bool
	executor.Register(executor.Tool{
		Name: toolName,
		Execute: func(ctx context.Context, _ json.RawMessage) *executor.ToolResult {
			executor.ReportProgress(ctx, executor.Progress{Kind: "stdout", Data: "unexpected"})
			callbackPresent.Store(true)
			return &executor.ToolResult{Success: true, Output: "final"}
		},
	})
	resultCh := make(chan protocol.RPCResult, 1)
	progressCh := make(chan protocol.RPCProgress, 1)
	h := startTestHand(t, "generic-progress-hand", nil, func(msg protocol.Envelope) {
		switch msg.Type {
		case protocol.TypeRPCProgress:
			progress, _ := protocol.DecodePayload[protocol.RPCProgress](&msg)
			progressCh <- progress
		case protocol.TypeRPCResult:
			result, _ := protocol.DecodePayload[protocol.RPCResult](&msg)
			resultCh <- result
		}
	})
	env, _ := protocol.NewEnvelope("", protocol.TypeRPC, testRPC("generic-progress-run", toolName, map[string]any{}, time.Second))
	if err := h.Send("generic-progress-hand", *env); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-resultCh:
		if result.Output != "final" {
			t.Fatalf("result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for result")
	}
	select {
	case progress := <-progressCh:
		t.Fatalf("generic tool emitted progress: %+v", progress)
	case <-time.After(50 * time.Millisecond):
	}
	if !callbackPresent.Load() {
		t.Fatal("generic tool did not execute")
	}
}

func TestProgressPumpIsBoundedAndStopsBeforeResult(t *testing.T) {
	var progressBytes int
	var progressEvents int
	resultSeen := false
	h := &Hand{}
	h.send = func(typ string, payload any) error {
		if resultSeen && typ == protocol.TypeRPCProgress {
			t.Fatal("progress sent after result")
		}
		if typ == protocol.TypeRPCProgress {
			progress := payload.(protocol.RPCProgress)
			progressBytes += len(progress.Data)
			progressEvents++
		}
		return nil
	}
	pump := newProgressPump(h, "progress-run", 10)
	for range protocol.MaxRPCProgressEvents + progressQueueSize + 10 {
		pump.report(executor.Progress{Kind: "stdout", Data: strings.Repeat("x", protocol.MaxRPCProgressChunkBytes)})
	}
	pump.close()
	resultSeen = true
	if progressBytes > 10 || progressEvents > protocol.MaxRPCProgressEvents {
		t.Fatalf("progress exceeded caps: bytes=%d events=%d", progressBytes, progressEvents)
	}
}

func TestProgressIsRestrictedToCommandTools(t *testing.T) {
	for _, name := range []string{"exec_command", "exec_cmd", "exec_ps"} {
		if !supportsProgress(name) {
			t.Fatalf("%s does not support progress", name)
		}
	}
	for _, name := range []string{"read_file", "write_file", "custom_tool"} {
		if supportsProgress(name) {
			t.Fatalf("generic tool %s supports progress", name)
		}
	}
}

func TestFinalOutputLimitIsIndependentOfProgressCap(t *testing.T) {
	configured := int64(protocol.MaxRPCProgressBytes + 123)
	h := &Hand{cfg: &config.Config{Hand: config.HandConfig{Limits: config.LimitsConfig{MaxOutputSize: configured}}}}
	if got := h.maxOutputSize(); got != configured {
		t.Fatalf("final output limit = %d, want %d", got, configured)
	}
	if got := h.maxProgressSize(); got != protocol.MaxRPCProgressBytes {
		t.Fatalf("progress limit = %d, want %d", got, protocol.MaxRPCProgressBytes)
	}
	if got := (&Hand{}).maxOutputSize(); got != 1<<20 {
		t.Fatalf("default final output limit = %d", got)
	}
}

func TestHandAllowListMiss(t *testing.T) {
	rejectedCh := make(chan protocol.RPCRejected, 1)
	cfg := &config.Config{Hand: config.HandConfig{Permission: config.PermissionConfig{AllowTools: []string{"read_file"}}}}
	h := startTestHand(t, "allow-list-hand", cfg, func(msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCRejected {
			rejected, err := protocol.DecodePayload[protocol.RPCRejected](&msg)
			if err == nil {
				rejectedCh <- rejected
			}
		}
	})
	rpcEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC,
		testRPC("allow-miss", "exec_command", map[string]any{"command": "echo no"}, time.Second))
	if err := h.Send("allow-list-hand", *rpcEnv); err != nil {
		t.Fatal(err)
	}
	select {
	case rejected := <-rejectedCh:
		if rejected.Code != protocol.RejectAllowToolsMiss {
			t.Fatalf("reject code = %q, want %q", rejected.Code, protocol.RejectAllowToolsMiss)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for allow-list rejection")
	}
}

func TestHandMalformedRPCWithRunIDIsRejected(t *testing.T) {
	rejectedCh := make(chan protocol.RPCRejected, 1)
	h := startTestHand(t, "malformed-hand", nil, func(msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCRejected {
			rejected, err := protocol.DecodePayload[protocol.RPCRejected](&msg)
			if err == nil {
				rejectedCh <- rejected
			}
		}
	})
	env := protocol.Envelope{
		Type:    protocol.TypeRPC,
		Payload: json.RawMessage(`{"run_id":"malformed-run","tool":"read_file","args":[],"deadline_at":9999999999999}`),
	}
	if err := h.Send("malformed-hand", env); err != nil {
		t.Fatal(err)
	}
	select {
	case rejected := <-rejectedCh:
		if rejected.RunID != "malformed-run" || rejected.Code != protocol.RejectInvalidRequest {
			t.Fatalf("unexpected rejection: %+v", rejected)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for malformed RPC rejection")
	}
}

func TestHandApprovalGuardsDefaultConfirm(t *testing.T) {
	const toolName = "phase1_default_confirm_tool"
	var executions atomic.Int32
	executor.Register(executor.Tool{
		Name:           toolName,
		DefaultConfirm: true,
		Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
			executions.Add(1)
			return &executor.ToolResult{Success: true, Output: "executed"}
		},
	})

	rejectedCh := make(chan protocol.RPCRejected, 2)
	resultCh := make(chan protocol.RPCResult, 1)
	h := startTestHand(t, "approval-hand", nil, func(msg protocol.Envelope) {
		switch msg.Type {
		case protocol.TypeRPCRejected:
			rejected, err := protocol.DecodePayload[protocol.RPCRejected](&msg)
			if err == nil {
				rejectedCh <- rejected
			}
		case protocol.TypeRPCResult:
			result, err := protocol.DecodePayload[protocol.RPCResult](&msg)
			if err == nil {
				resultCh <- result
			}
		}
	})

	missing := testRPC("approval-missing", toolName, map[string]any{"value": 1}, time.Second)
	missingEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC, missing)
	if err := h.Send("approval-hand", *missingEnv); err != nil {
		t.Fatal(err)
	}
	if rejected := <-rejectedCh; rejected.Code != protocol.RejectApprovalRequired {
		t.Fatalf("missing approval code = %q", rejected.Code)
	}

	wrong := testRPC("approval-wrong", toolName, map[string]any{"value": 2}, time.Second)
	wrong.Approval = &protocol.Approval{
		Approved: true, Source: "mind", OneShot: true, ArgsDigest: "wrong",
		ApprovedAt: time.Now().Add(-time.Second).UnixMilli(), ExpiresAt: time.Now().Add(time.Second).UnixMilli(),
	}
	wrongEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC, wrong)
	if err := h.Send("approval-hand", *wrongEnv); err != nil {
		t.Fatal(err)
	}
	if rejected := <-rejectedCh; rejected.Code != protocol.RejectApprovalDigestMismatch {
		t.Fatalf("wrong digest code = %q", rejected.Code)
	}

	valid := testRPC("approval-valid", toolName, map[string]any{"value": 3}, time.Second)
	digest, _ := protocol.ApprovalDigest(valid.RunID, "approval-hand", valid.Tool, valid.Args)
	valid.Approval = &protocol.Approval{
		Approved: true, Source: "mind", OneShot: true, ArgsDigest: digest,
		ApprovedAt: time.Now().Add(-time.Second).UnixMilli(), ExpiresAt: time.Now().Add(time.Second).UnixMilli(),
	}
	validEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC, valid)
	if err := h.Send("approval-hand", *validEnv); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-resultCh:
		if !result.Success {
			t.Fatalf("approved tool failed: %s", result.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for approved result")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("execution count = %d, want 1", got)
	}
}

func TestHandApprovalSatisfiesCheckConfirmAndPreservesOutputLimit(t *testing.T) {
	const toolName = "phase1_check_confirm_tool"
	var executions atomic.Int32
	executor.Register(executor.Tool{
		Name: toolName,
		Check: func(json.RawMessage) (executor.Decision, string) {
			return executor.DecisionConfirm, "confirm required"
		},
		Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
			executions.Add(1)
			return &executor.ToolResult{Success: true, Output: "你好world"}
		},
	})

	resultCh := make(chan protocol.RPCResult, 1)
	cfg := &config.Config{Hand: config.HandConfig{Limits: config.LimitsConfig{MaxOutputSize: 4}}}
	h := startTestHand(t, "check-confirm-hand", cfg, func(msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCResult {
			result, err := protocol.DecodePayload[protocol.RPCResult](&msg)
			if err == nil {
				resultCh <- result
			}
		}
	})
	rpc := testRPC("check-confirm-run", toolName, map[string]any{}, time.Second)
	digest, _ := protocol.ApprovalDigest(rpc.RunID, "check-confirm-hand", rpc.Tool, rpc.Args)
	rpc.Approval = &protocol.Approval{
		Approved: true, Source: "mind", OneShot: true, ArgsDigest: digest,
		ApprovedAt: time.Now().Add(-time.Second).UnixMilli(), ExpiresAt: time.Now().Add(time.Second).UnixMilli(),
	}
	env, _ := protocol.NewEnvelope("", protocol.TypeRPC, rpc)
	if err := h.Send("check-confirm-hand", *env); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-resultCh:
		if !result.Success || !result.Truncated {
			t.Fatalf("unexpected result: %+v", result)
		}
		if !strings.HasPrefix(result.Output, "你") {
			t.Fatalf("UTF-8 output was truncated incorrectly: %q", result.Output)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for check-confirm result")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("execution count = %d, want 1", got)
	}
}

func TestHandConcurrentDuplicateRunExecutesOnce(t *testing.T) {
	const toolName = "phase1_duplicate_tool"
	var executions atomic.Int32
	executor.Register(executor.Tool{
		Name: toolName,
		Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
			executions.Add(1)
			time.Sleep(50 * time.Millisecond)
			return &executor.ToolResult{Success: true}
		},
	})

	rejectedCh := make(chan protocol.RPCRejected, 1)
	resultCh := make(chan protocol.RPCResult, 1)
	h := startTestHand(t, "duplicate-hand", nil, func(msg protocol.Envelope) {
		switch msg.Type {
		case protocol.TypeRPCRejected:
			rejected, _ := protocol.DecodePayload[protocol.RPCRejected](&msg)
			rejectedCh <- rejected
		case protocol.TypeRPCResult:
			result, _ := protocol.DecodePayload[protocol.RPCResult](&msg)
			resultCh <- result
		}
	})
	rpc := testRPC("same-run", toolName, map[string]any{}, time.Second)
	first, _ := protocol.NewEnvelope("", protocol.TypeRPC, rpc)
	second, _ := protocol.NewEnvelope("", protocol.TypeRPC, rpc)
	if err := h.Send("duplicate-hand", *first); err != nil {
		t.Fatal(err)
	}
	if err := h.Send("duplicate-hand", *second); err != nil {
		t.Fatal(err)
	}

	select {
	case rejected := <-rejectedCh:
		if rejected.Code != protocol.RejectDuplicateRun {
			t.Fatalf("duplicate reject code = %q", rejected.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for duplicate rejection")
	}
	select {
	case <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for original result")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("execution count = %d, want 1", got)
	}
}

func TestHandCancelRunningRPC(t *testing.T) {
	acceptedCh := make(chan protocol.RPCAccepted, 1)
	cancelCh := make(chan protocol.RPCCancelResult, 1)
	resultCh := make(chan protocol.RPCResult, 1)
	h := startTestHand(t, "cancel-hand", nil, func(msg protocol.Envelope) {
		switch msg.Type {
		case protocol.TypeRPCAccepted:
			accepted, _ := protocol.DecodePayload[protocol.RPCAccepted](&msg)
			acceptedCh <- accepted
		case protocol.TypeRPCCancelResult:
			result, _ := protocol.DecodePayload[protocol.RPCCancelResult](&msg)
			cancelCh <- result
		case protocol.TypeRPCResult:
			result, _ := protocol.DecodePayload[protocol.RPCResult](&msg)
			resultCh <- result
		}
	})
	rpcEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC,
		testRPC("cancel-run", "exec_command", map[string]any{"command": "sleep 5", "timeout": 10}, 10*time.Second))
	if err := h.Send("cancel-hand", *rpcEnv); err != nil {
		t.Fatal(err)
	}
	select {
	case <-acceptedCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for accepted")
	}
	cancelEnv, _ := protocol.NewEnvelope("", protocol.TypeRPCCancel, protocol.RPCCancel{RunID: "cancel-run", Reason: "user"})
	if err := h.Send("cancel-hand", *cancelEnv); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-cancelCh:
		if result.Status != protocol.CancelCancelled {
			t.Fatalf("cancel status = %q, want cancelled", result.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for cancel result")
	}
	select {
	case result := <-resultCh:
		t.Fatalf("cancelled run should not also send RPCResult: %+v", result)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHandCancelConcurrentRunDoesNotCancelSurvivor(t *testing.T) {
	const toolName = "phase1_context_aware_concurrent_tool"
	started := make(chan string, 2)
	releaseSurvivor := make(chan struct{})
	executor.Register(executor.Tool{
		Name: toolName,
		Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
			var params struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return &executor.ToolResult{Error: err.Error()}
			}
			started <- params.ID
			select {
			case <-ctx.Done():
				return &executor.ToolResult{Error: ctx.Err().Error()}
			case <-releaseSurvivor:
				return &executor.ToolResult{Success: true, Output: params.ID}
			}
		},
	})

	acceptedCh := make(chan protocol.RPCAccepted, 2)
	cancelCh := make(chan protocol.RPCCancelResult, 1)
	resultCh := make(chan protocol.RPCResult, 1)
	h := startTestHand(t, "concurrent-cancel-hand", nil, func(msg protocol.Envelope) {
		switch msg.Type {
		case protocol.TypeRPCAccepted:
			accepted, _ := protocol.DecodePayload[protocol.RPCAccepted](&msg)
			acceptedCh <- accepted
		case protocol.TypeRPCCancelResult:
			result, _ := protocol.DecodePayload[protocol.RPCCancelResult](&msg)
			cancelCh <- result
		case protocol.TypeRPCResult:
			result, _ := protocol.DecodePayload[protocol.RPCResult](&msg)
			resultCh <- result
		}
	})

	for _, run := range []string{"cancelled-run", "survivor-run"} {
		env, _ := protocol.NewEnvelope("", protocol.TypeRPC,
			testRPC(run, toolName, map[string]any{"id": run}, 5*time.Second))
		if err := h.Send("concurrent-cancel-hand", *env); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		select {
		case <-acceptedCh:
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for both runs to be accepted")
		}
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for both tools to start")
		}
	}

	cancelEnv, _ := protocol.NewEnvelope("", protocol.TypeRPCCancel,
		protocol.RPCCancel{RunID: "cancelled-run", Reason: "user"})
	if err := h.Send("concurrent-cancel-hand", *cancelEnv); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-cancelCh:
		if result.RunID != "cancelled-run" || result.Status != protocol.CancelCancelled {
			t.Fatalf("unexpected cancel result: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for cancellation result")
	}

	close(releaseSurvivor)
	select {
	case result := <-resultCh:
		if result.RunID != "survivor-run" || !result.Success || result.Output != "survivor-run" {
			t.Fatalf("survivor result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("surviving run did not complete")
	}
}

func TestHandCancelUnknownAndCompletedRun(t *testing.T) {
	cancelCh := make(chan protocol.RPCCancelResult, 2)
	resultCh := make(chan protocol.RPCResult, 1)
	h := startTestHand(t, "cancel-status-hand", nil, func(msg protocol.Envelope) {
		switch msg.Type {
		case protocol.TypeRPCCancelResult:
			result, _ := protocol.DecodePayload[protocol.RPCCancelResult](&msg)
			cancelCh <- result
		case protocol.TypeRPCResult:
			result, _ := protocol.DecodePayload[protocol.RPCResult](&msg)
			resultCh <- result
		}
	})
	unknownEnv, _ := protocol.NewEnvelope("", protocol.TypeRPCCancel, protocol.RPCCancel{RunID: "unknown-run", Reason: "user"})
	if err := h.Send("cancel-status-hand", *unknownEnv); err != nil {
		t.Fatal(err)
	}
	if result := <-cancelCh; result.Status != protocol.CancelUnknownRun {
		t.Fatalf("unknown cancel status = %q", result.Status)
	}

	rpcEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC,
		testRPC("done-run", "exec_command", map[string]any{"command": "echo done"}, time.Second))
	if err := h.Send("cancel-status-hand", *rpcEnv); err != nil {
		t.Fatal(err)
	}
	select {
	case <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for completed result")
	}
	doneCancel, _ := protocol.NewEnvelope("", protocol.TypeRPCCancel, protocol.RPCCancel{RunID: "done-run", Reason: "user"})
	if err := h.Send("cancel-status-hand", *doneCancel); err != nil {
		t.Fatal(err)
	}
	if result := <-cancelCh; result.Status != protocol.CancelAlreadyDone {
		t.Fatalf("completed cancel status = %q", result.Status)
	}
}

func TestHandDisconnectCancelsRunningRPC(t *testing.T) {
	hubServer := hub.New()
	acceptedCh := make(chan struct{}, 1)
	hubServer.OnMessage(func(_ *hub.Peer, msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCAccepted {
			acceptedCh <- struct{}{}
		}
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			hubServer.ServeWS(conn)
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	session, err := wss.NewClient(wsURL).ConnectAndRegister("disconnect-hand", hub.PeerHand, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	hand := New(session, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hand.Serve(ctx)
	time.Sleep(50 * time.Millisecond)
	rpcEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC,
		testRPC("disconnect-run", "exec_command", map[string]any{"command": "sleep 5", "timeout": 10}, 10*time.Second))
	if err := hubServer.Send("disconnect-hand", *rpcEnv); err != nil {
		t.Fatal(err)
	}
	select {
	case <-acceptedCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for accepted")
	}
	hubServer.Remove("disconnect-hand")
	deadline := time.Now().Add(2 * time.Second)
	for {
		hand.tasksMu.Lock()
		task := hand.tasks["disconnect-run"]
		stopped := task != nil && task.status != taskRunning
		hand.tasksMu.Unlock()
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("running RPC did not stop after disconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHandPrunesCompletedTasks(t *testing.T) {
	hand := &Hand{tasks: make(map[string]*task)}
	now := time.Now()
	hand.tasks["old"] = &task{status: taskDone, finishedAt: now.Add(-completedTaskRetention)}
	hand.tasks["running"] = &task{status: taskRunning}
	hand.tasksMu.Lock()
	hand.pruneTasksLocked(now)
	hand.tasksMu.Unlock()
	if _, ok := hand.tasks["old"]; ok {
		t.Fatal("expired completed task should be pruned")
	}
	if _, ok := hand.tasks["running"]; !ok {
		t.Fatal("running task must not be pruned")
	}
}

func TestHandInfoIncludesToolSchemas(t *testing.T) {
	infoCh := make(chan protocol.HandInfoResp, 1)
	h := startTestHand(t, "schema-hand", nil, func(msg protocol.Envelope) {
		if msg.Type == protocol.TypeHandInfoResp {
			info, err := protocol.DecodePayload[protocol.HandInfoResp](&msg)
			if err == nil {
				infoCh <- info
			}
		}
	})
	req, _ := protocol.NewEnvelope("", protocol.TypeHandInfoReq, protocol.HandInfoReq{ID: "schema-request"})
	if err := h.Send("schema-hand", *req); err != nil {
		t.Fatal(err)
	}
	select {
	case info := <-infoCh:
		for _, tool := range info.Tools {
			if tool.Name == "exec_command" {
				properties, ok := tool.Parameters["properties"].(map[string]any)
				if !ok || properties["command"] == nil {
					t.Fatalf("exec_command schema missing command: %+v", tool.Parameters)
				}
				return
			}
		}
		t.Fatal("exec_command schema not returned")
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Hand info")
	}
}

func TestHandServeStopsOnContextCancel(t *testing.T) {
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
	session, err := wss.NewClient(wsURL).ConnectAndRegister("test-hand-cancel", "hand", "", nil)
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer session.Conn.Close()

	hand := New(session, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- hand.Serve(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop after context cancel")
	}
}

func TestHandUnknownTool(t *testing.T) {
	h := hub.New()
	rejectedCh := make(chan protocol.RPCRejected, 1)
	h.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCRejected {
			result, err := protocol.DecodePayload[protocol.RPCRejected](&msg)
			if err == nil {
				rejectedCh <- result
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
	session, err := wss.NewClient(wsURL).ConnectAndRegister("test-hand-2", "hand", "", nil)
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer session.Conn.Close()

	hand := New(session, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hand.Serve(ctx)

	time.Sleep(50 * time.Millisecond)

	rpcEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC,
		testRPC("rpc-unknown", "nonexistent_tool", map[string]any{}, time.Second))
	h.Send("test-hand-2", *rpcEnv)

	select {
	case rejected := <-rejectedCh:
		if rejected.Code != protocol.RejectUnknownTool {
			t.Fatalf("reject code = %q, want %q", rejected.Code, protocol.RejectUnknownTool)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestHandDenyTool(t *testing.T) {
	h := hub.New()
	rejectedCh := make(chan protocol.RPCRejected, 1)
	h.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCRejected {
			result, err := protocol.DecodePayload[protocol.RPCRejected](&msg)
			if err == nil {
				rejectedCh <- result
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
	session, err := wss.NewClient(wsURL).ConnectAndRegister("test-hand-3", "hand", "", nil)
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer session.Conn.Close()

	cfg := &config.Config{
		Hand: config.HandConfig{
			Permission: config.PermissionConfig{
				DenyTools: []string{"exec_command"},
			},
		},
	}
	hand := New(session, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hand.Serve(ctx)

	time.Sleep(50 * time.Millisecond)

	rpcEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC,
		testRPC("rpc-denied", "exec_command", map[string]any{"command": "echo hello"}, time.Second))
	h.Send("test-hand-3", *rpcEnv)

	select {
	case rejected := <-rejectedCh:
		if rejected.RunID != "rpc-denied" {
			t.Errorf("run id = %q", rejected.RunID)
		}
		if rejected.Code != protocol.RejectDenyTools {
			t.Errorf("reject code = %q, want %q", rejected.Code, protocol.RejectDenyTools)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestHandBlockedCommand(t *testing.T) {
	h := hub.New()
	rejectedCh := make(chan protocol.RPCRejected, 1)
	h.OnMessage(func(peer *hub.Peer, msg protocol.Envelope) {
		if msg.Type == protocol.TypeRPCRejected {
			result, err := protocol.DecodePayload[protocol.RPCRejected](&msg)
			if err == nil {
				rejectedCh <- result
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
	session, err := wss.NewClient(wsURL).ConnectAndRegister("test-hand-4", "hand", "", nil)
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer session.Conn.Close()

	hand := New(session, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hand.Serve(ctx)

	time.Sleep(50 * time.Millisecond)

	rpc := testRPC("rpc-blocked", "exec_command", map[string]any{"command": "rm -rf /"}, time.Second)
	digest, _ := protocol.ApprovalDigest(rpc.RunID, "test-hand-4", rpc.Tool, rpc.Args)
	rpc.Approval = &protocol.Approval{
		Approved: true, Source: "mind", OneShot: true, ArgsDigest: digest,
		ApprovedAt: time.Now().Add(-time.Second).UnixMilli(), ExpiresAt: time.Now().Add(time.Second).UnixMilli(),
	}
	rpcEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC, rpc)
	h.Send("test-hand-4", *rpcEnv)

	select {
	case rejected := <-rejectedCh:
		if rejected.RunID != "rpc-blocked" {
			t.Errorf("run id = %q", rejected.RunID)
		}
		if rejected.Code != protocol.RejectCheckFailed {
			t.Errorf("reject code = %q, want %q", rejected.Code, protocol.RejectCheckFailed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestHandRPCTimeoutCancelsCommand(t *testing.T) {
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
	session, err := wss.NewClient(wsURL).ConnectAndRegister("test-hand-timeout", "hand", "", nil)
	if err != nil {
		t.Fatalf("ConnectAndRegister: %v", err)
	}
	defer session.Conn.Close()

	hand := New(session, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hand.Serve(ctx)

	time.Sleep(50 * time.Millisecond)

	rpcEnv, _ := protocol.NewEnvelope("", protocol.TypeRPC,
		testRPC("rpc-timeout", "exec_command", map[string]any{"command": "sleep 2", "timeout": 5}, 100*time.Millisecond))
	h.Send("test-hand-timeout", *rpcEnv)

	select {
	case result := <-resultCh:
		if result.RunID != "rpc-timeout" {
			t.Errorf("run id = %q", result.RunID)
		}
		if result.Success {
			t.Fatal("timeout command should fail")
		}
		if result.Error == "" {
			t.Fatal("timeout command should return error")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for RPC timeout result")
	}
}
