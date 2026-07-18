package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testToken = "00112233445566778899aabbccddeeff"
	testKey   = "ffeeddccbbaa99887766554433221100"
)

func TestLoadAppliesDefaultsAndEnvironmentOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "face", "config.toml")
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	content := `[server]
url = ""
token = "bad"
application_key = "bad"

[face]
id = "bad"
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HALF_PI_FACE_SERVER", "ws://127.0.0.1:27017/ws")
	t.Setenv("FACE_TOKEN", testToken)
	t.Setenv("HALF_PI_FACE_APPLICATION_KEY", testKey)
	t.Setenv("HALF_PI_FACE_ID", "agent-face")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.URL != "ws://127.0.0.1:27017/ws" || cfg.Server.Token != testToken ||
		cfg.Server.ApplicationKey != testKey || cfg.Face.ID != "agent-face" || cfg.Face.Mode != ModeHeadless {
		t.Fatalf("loaded config = %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsInvalidCredentialsAndMode(t *testing.T) {
	valid := Config{
		Server: ServerConfig{URL: DefaultServerURL, Token: testToken, ApplicationKey: testKey},
		Face:   FaceConfig{ID: "face-1", Mode: ModeHeadless},
	}
	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{name: "label", edit: func(cfg *Config) { cfg.Face.ID = "bad label" }, want: "face.id"},
		{name: "token", edit: func(cfg *Config) { cfg.Server.Token = "bad" }, want: "server.token"},
		{name: "same secrets", edit: func(cfg *Config) { cfg.Server.ApplicationKey = testToken }, want: "must differ"},
		{name: "mode", edit: func(cfg *Config) { cfg.Face.Mode = "unknown" }, want: "face.mode"},
		{name: "insecure remote", edit: func(cfg *Config) { cfg.Server.URL = "ws://192.0.2.10/ws" }, want: "must use wss"},
		{name: "URL credentials", edit: func(cfg *Config) { cfg.Server.URL = "wss://user@example.com/ws" }, want: "user info"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.edit(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v", err)
			}
		})
	}
}

func TestWriteDefaultDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "face", "config.toml")
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("custom"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "custom" {
		t.Fatalf("config = %q, %v", data, err)
	}
}
