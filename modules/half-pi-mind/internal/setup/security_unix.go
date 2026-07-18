//go:build !windows

package setup

import (
	"errors"
	"fmt"
	"os"
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
	return os.Chmod(path, 0700)
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
	if err := os.Chmod(path, 0600); err != nil {
		return false, err
	}
	return true, nil
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
