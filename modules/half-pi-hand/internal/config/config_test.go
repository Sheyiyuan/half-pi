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
	if !strings.Contains(string(data), `application_key = ""`) {
		t.Fatalf("default config missing application key:\n%s", data)
	}
	for _, expected := range []string{"[hand.tasks]", "max_running = 4", `max_runtime = "24h"`, "max_log_bytes = 1048576", `retention = "168h"`, "max_retained = 1000"} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("default config missing %q:\n%s", expected, data)
		}
	}
}

func TestLoadApplicationKeyEnvironmentOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.toml")
	if err := os.WriteFile(path, []byte("[server]\ntoken = \"file-token\"\napplication_key = \"file-key\"\n[hand]\nid = \"hand-1\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_TOKEN", "11111111111111111111111111111111")
	t.Setenv("HALF_PI_HAND_APPLICATION_KEY", "22222222222222222222222222222222")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Token != "11111111111111111111111111111111" || cfg.Server.ApplicationKey != "22222222222222222222222222222222" {
		t.Fatalf("environment credentials not applied: %+v", cfg.Server)
	}
	if err := cfg.ValidateCredentials(); err != nil {
		t.Fatalf("ValidateCredentials: %v", err)
	}
}

func TestValidateCredentialsRequiresAllThreeValues(t *testing.T) {
	valid := Config{
		Server: ServerConfig{Token: "11111111111111111111111111111111", ApplicationKey: "22222222222222222222222222222222"},
		Hand:   HandConfig{ID: "hand-1"},
	}
	if err := valid.ValidateCredentials(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Hand.ID = "" },
		func(c *Config) { c.Hand.ID = "invalid label" },
		func(c *Config) { c.Server.Token = "" },
		func(c *Config) { c.Server.ApplicationKey = "ABC" },
	} {
		cfg := valid
		mutate(&cfg)
		if err := cfg.ValidateCredentials(); err == nil {
			t.Fatalf("invalid credentials accepted: %+v", cfg)
		}
	}
}

func TestLoadRejectsMaxOutputSizeAboveOneMiB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.toml")
	data := []byte("[hand.limits]\nmax_output_size = 1048577\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "must not exceed 1048576") {
		t.Fatalf("Load error = %v", err)
	}
}
