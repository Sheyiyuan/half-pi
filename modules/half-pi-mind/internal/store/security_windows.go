//go:build windows

package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func secureDatabasePaths(path string) error {
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("database parent is not a real directory")
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return secureSQLiteFiles(path)
}

func secureSQLiteFiles(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Lstat(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("SQLite path is not a regular file")
		}
	}
	return nil
}
