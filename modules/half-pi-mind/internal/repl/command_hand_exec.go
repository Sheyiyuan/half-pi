package repl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor/local"
)

func (r *Repl) handleHandSelect(handID string) {
	if handID == "" {
		r.emit(events.LevelWarn, events.TypeSystem, "usage: /hand select <id>")
		return
	}
	args, _ := json.Marshal(map[string]any{"hand_id": handID})
	result := r.core.ExecuteTool(context.Background(), "select_hand", args)
	r.printToolResult(result)
}

func (r *Repl) handleHandOnline() {
	r.printToolResult(r.core.ExecuteTool(context.Background(), "list_hands", json.RawMessage(`{}`)))
}

func (r *Repl) handleHandInfo(handID string) {
	if handID == "" {
		r.emit(events.LevelWarn, events.TypeSystem, "usage: /hand info <id>")
		return
	}
	args, _ := json.Marshal(map[string]any{"hand_id": handID})
	r.printToolResult(r.core.ExecuteTool(context.Background(), "get_hand_info", args))
}

func (r *Repl) handleHandExec(input string) {
	tool, args, err := parseHandExec(input)
	if err != nil {
		r.emit(events.LevelWarn, events.TypeSystem, err.Error())
		return
	}
	payload, _ := json.Marshal(map[string]any{"tool": tool, "args": args, "confirm": true})
	started := make(chan string, 1)
	resultCh := make(chan *executor.ToolResult, 1)
	ctx := local.WithRunStarted(context.Background(), func(runID string) { started <- runID })
	core := r.core
	sessionID := core.SessionID()
	go func() {
		resultCh <- core.ExecuteTool(ctx, "use_hand", payload)
	}()
	select {
	case runID := <-started:
		fmt.Printf("remote run started: %s\n", runID)
		go func() {
			result := <-resultCh
			data, _ := json.Marshal(result.Data)
			if result.Success {
				r.emitForSession(sessionID, events.LevelInfo, events.TypeToolResult, string(data))
			} else {
				r.emitForSession(sessionID, events.LevelError, events.TypeToolResult, fmt.Sprintf("%s\n%s", result.Error, data))
			}
		}()
	case result := <-resultCh:
		r.printToolResult(result)
	}
}

func (r *Repl) handleHandCancel(runID string) {
	if runID == "" {
		r.emit(events.LevelWarn, events.TypeSystem, "usage: /hand cancel <run_id>")
		return
	}
	view, err := local.CancelRemoteRun(context.Background(), r.bridge, runID)
	if err != nil {
		r.emit(events.LevelError, events.TypeSystem, err.Error())
		return
	}
	data, _ := json.Marshal(view)
	fmt.Println(string(data))
}

func (r *Repl) handleHandRun(runID string) {
	if runID == "" {
		r.emit(events.LevelWarn, events.TypeSystem, "usage: /hand run <run_id>")
		return
	}
	view, err := local.RemoteRunSnapshot(r.bridge, runID)
	if err == nil {
		data, _ := json.Marshal(view)
		fmt.Println(string(data))
		return
	}
	record, storeErr := r.store.GetRemoteRun(runID)
	if storeErr != nil || record.SessionID != r.core.SessionID() {
		r.emit(events.LevelError, events.TypeSystem, err.Error())
		return
	}
	data, _ := json.Marshal(local.RemoteRunView{
		RunID: record.ID, SessionID: record.SessionID, HandID: record.HandID, Tool: record.Tool,
		Status: record.Status, DurationMs: record.DurationMs, Error: record.Error,
		RejectCode: protocol.RejectCode(record.RejectCode),
	})
	fmt.Println(string(data))
}

func (r *Repl) printToolResult(result *executor.ToolResult) {
	if result == nil {
		r.emit(events.LevelError, events.TypeSystem, "tool returned no result")
		return
	}
	if result.Success {
		fmt.Println(result.Output)
		return
	}
	data, _ := json.Marshal(result.Data)
	r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("%s\n%s", result.Error, data))
}

func parseHandExec(input string) (string, map[string]any, error) {
	input = strings.TrimSpace(input)
	separator := strings.IndexAny(input, " \t")
	if separator < 1 || strings.TrimSpace(input[separator:]) == "" {
		return "", nil, fmt.Errorf("usage: /hand exec <tool> <json-object>")
	}
	tool, raw := input[:separator], input[separator:]
	decoder := json.NewDecoder(bytes.NewBufferString(strings.TrimSpace(raw)))
	decoder.UseNumber()
	var args map[string]any
	if err := decoder.Decode(&args); err != nil {
		return "", nil, fmt.Errorf("invalid tool arguments: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", nil, fmt.Errorf("tool arguments contain trailing JSON")
	}
	return tool, args, nil
}
