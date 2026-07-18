//go:build windows

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func TestRunCommandCancelsWindowsProcessTree(t *testing.T) {
	childPIDFile := t.TempDir() + `\child.pid`
	pidFile := t.TempDir() + `\grandchild.pid`
	grandchildScriptPath := t.TempDir() + `\grandchild.ps1`
	childScriptPath := t.TempDir() + `\child.ps1`
	parentScriptPath := t.TempDir() + `\parent.ps1`
	quotedChildPIDFile := strings.ReplaceAll(childPIDFile, `'`, `''`)
	quotedPIDFile := strings.ReplaceAll(pidFile, `'`, `''`)
	writeScript(t, grandchildScriptPath, fmt.Sprintf(`Set-Content -LiteralPath '%s' -Value $PID; Start-Sleep -Seconds 30`, quotedPIDFile))
	writeScript(t, childScriptPath, fmt.Sprintf(
		`Set-Content -LiteralPath '%s' -Value $PID; $grandchild = Start-Process powershell -ArgumentList '-NoProfile','-File','%s' -PassThru; Wait-Process -Id $grandchild.Id`,
		quotedChildPIDFile, strings.ReplaceAll(grandchildScriptPath, `'`, `''`),
	))
	writeScript(t, parentScriptPath, fmt.Sprintf(
		`$child = Start-Process powershell -ArgumentList '-NoProfile','-File','%s' -PassThru; Wait-Process -Id $child.Id`,
		strings.ReplaceAll(childScriptPath, `'`, `''`),
	))

	unrelated := exec.Command("powershell", "-NoProfile", "-Command", "Start-Sleep -Seconds 30")
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = unrelated.Process.Kill()
		_ = unrelated.Wait()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.Command("powershell", "-NoProfile", "-File", parentScriptPath)
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		done <- runCommand(ctx, cmd)
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
		}
	})

	childPID := waitForChildPID(t, childPIDFile)
	grandchildPID := waitForChildPID(t, pidFile)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runCommand error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runCommand did not return after cancellation")
	}

	assertProcessMissing(t, childPID)
	assertProcessMissing(t, grandchildPID)
	assertProcessRunning(t, unrelated.Process.Pid)
}

func TestWindowsExecToolsProgressPreservesFinalOutput(t *testing.T) {
	for _, name := range []string{"exec_cmd", "exec_ps"} {
		t.Run(name, func(t *testing.T) {
			tool, ok := executor.FindTool(name)
			if !ok {
				t.Fatalf("%s not registered", name)
			}
			var chunks []executor.Progress
			var chunksMu sync.Mutex
			ctx := executor.WithProgress(context.Background(), func(progress executor.Progress) {
				chunksMu.Lock()
				defer chunksMu.Unlock()
				chunks = append(chunks, progress)
			})
			command := "echo hello"
			result := tool.Execute(ctx, json.RawMessage(`{"command":"`+command+`"}`))
			if !result.Success || !strings.Contains(result.Output, "hello") {
				t.Fatalf("result = %+v", result)
			}
			if len(chunks) == 0 {
				t.Fatal("missing progress")
			}
			for _, chunk := range chunks {
				if len(chunk.Data) > commandProgressChunkBytes || !utf8.ValidString(chunk.Data) {
					t.Fatalf("invalid chunk: %+v", chunk)
				}
			}
		})
	}
}

func writeScript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunCommandDoesNotStartWithCancelledContext(t *testing.T) {
	marker := t.TempDir() + `\started`
	quotedMarker := strings.ReplaceAll(marker, `'`, `''`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf(`Set-Content -LiteralPath '%s' -Value started`, quotedMarker))
	if err := runCommand(ctx, cmd); !errors.Is(err, context.Canceled) {
		t.Fatalf("runCommand error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled command created marker: %v", err)
	}
}

func TestRunCommandLeavesBackgroundProcessRunningOnSuccess(t *testing.T) {
	pidFile := t.TempDir() + `\background.pid`
	quotedPIDFile := strings.ReplaceAll(pidFile, `'`, `''`)
	command := fmt.Sprintf(
		`Start-Process powershell -ArgumentList '-NoProfile','-Command','Set-Content -LiteralPath ''%s'' -Value $PID; Start-Sleep -Seconds 30'`,
		quotedPIDFile,
	)
	cmd := exec.Command("powershell", "-NoProfile", "-Command", command)
	if err := runCommand(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	pid := waitForChildPID(t, pidFile)
	assertProcessRunning(t, pid)
	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Kill()
		_, _ = process.Wait()
	}
}

func waitForChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatalf("parse child PID: %v", err)
			}
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("child PID was not written")
	return 0
}

func assertProcessMissing(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		check := exec.Command("powershell", "-NoProfile", "-Command",
			fmt.Sprintf(`if (Get-Process -Id %d -ErrorAction SilentlyContinue) { exit 1 }`, pid))
		if err := check.Run(); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d survived tree cancellation", pid)
}

func assertProcessRunning(t *testing.T, pid int) {
	t.Helper()
	check := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf(`if (-not (Get-Process -Id %d -ErrorAction SilentlyContinue)) { exit 1 }`, pid))
	if err := check.Run(); err != nil {
		t.Fatalf("unrelated process %d was terminated: %v", pid, err)
	}
}
