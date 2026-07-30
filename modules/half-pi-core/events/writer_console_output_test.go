package events

import (
	"bytes"
	"strings"
	"testing"
)

func TestConsoleWriterUsesConfiguredOutput(t *testing.T) {
	var output bytes.Buffer
	writer := NewConsoleWriterWithOutput(&output)
	event := Event{
		Type:    TypeToolResult,
		Message: "done",
		Data:    map[string]any{"result": map[string]any{"ok": true}},
	}
	if err := writer.WriteEvent(event); err != nil {
		t.Fatalf("write event: %v", err)
	}
	got := output.String()
	for _, want := range []string{"── [RESULT] done", `"ok": true`, "\n\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q does not contain %q", got, want)
		}
	}
}
