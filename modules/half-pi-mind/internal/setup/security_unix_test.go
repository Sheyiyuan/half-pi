//go:build !windows

package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitTightensPermissions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".half-pi")
	if err := os.MkdirAll(filepath.Join(root, "db"), 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	env, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{env.HomeDir, env.DataDir, env.LogDir, env.SkillsDir, filepath.Dir(env.DBPath)} {
		assertMode(t, path, 0700)
	}
	assertMode(t, env.Config, 0600)
}

func TestInitRejectsConfigSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".half-pi")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "target")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(); err == nil {
		t.Fatal("Init accepted a config symlink")
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
