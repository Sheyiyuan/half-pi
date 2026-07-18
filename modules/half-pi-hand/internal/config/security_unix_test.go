//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTightensConfigPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hand")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[hand]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	assertConfigMode(t, dir, 0700)
	assertConfigMode(t, path, 0600)
}

func TestWriteDefaultSecuresParentAndConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hand")
	path := filepath.Join(dir, "config.toml")
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	assertConfigMode(t, dir, 0700)
	assertConfigMode(t, path, 0600)
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	assertConfigMode(t, dir, 0700)
	assertConfigMode(t, path, 0600)
}

func TestConfigRejectsSymlinks(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.toml")
		if err := os.WriteFile(target, []byte("[hand]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "config.toml")
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("Load accepted a config symlink")
		}
		if err := WriteDefault(path); err == nil {
			t.Fatal("WriteDefault accepted a config symlink")
		}
	})

	t.Run("parent", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.Mkdir(target, 0700); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, "hand")
		if err := os.Symlink(target, dir); err != nil {
			t.Fatal(err)
		}
		if err := WriteDefault(filepath.Join(dir, "config.toml")); err == nil {
			t.Fatal("WriteDefault accepted a parent symlink")
		}
	})
}

func assertConfigMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
