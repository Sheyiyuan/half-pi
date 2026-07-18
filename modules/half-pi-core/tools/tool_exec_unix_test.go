//go:build !windows

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func TestRunCommandCancellationStopsDescendants(t *testing.T) {
	dir := t.TempDir()
	childReady := filepath.Join(dir, "child-ready")
	grandchildReady := filepath.Join(dir, "grandchild-ready")
	childSurvived := filepath.Join(dir, "child-survived")
	grandchildSurvived := filepath.Join(dir, "grandchild-survived")

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.Command("sh", "-c", `
		(touch "$GRANDCHILD_READY"; sleep 1; touch "$GRANDCHILD_SURVIVED") &
		touch "$CHILD_READY"
		sleep 1
		touch "$CHILD_SURVIVED"
		wait
	`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(),
		"CHILD_READY="+childReady,
		"GRANDCHILD_READY="+grandchildReady,
		"CHILD_SURVIVED="+childSurvived,
		"GRANDCHILD_SURVIVED="+grandchildSurvived,
	)
	done := make(chan error, 1)
	go func() { done <- runCommand(ctx, cmd) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		default:
		}
	})

	waitForMarkers(t, childReady, grandchildReady)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runCommand error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runCommand did not return after cancellation")
	}

	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if markerExists(childSurvived) || markerExists(grandchildSurvived) {
			t.Fatal("a descendant survived cancellation long enough to write its marker")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestExecCommandProgressPreservesFinalOutput(t *testing.T) {
	tool, ok := executor.FindTool("exec_command")
	if !ok {
		t.Fatal("exec_command not registered")
	}
	var chunks []executor.Progress
	var chunksMu sync.Mutex
	ctx := executor.WithProgress(context.Background(), func(progress executor.Progress) {
		chunksMu.Lock()
		defer chunksMu.Unlock()
		chunks = append(chunks, progress)
	})
	result := tool.Execute(ctx, json.RawMessage(`{"command":"printf '你'; printf 'err' >&2"}`))
	if !result.Success || result.Output != "你\nerr" {
		t.Fatalf("result = %+v", result)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks = %+v", chunks)
	}
	kinds := map[string]string{}
	for _, chunk := range chunks {
		if len(chunk.Data) > commandProgressChunkBytes || !utf8.ValidString(chunk.Data) {
			t.Fatalf("invalid chunk: %+v", chunk)
		}
		kinds[chunk.Kind] += chunk.Data
	}
	if kinds["stdout"] != "你" || kinds["stderr"] != "err" {
		t.Fatalf("chunks = %+v", chunks)
	}
}

func TestCommandOutputKeepsSplitUTF8Rune(t *testing.T) {
	var chunks []executor.Progress
	ctx := executor.WithProgress(context.Background(), func(progress executor.Progress) {
		chunks = append(chunks, progress)
	})
	output := commandOutput{ctx: ctx, kind: "stdout"}
	data := append([]byte(strings.Repeat("x", commandProgressChunkBytes-1)), []byte("你")...)
	if _, err := output.Write(data[:commandProgressChunkBytes]); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write(data[commandProgressChunkBytes:]); err != nil {
		t.Fatal(err)
	}
	output.Flush()
	if output.String() != string(data) || len(chunks) != 2 {
		t.Fatalf("output=%q chunks=%+v", output.String(), chunks)
	}
	if chunks[0].Data != strings.Repeat("x", commandProgressChunkBytes-1) || chunks[1].Data != "你" {
		t.Fatalf("split chunks = %+v", chunks)
	}
}

func TestCommandOutputReplacesMalformedUTF8AndContinues(t *testing.T) {
	var chunks []executor.Progress
	ctx := executor.WithProgress(context.Background(), func(progress executor.Progress) {
		chunks = append(chunks, progress)
	})
	output := commandOutput{ctx: ctx, kind: "stdout"}
	if _, err := output.Write([]byte{'a', 0xff, 'b', 0xe4, 0xbd}); err != nil {
		t.Fatal(err)
	}
	if got := joinProgress(chunks); got != "a\uFFFDb" {
		t.Fatalf("progress before trailing rune completion = %q", got)
	}
	if _, err := output.Write([]byte{0xa0, 'c'}); err != nil {
		t.Fatal(err)
	}
	output.Flush()
	if got := joinProgress(chunks); got != "a\uFFFDb你c" {
		t.Fatalf("progress = %q", got)
	}
	if output.String() != string([]byte{'a', 0xff, 'b', 0xe4, 0xbd, 0xa0, 'c'}) {
		t.Fatalf("final output bytes changed: %q", output.String())
	}
}

func joinProgress(chunks []executor.Progress) string {
	var result strings.Builder
	for _, chunk := range chunks {
		result.WriteString(chunk.Data)
	}
	return result.String()
}

func waitForMarkers(t *testing.T, paths ...string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		allExist := true
		for _, path := range paths {
			if !markerExists(path) {
				allExist = false
				break
			}
		}
		if allExist {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("markers were not created before deadline: %v", paths)
}

func markerExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
