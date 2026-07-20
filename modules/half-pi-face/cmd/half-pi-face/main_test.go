package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionDoesNotRequireConfig(t *testing.T) {
	var output, logs bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, bytes.NewReader(nil), &output, &logs); err != nil {
		t.Fatal(err)
	}
	if output.String() != "half-pi-face version dev\n" || logs.Len() != 0 {
		t.Fatalf("output=%q logs=%q", output.String(), logs.String())
	}
}

func TestTUIModeRejectsNonTerminalBeforeDial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "face.toml")
	content := `[server]
url = "ws://127.0.0.1:1/ws"
token = "00112233445566778899aabbccddeeff"
application_key = "ffeeddccbbaa99887766554433221100"

[face]
id = "test-face"
mode = "tui"
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	var output, logs bytes.Buffer
	err := run(context.Background(), []string{"--config", path}, bytes.NewReader(nil), &output, &logs)
	if err == nil || !strings.Contains(err.Error(), "--mode headless") {
		t.Fatalf("non-terminal TUI error = %v", err)
	}
}
