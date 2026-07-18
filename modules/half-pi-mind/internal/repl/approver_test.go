package repl

import (
	"context"
	"testing"
	"time"
)

func TestInputReaderCancellationDoesNotConsumeNextLine(t *testing.T) {
	lines := make(chan inputLine)
	reader := &inputReader{lines: lines}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if line, ok := reader.read(ctx); ok || line != "" {
		t.Fatalf("cancelled read = %q, %t", line, ok)
	}
	go func() { lines <- inputLine{text: "next command", ok: true} }()
	readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	if line, ok := reader.read(readCtx); !ok || line != "next command" {
		t.Fatalf("next read = %q, %t", line, ok)
	}
}
