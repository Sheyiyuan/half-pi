//go:build !windows

package statelock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireSerializesLocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "mind.lock")
	ctx := context.Background()
	first, err := Acquire(ctx, path, Info{Mode: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	secondCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := Acquire(secondCtx, path, Info{Mode: "test"}); err == nil {
		t.Fatal("second lock acquired while first lock was held")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock error = %v", err)
	}
}

func TestAcquireIgnoresStaleDiagnostics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mind.lock")
	if err := os.WriteFile(path, []byte("pid=999999\nmode=old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := Acquire(context.Background(), path, Info{Mode: "new"})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) == "pid=999999\nmode=old\n" {
		t.Fatal("stale lock diagnostics were not replaced")
	}
}

func TestAcquireRejectsSymlinkPaths(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	parentLink := filepath.Join(root, "link")
	if err := os.Symlink(realDir, parentLink); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(context.Background(), filepath.Join(parentLink, "mind.lock"), Info{}); err == nil {
		t.Fatal("symlink parent accepted")
	}

	regularDir := filepath.Join(root, "regular")
	if err := os.Mkdir(regularDir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(regularDir, "target")
	if err := os.WriteFile(target, []byte("target"), 0600); err != nil {
		t.Fatal(err)
	}
	lockLink := filepath.Join(regularDir, "mind.lock")
	if err := os.Symlink(target, lockLink); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(context.Background(), lockLink, Info{}); err == nil {
		t.Fatal("symlink lock accepted")
	}
}
