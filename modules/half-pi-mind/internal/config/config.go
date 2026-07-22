// Package config handles TOML configuration loading.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// ── 配置结构 ──

type Config struct {
	Server   ServerConfig   `toml:"server" json:"server"`
	LLM      LLMConfig      `toml:"llm" json:"llm"`
	Compact  CompactCfg     `toml:"compact" json:"compact"`
	Security SecurityConfig `toml:"security" json:"security"`
	Storage  StorageConfig  `toml:"storage" json:"storage"`
}

// CompactCfg 配置会话上下文压缩。缺少整个 section 时保持禁用并使用安全默认值。
type CompactCfg struct {
	Enabled                   bool    `toml:"enabled" json:"enabled"`
	Automatic                 bool    `toml:"automatic" json:"automatic"`
	Provider                  string  `toml:"provider" json:"provider"`
	Model                     string  `toml:"model" json:"model"`
	TimeoutMS                 int     `toml:"timeout_ms" json:"timeout_ms"`
	MaxTokens                 int     `toml:"max_tokens" json:"max_tokens"`
	HighWatermark             float64 `toml:"high_watermark" json:"high_watermark"`
	LowWatermark              float64 `toml:"low_watermark" json:"low_watermark"`
	ReservedOutputTokens      int     `toml:"reserved_output_tokens" json:"reserved_output_tokens"`
	ProviderMarginTokens      int     `toml:"provider_margin_tokens" json:"provider_margin_tokens"`
	MaxConcurrent             int     `toml:"max_concurrent" json:"max_concurrent"`
	RateLimitInitialBackoffMS int     `toml:"rate_limit_initial_backoff_ms" json:"rate_limit_initial_backoff_ms"`
	RateLimitMaxBackoffMS     int     `toml:"rate_limit_max_backoff_ms" json:"rate_limit_max_backoff_ms"`
	SummaryWarningNodes       int     `toml:"summary_warning_nodes" json:"summary_warning_nodes"`
	SummaryWarningBytes       int64   `toml:"summary_warning_bytes" json:"summary_warning_bytes"`
	PolicyVersion             string  `toml:"policy_version" json:"policy_version"`
	Profile                   string  `toml:"profile" json:"profile"`
}

// DefaultCompactCfg 返回第一版 Compact 的默认配置。
func DefaultCompactCfg() CompactCfg {
	return CompactCfg{
		TimeoutMS: 30_000, MaxTokens: 2048,
		HighWatermark: 0.80, LowWatermark: 0.60,
		ProviderMarginTokens: 1024, MaxConcurrent: 1,
		RateLimitInitialBackoffMS: 5000, RateLimitMaxBackoffMS: 300_000,
		SummaryWarningNodes: 100, SummaryWarningBytes: 16 << 20,
		PolicyVersion: "compact-v1", Profile: "default",
	}
}

// SecurityConfig 配置内置安全组件。
type SecurityConfig struct {
	Review SecurityReviewConfig `toml:"review" json:"review"`
}

// SecurityReviewConfig 配置独立 AI Reviewer。
type SecurityReviewConfig struct {
	Enabled       bool   `toml:"enabled" json:"enabled"`
	Provider      string `toml:"provider" json:"provider"`
	Model         string `toml:"model" json:"model"`
	TimeoutMS     int    `toml:"timeout_ms" json:"timeout_ms"`
	MaxTokens     int    `toml:"max_tokens" json:"max_tokens"`
	PolicyVersion string `toml:"policy_version" json:"policy_version"`
	Profile       string `toml:"profile" json:"profile"`
}

type ServerConfig struct {
	Host    string `toml:"host" json:"host"`
	Port    int    `toml:"port" json:"port"`
	Enabled bool   `toml:"enabled" json:"enabled"`
}

type LLMConfig struct {
	DefaultProvider string        `toml:"default_provider" json:"default_provider"`
	DefaultModel    string        `toml:"default_model" json:"default_model"`
	Providers       []ProviderCfg `toml:"providers" json:"providers"`
	Models          []ModelCfg    `toml:"models" json:"models"`
}

type ProviderCfg struct {
	Name       string `toml:"name" json:"name"`
	Adapter    string `toml:"adapter" json:"adapter"`
	BaseURL    string `toml:"base_url" json:"base_url"`
	APIKey     string `toml:"api_key" json:"api_key"`
	ScriptPath string `toml:"script_path" json:"script_path"`
}

type ModelCfg struct {
	ID            string   `toml:"id" json:"id"`
	Name          string   `toml:"name,omitempty" json:"name,omitempty"`
	Provider      string   `toml:"provider" json:"provider"`
	Capabilities  []string `toml:"capabilities" json:"capabilities"`
	ContextWindow int      `toml:"context_window" json:"context_window"`
	MaxTokens     int      `toml:"max_tokens" json:"max_tokens"`
	Temperature   float64  `toml:"temperature" json:"temperature"`
	InputPrice    float64  `toml:"input_price_per_1k" json:"input_price_per_1k"`
	OutputPrice   float64  `toml:"output_price_per_1k" json:"output_price_per_1k"`
}

