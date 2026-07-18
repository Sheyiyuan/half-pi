package repl

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor/local"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type captureWriter struct {
	mu     sync.Mutex
	events []events.Event
	wrote  chan events.Event
}

func (w *captureWriter) WriteEvent(event events.Event) error {
	w.mu.Lock()
	w.events = append(w.events, event)
	w.mu.Unlock()
	if w.wrote != nil {
		w.wrote <- event
	}
	return nil
}

func (*captureWriter) Close() error { return nil }

func TestParseHandExecPreservesNestedJSONAndNumbers(t *testing.T) {
	tool, args, err := parseHandExec(`exec_command {"command":"printf hello world", "nested":{"value":9007199254740993}}`)
	if err != nil {
		t.Fatal(err)
	}
	if tool != "exec_command" || args["command"] != "printf hello world" {
		t.Fatalf("unexpected parse result: %s %+v", tool, args)
	}
	nested := args["nested"].(map[string]any)
	if nested["value"] != json.Number("9007199254740993") {
		t.Fatalf("large number changed: %v", nested["value"])
	}
}

func TestParseHandExecRejectsInvalidOrTrailingJSON(t *testing.T) {
	for _, input := range []string{
		`exec_command`,
		`exec_command []`,
		`exec_command {"command":"ok"} {"extra":true}`,
		`exec_command {`,
	} {
		if _, _, err := parseHandExec(input); err == nil {
			t.Fatalf("input %q should fail", input)
		}
	}
}

func TestParseHandTaskStartReusesUseHand(t *testing.T) {
	tool, payload, err := parseHandTaskStart(`exec_command {"command":"sleep 1"} --timeout-ms 120000`)
	if err != nil {
		t.Fatal(err)
	}
	if tool != "use_hand" {
		t.Fatalf("tool = %q", tool)
	}
	var params struct {
		Background    bool           `json:"background"`
		TaskTimeoutMs int64          `json:"task_timeout_ms"`
		Tool          string         `json:"tool"`
		Args          map[string]any `json:"args"`
	}
	if err := json.Unmarshal(payload, &params); err != nil {
		t.Fatal(err)
	}
	if !params.Background || params.TaskTimeoutMs != 120000 || params.Tool != "exec_command" || params.Args["command"] != "sleep 1" {
		t.Fatalf("params = %+v", params)
	}
}

func TestParseHandTaskStartKeepsNumericJSONValue(t *testing.T) {
	_, payload, err := parseHandTaskStart(`test_tool {"count":120000}`)
	if err != nil {
		t.Fatal(err)
	}
	var params struct {
		TaskTimeoutMs int64          `json:"task_timeout_ms"`
		Args          map[string]any `json:"args"`
	}
	if err := json.Unmarshal(payload, &params); err != nil {
		t.Fatal(err)
	}
	if params.TaskTimeoutMs != 30*60*1000 || params.Args["count"] != float64(120000) {
		t.Fatalf("params = %+v", params)
	}
}

func TestParseHandTaskStartKeepsTimeoutTextInsideJSON(t *testing.T) {
	_, payload, err := parseHandTaskStart(`exec_command {"command":"tool --timeout-ms 5"}`)
	if err != nil {
		t.Fatal(err)
	}
	var params struct {
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal(payload, &params); err != nil {
		t.Fatal(err)
	}
	if params.Args["command"] != "tool --timeout-ms 5" {
		t.Fatalf("params = %+v", params)
	}
}

func TestEmitForSessionPreservesOriginSession(t *testing.T) {
	bus := events.NewEventBus()
	writer := &captureWriter{}
	bus.Subscribe(writer)
	r := &Repl{bus: bus}
	r.emitForSession("session-a", events.LevelInfo, events.TypeToolResult, "done")
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.events) != 1 || writer.events[0].SessionID != "session-a" {
		t.Fatalf("unexpected events: %+v", writer.events)
	}
}

type allowApprover struct{}

func (allowApprover) Confirm(string, string) agentcore.ConfirmResult {
	return agentcore.ConfirmAllow
}

