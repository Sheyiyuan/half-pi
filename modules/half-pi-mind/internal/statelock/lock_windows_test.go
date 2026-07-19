//go:build windows

package statelock

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireSerializesWindowsLocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "mind.lock")
	first, err := Acquire(context.Background(), path, Info{Mode: "first"})
	if err != nil {
		t.Fatal(err)
	}

	secondCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Acquire(secondCtx, path, Info{Mode: "second"}); err == nil {
		t.Fatal("second lock acquired while first lock was held")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	third, err := Acquire(context.Background(), path, Info{Mode: "third"})
	if err != nil {
		t.Fatalf("lock could not be reacquired: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}
