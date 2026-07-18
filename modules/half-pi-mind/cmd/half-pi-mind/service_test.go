package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/setup"
)

func TestRunServiceReturnsServerErrorAndRemovesPID(t *testing.T) {
	home := t.TempDir()
	serverErr := errors.New("listener failed")
	serverErrors := make(chan error, 1)
	serverErrors <- serverErr
	close(serverErrors)
	if err := runService(&setup.Env{HomeDir: home}, events.NewEventBus(), serverErrors); !errors.Is(err, serverErr) {
		t.Fatalf("runService error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "mind.pid")); !os.IsNotExist(err) {
		t.Fatalf("PID file remains: %v", err)
	}
}

func TestRunServiceRejectsUnwritablePIDPath(t *testing.T) {
	homeFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(homeFile, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runService(&setup.Env{HomeDir: homeFile}, events.NewEventBus(), nil); err == nil {
		t.Fatal("runService accepted an unwritable PID path")
	}
}
