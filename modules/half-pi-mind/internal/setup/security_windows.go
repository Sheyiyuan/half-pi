//go:build windows

package setup

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func secureDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("not a real directory")
	}
	return secureDACL(path, true)
}

func secureOptionalRegular(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("not a regular file")
	}
	return true, secureDACL(path, false)
}

func secureRegular(path string) error {
	exists, err := secureOptionalRegular(path)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("regular file does not exist")
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
	if err := windows.SetNamedSecurityInfo(
		filepath.Clean(path), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		return fmt.Errorf("apply DACL: %w", err)
	}
	return nil
}
