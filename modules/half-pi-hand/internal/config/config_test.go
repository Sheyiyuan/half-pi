package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultServerURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.toml")
	if err := os.WriteFile(path, []byte("[server]\ntoken = \"\"\n\n[hand]\nid = \"\"\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.URL != DefaultServerURL {
		t.Fatalf("server url = %q", cfg.Server.URL)
	}
}

func TestWriteDefaultServerURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.toml")
	if err := WriteDefault(path); err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `url = "ws://127.0.0.1:15707/ws"`) {
		t.Fatalf("default config missing 15707 url:\n%s", data)
	}
}
