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

func TestUseHandConcurrentRunsDoNotCrossDeliver(t *testing.T) {
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
	session, err := wss.NewClient(wsURL).ConnectAndRegister(testHandCredentials("parallel-hand"))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Conn.Close()
	waitForTestHand(t, h, "parallel-hand")
	runs := remoteexec.NewRegistry()
	authority := remoteexec.NewAuthority(h, runs, nil)
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

	bridge := &RemoteBridge{
		Hub: h, Authority: authority, Runs: runs,
		ActiveHand: func() string { return "parallel-hand" }, SessionID: func() string { return "session-1" },
		PrepareRemote: testRemotePreparer(nil),
	}

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
			result := tool.Execute(WithRemoteBridge(context.Background(), bridge), args)
			view, ok := result.Data.(RemoteRunView)
			if !result.Success || !ok || view.Output != fmt.Sprint(i) {
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
