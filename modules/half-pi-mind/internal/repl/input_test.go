package repl

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestInputReaderNonTTYKeepsLineMode(t *testing.T) {
	inputFile, outputFile, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer inputFile.Close()
	defer outputFile.Close()

	var output bytes.Buffer
	reader, err := NewInput(inputFile, &output, &output)
	if err != nil {
		t.Fatalf("create input: %v", err)
	}
	defer reader.Close()
	if reader.editor != nil {
		t.Fatal("pipe input unexpectedly enabled terminal editing")
	}

	go func() {
		_, _ = fmt.Fprintln(outputFile, "中文 command")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	line, ok := reader.ReadLine(ctx, "> ", true)
	if !ok || line != "中文 command" {
		t.Fatalf("line = %q, %t", line, ok)
	}
	if got := output.String(); got != "> " {
		t.Fatalf("prompt = %q, want %q", got, "> ")
	}
}
