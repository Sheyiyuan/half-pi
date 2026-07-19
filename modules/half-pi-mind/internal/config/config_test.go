package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProvider(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{
			Providers: []ProviderCfg{
				{Name: "deepseek", BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-test"},
			},
		},
	}

	rp, err := cfg.ResolveProvider("deepseek")
	if err != nil {
		t.Fatalf("ResolveProvider 失败: %v", err)
	}
	if rp.BaseURL != "https://api.deepseek.com/v1" {
		t.Errorf("BaseURL = %q", rp.BaseURL)
	}
	if rp.APIKey != "sk-test" {
		t.Errorf("APIKey = %q", rp.APIKey)
	}
}

func TestResolveProviderMissing(t *testing.T) {
	cfg := &Config{}
	_, err := cfg.ResolveProvider("nonexistent")
	if err == nil {
		t.Error("期望错误，得到 nil")
	}
}

func TestResolveProviderEmptyKey(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{
			Providers: []ProviderCfg{
				{Name: "deepseek", BaseURL: "https://api.deepseek.com/v1"},
			},
		},
	}
	_, err := cfg.ResolveProvider("deepseek")
	if err == nil {
		t.Error("空 api_key 应报错")
	}
}

func TestLoadResolvesScriptedProviderPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	config := `[llm]
default_model = "fixture"

[[llm.providers]]
name = "fixture"
adapter = "scripted"
script_path = "fixtures/chat.json"

[[llm.models]]
id = "fixture"
provider = "fixture"
`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	model, err := cfg.ResolveModel("fixture")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "fixtures", "chat.json")
	if model.Adapter != "scripted" || model.ScriptPath != want || model.APIKey != "" {
		t.Fatalf("resolved model = %+v", model)
	}
}

func TestResolveProviderDoesNotUseScriptPathToBypassCredentials(t *testing.T) {
	for _, provider := range []ProviderCfg{
		{Name: "scripted", Adapter: "scripted"},
		{Name: "openai", Adapter: "openai", ScriptPath: "fixture.json"},
	} {
		cfg := &Config{LLM: LLMConfig{Providers: []ProviderCfg{provider}}}
		if _, err := cfg.ResolveProvider(provider.Name); err == nil {
			t.Fatalf("ResolveProvider(%+v) succeeded", provider)
		}
	}
}

func TestResolveModel(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{
			Providers: []ProviderCfg{
				{Name: "deepseek", BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-test"},
			},
			Models: []ModelCfg{
				{ID: "ds-v4-flash", Name: "deepseek-v4-flash", Provider: "deepseek", MaxTokens: 8192, Temperature: 0.3},
			},
		},
	}

	rm, err := cfg.ResolveModel("ds-v4-flash")
	if err != nil {
		t.Fatalf("ResolveModel 失败: %v", err)
	}
	if rm.Name != "deepseek-v4-flash" {
		t.Errorf("Name = %q", rm.Name)
	}
	if rm.Endpoint != "https://api.deepseek.com/v1" {
		t.Errorf("Endpoint = %q", rm.Endpoint)
	}
	if rm.APIKey != "sk-test" {
		t.Errorf("APIKey = %q", rm.APIKey)
	}
	if rm.MaxTokens != 8192 {
		t.Errorf("MaxTokens = %d", rm.MaxTokens)
	}
}

func TestResolveModelNameFallback(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{
			Providers: []ProviderCfg{
				{Name: "qwen", BaseURL: "https://dashscope.aliyuncs.com", APIKey: "sk-qwen"},
			},
			Models: []ModelCfg{
				{ID: "qwen3.7-plus", Provider: "qwen"},
			},
		},
	}

	rm, err := cfg.ResolveModel("qwen3.7-plus")
	if err != nil {
		t.Fatalf("ResolveModel 失败: %v", err)
	}
	// name 省略时等于 id
	if rm.Name != "qwen3.7-plus" {
		t.Errorf("Name = %q, 期望回退到 id", rm.Name)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("LLM_DEEPSEEK_API_KEY", "sk-from-env")

	cfg := &Config{
		LLM: LLMConfig{
			Providers: []ProviderCfg{
				{Name: "deepseek", BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-config"},
			},
		},
	}

	// Load 应用环境变量覆盖
	// 模拟 Load 行为
	for i := range cfg.LLM.Providers {
		if cfg.LLM.Providers[i].Name == "deepseek" {
			cfg.LLM.Providers[i].APIKey = os.Getenv("LLM_DEEPSEEK_API_KEY")
		}
	}

	rp, err := cfg.ResolveProvider("deepseek")
	if err != nil {
		t.Fatalf("ResolveProvider 失败: %v", err)
	}
	if rp.APIKey != "sk-from-env" {
		t.Errorf("APIKey = %q, 期望环境变量覆盖", rp.APIKey)
	}
}

func TestSanitized(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{
			Providers: []ProviderCfg{
				{Name: "deepseek", BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-abcdef123456"},
			},
		},
	}

	cp := cfg.Sanitized()
	if cp.LLM.Providers[0].APIKey != "<redacted>" {
		t.Errorf("脱敏结果 = %q", cp.LLM.Providers[0].APIKey)
	}
	if cfg.LLM.Providers[0].APIKey != "sk-abcdef123456" {
		t.Error("原始配置被修改")
	}
}
