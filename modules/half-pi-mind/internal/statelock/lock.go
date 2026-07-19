// Package statelock provides the Mind process-wide state lock.
package statelock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Lock 是一个已取得的 Mind 状态锁。
type Lock struct {
	file  *os.File
	path  string
	state lockState
}

// Info 是写入锁文件的诊断信息；文件内容不作为存活权威。
type Info struct {
	PID       int
	Mode      string
	StartedAt time.Time
}

// Acquire 在 deadline 内尝试取得状态锁。
func Acquire(ctx context.Context, path string, info Info) (*Lock, error) {
	if path == "" {
		return nil, fmt.Errorf("lock path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	if err := secureLockPath(path); err != nil {
		return nil, fmt.Errorf("secure state lock path: %w", err)
	}
	file, err := openLockFile(path)
	if err != nil {
		return nil, fmt.Errorf("open state lock: %w", err)
	}
	lock := &Lock{file: file, path: path, state: newLockState()}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := tryLockFile(file, lock.state); err == nil {
			if err := writeInfo(file, info); err != nil {
				_ = unlockFile(file, lock.state)
				_ = file.Close()
				return nil, err
			}
			return lock, nil
		} else if !isBusy(err) {
			_ = file.Close()
			return nil, fmt.Errorf("acquire state lock: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, fmt.Errorf("state lock busy: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// Close 释放状态锁。
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlockFile(l.file, l.state)
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	l.file = nil
	return err
}

func writeInfo(file *os.File, info Info) error {
	if info.PID == 0 {
		info.PID = os.Getpid()
	}
	if info.StartedAt.IsZero() {
		info.StartedAt = time.Now().UTC()
	}
	body := fmt.Sprintf("pid=%d\nmode=%s\nstarted_at=%s\n", info.PID, info.Mode, info.StartedAt.UTC().Format(time.RFC3339Nano))
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate lock diagnostics: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("seek lock diagnostics: %w", err)
	}
	if _, err := file.WriteString(body); err != nil {
		return fmt.Errorf("write lock diagnostics: %w", err)
	}
	return nil
}
