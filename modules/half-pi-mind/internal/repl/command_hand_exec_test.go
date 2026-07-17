package repl

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
)

type captureWriter struct {
	mu     sync.Mutex
	events []events.Event
}

func (w *captureWriter) WriteEvent(event events.Event) error {
	w.mu.Lock()
	w.events = append(w.events, event)
	w.mu.Unlock()
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
