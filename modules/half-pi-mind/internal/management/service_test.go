package management

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func newTestService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	db, err := store.New(filepath.Join(t.TempDir(), "management.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db, nil, Runtime{Mode: "test"}), db
}

func TestServiceExpandsProfiles(t *testing.T) {
	observer, err := ExpandProfile(ProfileObserver)
	if err != nil {
		t.Fatal(err)
	}
	want := []protocol.FaceScope{
		protocol.FaceScopeHandsRead,
		protocol.FaceScopeRunsRead,
		protocol.FaceScopeSessionsRead,
		protocol.FaceScopeTasksRead,
	}
	if len(observer) != len(want) {
		t.Fatalf("observer scopes = %v", observer)
	}
	for i := range want {
		if observer[i] != want[i] {
			t.Fatalf("observer scopes = %v, want %v", observer, want)
		}
	}
	if _, err := ExpandProfile("admin"); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

func TestServiceCredentialCRUDHidesSecretsOnList(t *testing.T) {
	service, _ := newTestService(t)
	meta := RequestMeta{RequestID: "req-1", Source: SourceOfflineCLI, Actor: "test"}
	created, err := service.AddFace(meta, "desktop", []protocol.FaceScope{protocol.FaceScopeChat, protocol.FaceScopeRunsRead})
	if err != nil {
		t.Fatal(err)
	}
	if created.Token == "" || created.ApplicationKey == "" {
		t.Fatalf("created credential lacks one-time secrets: %+v", created)
	}
	faces, err := service.ListFaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(faces) != 1 || faces[0].Label != "desktop" || len(faces[0].Scopes) != 2 {
		t.Fatalf("ListFaces = %+v", faces)
	}
	removed, err := service.RemoveFace(RequestMeta{RequestID: "req-2", Source: SourceOfflineCLI, Actor: "test"}, "label", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	if removed.Label != "desktop" || removed.Type != "face" || removed.Disconnected {
		t.Fatalf("RemoveFace = %+v", removed)
	}
}

func TestServiceDuplicateLabelReturnsConflict(t *testing.T) {
	service, _ := newTestService(t)
	meta := RequestMeta{RequestID: "req-1", Source: SourceOfflineCLI, Actor: "test"}
	if _, err := service.AddHand(meta, "shared"); err != nil {
		t.Fatal(err)
	}
	_, err := service.AddHand(RequestMeta{RequestID: "req-2", Source: SourceOfflineCLI, Actor: "test"}, "shared")
	var managed *Error
	if !errors.As(err, &managed) || managed.Code != "conflict" {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestServiceRejectsUnknownSourceWithoutMutation(t *testing.T) {
	service, db := newTestService(t)
	_, err := service.AddHand(RequestMeta{RequestID: "req-invalid-source", Source: "remote", Actor: "test"}, "shared")
	var managed *Error
	if !errors.As(err, &managed) || managed.Code != "internal" {
		t.Fatalf("unknown source error = %v", err)
	}
	credentials, listErr := db.ListHandCredentials()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(credentials) != 0 {
		t.Fatalf("unknown source committed credentials: %+v", credentials)
	}
}

func TestValidateConfig(t *testing.T) {
	valid := func() *config.Config {
		return &config.Config{
			Server: config.ServerConfig{Host: "127.0.0.1", Port: 15707},
			LLM: config.LLMConfig{
				DefaultProvider: "provider", DefaultModel: "model",
				Providers: []config.ProviderCfg{{Name: "provider", Adapter: "openai", BaseURL: "https://example.com/v1"}},
				Models:    []config.ModelCfg{{ID: "model", Provider: "provider"}},
			},
		}
	}
	if err := ValidateConfig(valid()); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	tests := map[string]func(*config.Config){
		"duplicate provider": func(cfg *config.Config) {
			cfg.LLM.Providers = append(cfg.LLM.Providers, cfg.LLM.Providers[0])
		},
		"duplicate model": func(cfg *config.Config) {
			cfg.LLM.Models = append(cfg.LLM.Models, cfg.LLM.Models[0])
		},
		"unknown adapter": func(cfg *config.Config) {
			cfg.LLM.Providers[0].Adapter = "unknown"
		},
		"missing base URL": func(cfg *config.Config) {
			cfg.LLM.Providers[0].BaseURL = ""
		},
		"missing script path": func(cfg *config.Config) {
			cfg.LLM.Providers[0].Adapter = "scripted"
			cfg.LLM.Providers[0].BaseURL = ""
		},
		"missing model provider": func(cfg *config.Config) {
			cfg.LLM.Models[0].Provider = "missing"
		},
		"default provider mismatch": func(cfg *config.Config) {
			cfg.LLM.Providers = append(cfg.LLM.Providers, config.ProviderCfg{Name: "other", Adapter: "openai", BaseURL: "https://example.com/v1"})
			cfg.LLM.DefaultProvider = "other"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := valid()
			mutate(cfg)
			if err := ValidateConfig(cfg); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
