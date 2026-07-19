//go:build windows

package statelock

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type lockState = *windows.Overlapped

func newLockState() lockState { return &windows.Overlapped{} }

func tryLockFile(file *os.File, state lockState) error {
	handle := windows.Handle(file.Fd())
	return windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, state)
}

func unlockFile(file *os.File, state lockState) error {
	handle := windows.Handle(file.Fd())
	return windows.UnlockFileEx(handle, 0, 1, 0, state)
}

func isBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

func openLockFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := secureDACL(path, false); err != nil {
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
	if err := secureDACL(parent, true); err != nil {
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

func secureDACL(path string, inherit bool) error {
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("get current Windows user: %w", err)
	}
	if current.Uid == "" {
		return fmt.Errorf("current Windows user has no SID")
	}
	inheritFlags := ""
	if inherit {
		inheritFlags = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf(
		"D:P(A;%s;GA;;;SY)(A;%s;GA;;;%s)",
		inheritFlags,
		inheritFlags,
		current.Uid,
	))
	if err != nil {
		return fmt.Errorf("build DACL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read DACL: %w", err)
	}
	return windows.SetNamedSecurityInfo(
		filepath.Clean(path), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
}
