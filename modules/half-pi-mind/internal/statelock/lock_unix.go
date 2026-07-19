//go:build !windows

package statelock

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

type lockState struct{}

func newLockState() lockState { return lockState{} }

func tryLockFile(file *os.File, _ lockState) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockFile(file *os.File, _ lockState) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func isBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func openLockFile(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func secureLockPath(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("lock parent is not a real directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("lock parent ownership is unavailable")
	}
	expectedUID := uint64(os.Getuid())
	if parsed, err := strconv.ParseUint(os.Getenv("SUDO_UID"), 10, 32); err == nil {
		expectedUID = parsed
	}
	if uint64(stat.Uid) != expectedUID {
		return errors.New("lock parent is not owned by the current user")
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	absParent, err := filepath.Abs(parent)
	if err != nil {
		return err
	}
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return err
	}
	if filepath.Clean(absParent) != filepath.Clean(absResolved) {
		return errors.New("lock parent contains a symlink")
	}
	if err := os.Chmod(parent, 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("lock path is not a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