func TestHandExecUsesSharedAuthorityAndEmitsResultForOriginalSession(t *testing.T) {
	const (
		handID    = "simulated-hand"
		sessionID = "original-session"
	)
	db, err := store.New(filepath.Join(t.TempDir(), "repl-integration.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	group, err := db.UpsertGroup(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(group.ID, sessionID); err != nil {
		t.Fatal(err)
	}

	bus := events.NewEventBus()
	writer := &captureWriter{wrote: make(chan events.Event, 8)}
	bus.Subscribe(writer)
	wsHub := hub.New()
	authority := remoteexec.NewAuthority(wsHub, remoteexec.NewRegistry(db), bus,
		func(token, handID string) (string, error) { return "test", nil })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, upgradeErr := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if upgradeErr == nil {
			_ = wsHub.ServeWS(conn)
		}
	}))
	t.Cleanup(srv.Close)
	client, err := wss.NewClient("ws"+strings.TrimPrefix(srv.URL, "http")).ConnectAndRegister(handID, hub.PeerHand, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = authority.Close()
		bus.Close()
	})
	handDone := make(chan error, 1)
	go func() {
		env, readErr := client.Read()
		if readErr != nil {
			handDone <- readErr
			return
		}
		rpc, decodeErr := protocol.DecodePayload[protocol.RPC](&env)
		if decodeErr != nil {
			handDone <- decodeErr
			return
		}
		accepted, _ := protocol.NewEnvelope("", protocol.TypeRPCAccepted,
			protocol.RPCAccepted{RunID: rpc.RunID, StartedAt: time.Now().UnixMilli()})
		if sendErr := client.Send(*accepted); sendErr != nil {
			handDone <- sendErr
			return
		}
		result, _ := protocol.NewEnvelope("", protocol.TypeRPCResult,
			protocol.RPCResult{RunID: rpc.RunID, Success: true, Output: "simulated result"})
		handDone <- client.Send(*result)
	}()

	bridge := &local.RemoteBridge{Hub: authority.Hub, Runs: authority.Registry, PendingCall: authority.PendingCall}
	core, err := agentcore.New(nil, local.New(bridge))
	if err != nil {
		t.Fatal(err)
	}
	core.Bus = bus
	if err := core.SetStore(db, sessionID); err != nil {
		t.Fatal(err)
	}
	if err := core.SetActiveHand(handID); err != nil {
		t.Fatal(err)
	}
	core.SetApprover(allowApprover{})
	bridge.ActiveHand = core.ActiveHand
	bridge.SessionID = core.SessionID
	bridge.Mode = core.SecurityMode
	bridge.SetActiveHand = core.SetActiveHand
	bridge.CheckAndConfirm = core.CheckAndConfirm
	r := &Repl{core: core, bridge: bridge, bus: bus, store: db}

	if !r.handleCommand(`/hand exec simulated_remote_tool {"value":1}`) {
		t.Fatal("/hand exec was not handled as a REPL command")
	}
	var terminal events.Event
	deadline := time.After(3 * time.Second)
	for terminal.Type != events.TypeToolResult || terminal.Source != "repl" {
		select {
		case terminal = <-writer.wrote:
		case <-deadline:
			t.Fatal("timeout waiting for REPL terminal result")
		}
	}
	if terminal.SessionID != sessionID || terminal.Level != events.LevelInfo {
		t.Fatalf("terminal event = %+v", terminal)
	}
	var view local.RemoteRunView
	if err := json.Unmarshal([]byte(terminal.Message), &view); err != nil {
		t.Fatalf("decode terminal result: %v (%q)", err, terminal.Message)
	}
	if view.Status != protocol.RunSucceeded || view.SessionID != sessionID || view.HandID != handID {
		t.Fatalf("terminal run view = %+v", view)
	}
	run, ok := authority.Registry.Snapshot(view.RunID)
	if !ok || run.Status != protocol.RunSucceeded || run.Result == nil || run.Result.Output != "simulated result" {
		t.Fatalf("Authority registry run = %+v, ok=%t", run, ok)
	}
	select {
	case err := <-handDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("simulated Hand did not finish")
	}
}
