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

	bridge := &RemoteBridge{
		Hub:        h,
		Runs:       runs,
		ActiveHand: func() string { return "remote-hand" },
		CheckAndConfirm: func(toolName string, args json.RawMessage, llmConfirm bool) (bool, string) {
			return false, ""
		},
	}

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
		resultCh <- tool.Execute(WithRemoteBridge(context.Background(), bridge), args)
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
