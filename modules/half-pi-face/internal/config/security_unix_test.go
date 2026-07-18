//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigPermissionsAndSymlinkRejection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "face")
	path := filepath.Join(dir, "config.toml")
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	assertMode(t, dir, 0700)
	assertMode(t, path, 0600)

	target := filepath.Join(t.TempDir(), "target.toml")
	if err := os.WriteFile(target, []byte("[face]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(symlink); err == nil {
		t.Fatal("Load accepted a config symlink")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
