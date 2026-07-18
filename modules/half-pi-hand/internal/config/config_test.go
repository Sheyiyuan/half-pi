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
	if cfg.Hand.Tasks.MaxRunning != 4 || cfg.Hand.Tasks.MaxRuntime != "24h" || cfg.Hand.Tasks.MaxLogBytes != 1<<20 || cfg.Hand.Tasks.Retention != "168h" || cfg.Hand.Tasks.MaxRetained != 1000 {
		t.Fatalf("task defaults = %+v", cfg.Hand.Tasks)
	}
	if cfg.Hand.Tasks.Dir != filepath.Join(filepath.Dir(path), "tasks") {
		t.Fatalf("task dir = %q", cfg.Hand.Tasks.Dir)
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
	for _, expected := range []string{"[hand.tasks]", "max_running = 4", `max_runtime = "24h"`, "max_log_bytes = 1048576", `retention = "168h"`, "max_retained = 1000"} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("default config missing %q:\n%s", expected, data)
		}
	}
}
