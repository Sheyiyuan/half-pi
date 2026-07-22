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
	operator, err := ExpandProfile(ProfileOperator)
	if err != nil {
		t.Fatal(err)
	}
	contains := func(scopes []protocol.FaceScope, target protocol.FaceScope) bool {
		for _, scope := range scopes {
			if scope == target {
				return true
			}
		}
		return false
	}
	if contains(observer, protocol.FaceScopeRunsOutput) || !contains(operator, protocol.FaceScopeRunsOutput) {
		t.Fatalf("run output scope: observer=%v operator=%v", observer, operator)
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
	reviewEnabled := valid()
	reviewEnabled.Security.Review = config.SecurityReviewConfig{
		Enabled: true, Provider: "provider", Model: "model", TimeoutMS: 1500,
		MaxTokens: 256, PolicyVersion: "v1", Profile: "default",
	}
	if err := ValidateConfig(reviewEnabled); err != nil {
		t.Fatalf("valid security review config rejected: %v", err)
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
		"review missing model": func(cfg *config.Config) {
			cfg.Security.Review = config.SecurityReviewConfig{Enabled: true, TimeoutMS: 1500, MaxTokens: 256, PolicyVersion: "v1", Profile: "default"}
		},
		"review unknown model": func(cfg *config.Config) {
			cfg.Security.Review = config.SecurityReviewConfig{Enabled: true, Model: "missing", TimeoutMS: 1500, MaxTokens: 256, PolicyVersion: "v1", Profile: "default"}
		},
		"review provider mismatch": func(cfg *config.Config) {
			cfg.Security.Review = config.SecurityReviewConfig{Enabled: true, Provider: "other", Model: "model", TimeoutMS: 1500, MaxTokens: 256, PolicyVersion: "v1", Profile: "default"}
		},
		"review timeout": func(cfg *config.Config) {
			cfg.Security.Review = config.SecurityReviewConfig{Enabled: true, Model: "model", TimeoutMS: 99, MaxTokens: 256, PolicyVersion: "v1", Profile: "default"}
		},
		"review max tokens": func(cfg *config.Config) {
			cfg.Security.Review = config.SecurityReviewConfig{Enabled: true, Model: "model", TimeoutMS: 1500, MaxTokens: 0, PolicyVersion: "v1", Profile: "default"}
		},
		"review policy version": func(cfg *config.Config) {
			cfg.Security.Review = config.SecurityReviewConfig{Enabled: true, Model: "model", TimeoutMS: 1500, MaxTokens: 256, Profile: "default"}
		},
		"review profile": func(cfg *config.Config) {
			cfg.Security.Review = config.SecurityReviewConfig{Enabled: true, Model: "model", TimeoutMS: 1500, MaxTokens: 256, PolicyVersion: "v1"}
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

func TestValidateCompactConfig(t *testing.T) {
	valid := func() *config.Config {
		return &config.Config{
			Server: config.ServerConfig{Host: "127.0.0.1", Port: 15707},
			LLM: config.LLMConfig{
				DefaultProvider: "main", DefaultModel: "main-model",
				Providers: []config.ProviderCfg{
					{Name: "main", Adapter: "openai", BaseURL: "https://example.com/v1"},
					{Name: "summary", Adapter: "openai", BaseURL: "https://example.com/v1"},
				},
				Models: []config.ModelCfg{
					{ID: "main-model", Provider: "main", ContextWindow: 32768, MaxTokens: 2048},
					{ID: "summary-model", Provider: "summary", ContextWindow: 16384, MaxTokens: 2048},
				},
			},
			Compact: config.CompactCfg{
				Enabled: true, Automatic: true, Provider: "summary", Model: "summary-model",
				TimeoutMS: 30_000, MaxTokens: 1024, HighWatermark: .8, LowWatermark: .6,
				ProviderMarginTokens: 1024, MaxConcurrent: 1,
				RateLimitInitialBackoffMS: 5000, RateLimitMaxBackoffMS: 300_000,
				SummaryWarningNodes: 100, SummaryWarningBytes: 16 << 20,
				PolicyVersion: "compact-v1", Profile: "default",
			},
		}
	}
	if err := ValidateConfig(valid()); err != nil {
		t.Fatalf("valid compact config rejected: %v", err)
	}
	tests := map[string]func(*config.Config){
		"missing summary model": func(cfg *config.Config) { cfg.Compact.Model = "" },
		"provider mismatch":     func(cfg *config.Config) { cfg.Compact.Provider = "main" },
		"main budget":           func(cfg *config.Config) { cfg.LLM.Models[0].ContextWindow = 3000 },
		"summary budget":        func(cfg *config.Config) { cfg.LLM.Models[1].ContextWindow = 2048 },
		"watermarks":            func(cfg *config.Config) { cfg.Compact.LowWatermark = .9 },
		"timeout":               func(cfg *config.Config) { cfg.Compact.TimeoutMS = 999 },
		"max tokens":            func(cfg *config.Config) { cfg.Compact.MaxTokens = 64 },
		"max concurrent":        func(cfg *config.Config) { cfg.Compact.MaxConcurrent = 17 },
		"backoff":               func(cfg *config.Config) { cfg.Compact.RateLimitInitialBackoffMS = 999 },
		"policy":                func(cfg *config.Config) { cfg.Compact.PolicyVersion = "future" },
		"profile":               func(cfg *config.Config) { cfg.Compact.Profile = "verbose" },
		"automatic disabled":    func(cfg *config.Config) { cfg.Compact.Enabled = false },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := valid()
			mutate(cfg)
			if err := ValidateConfig(cfg); err == nil {
				t.Fatal("invalid compact config accepted")
			}
		})
	}
}