type StorageConfig struct {
	DataDir string `toml:"data_dir" json:"data_dir"`
	LogDir  string `toml:"log_dir" json:"log_dir"`
}

// ── 解析后的运行时配置 ──

// ResolvedProvider 是解析完成的提供商信息，可直接用于初始化 LLM 适配器。
type ResolvedProvider struct {
	Name       string
	Adapter    string
	BaseURL    string
	APIKey     string
	ScriptPath string
}

// ResolvedModel 是解析完成的模型信息。
type ResolvedModel struct {
	ID            string
	Name          string // 实际传入 API 的模型名
	Provider      string
	Adapter       string // 适配器类型
	Capabilities  []string
	ContextWindow int
	MaxTokens     int
	Temperature   float64
	InputPrice    float64
	OutputPrice   float64
	Endpoint      string // 解析后的 API 端点
	APIKey        string // 解析后的密钥
	ScriptPath    string // Scripted adapter 的 fixture 路径
}

// ── 配置加载 ──

func Load(path string) (*Config, error) {
	cfg := Config{Compact: DefaultCompactCfg()}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// 用环境变量覆盖 api_key：LLM_{NAME}_API_KEY
	for i := range cfg.LLM.Providers {
		if cfg.LLM.Providers[i].ScriptPath != "" && !filepath.IsAbs(cfg.LLM.Providers[i].ScriptPath) {
			cfg.LLM.Providers[i].ScriptPath = filepath.Clean(filepath.Join(filepath.Dir(path), cfg.LLM.Providers[i].ScriptPath))
		}
		envKey := fmt.Sprintf("LLM_%s_API_KEY", strings.ToUpper(strings.ReplaceAll(cfg.LLM.Providers[i].Name, "-", "_")))
		if v := os.Getenv(envKey); v != "" {
			cfg.LLM.Providers[i].APIKey = v
		}
	}

	return &cfg, nil
}

// ── 模型 / 提供商解析 ──

// ResolveProvider 按名称查找提供商。
func (c *Config) ResolveProvider(name string) (*ResolvedProvider, error) {
	for _, p := range c.LLM.Providers {
		if p.Name == name {
			if p.Adapter == "scripted" && p.ScriptPath == "" {
				return nil, fmt.Errorf("provider %s has no script_path set", name)
			}
			if p.Adapter != "scripted" && p.APIKey == "" {
				return nil, fmt.Errorf("provider %s has no api_key set", name)
			}
			return &ResolvedProvider{
				Name: p.Name, Adapter: p.Adapter, BaseURL: p.BaseURL,
				APIKey: p.APIKey, ScriptPath: p.ScriptPath,
			}, nil
		}
	}
	return nil, fmt.Errorf("provider not found in config: %s", name)
}

// ResolveModel 按 id 查找模型，同时解析其提供商信息。
func (c *Config) ResolveModel(id string) (*ResolvedModel, error) {
	var model *ModelCfg
	for i := range c.LLM.Models {
		if c.LLM.Models[i].ID == id {
			model = &c.LLM.Models[i]
			break
		}
	}
	if model == nil {
		return nil, fmt.Errorf("model not found in config: %s", id)
	}

	rp, err := c.ResolveProvider(model.Provider)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve provider %s for model %s: %w", id, model.Provider, err)
	}

	name := model.Name
	if name == "" {
		name = model.ID
	}

	return &ResolvedModel{
		ID:            model.ID,
		Name:          name,
		Provider:      model.Provider,
		Adapter:       rp.Adapter,
		Capabilities:  model.Capabilities,
		ContextWindow: model.ContextWindow,
		MaxTokens:     model.MaxTokens,
		Temperature:   model.Temperature,
		InputPrice:    model.InputPrice,
		OutputPrice:   model.OutputPrice,
		Endpoint:      rp.BaseURL,
		APIKey:        rp.APIKey,
		ScriptPath:    rp.ScriptPath,
	}, nil
}

// ── 脱敏 ──

func (c *Config) Sanitized() *Config {
	cp := *c

	// 深拷贝 Providers（脱敏 api_key）
	providers := make([]ProviderCfg, len(cp.LLM.Providers))
	for i, p := range cp.LLM.Providers {
		if p.APIKey != "" {
			p.APIKey = "<redacted>"
		}
		providers[i] = p
	}
	cp.LLM.Providers = providers

	// 深拷贝 Models（与 Providers 一致）
	models := make([]ModelCfg, len(cp.LLM.Models))
	copy(models, cp.LLM.Models)
	cp.LLM.Models = models

	return &cp
}
