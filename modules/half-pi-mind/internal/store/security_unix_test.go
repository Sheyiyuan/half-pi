//go:build !windows

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewSecuresSQLitePaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "database")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mind.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`INSERT INTO hand_tokens (label, token) VALUES ('legacy', 'legacy-token')`); err != nil {
		t.Fatal(err)
	}

	assertStoreMode(t, dir, 0700)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		assertStoreMode(t, path+suffix, 0600)
	}
	count, err := s.LegacyHandTokenCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("LegacyHandTokenCount = %d, want 1", count)
	}
}

func TestNewTightensExistingSQLiteFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mind.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.WriteFile(path+suffix, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := secureDatabasePaths(path); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		assertStoreMode(t, path+suffix, 0600)
	}
}

func TestNewRejectsSQLiteSymlinks(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		t.Run(suffix, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "mind.db")
			if suffix != "" {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(dir, "target")
			if err := os.WriteFile(target, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path+suffix); err != nil {
				t.Fatal(err)
			}
			if _, err := New(path); err == nil {
				t.Fatal("New accepted an SQLite symlink")
			}
		})
	}
}

func assertStoreMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
