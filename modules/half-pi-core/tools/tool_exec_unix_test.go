//go:build !windows

package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
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
