// Package config handles TOML configuration loading.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// ── 配置结构 ──

type Config struct {
	Server  ServerConfig  `toml:"server"`
	LLM     LLMConfig     `toml:"llm"`
	Storage StorageConfig `toml:"storage"`
}

type ServerConfig struct {
	Host string `toml:"host"`
	Port int    `toml:"port"`
}

type LLMConfig struct {
	DefaultProvider string        `toml:"default_provider"`
	DefaultModel    string        `toml:"default_model"`
	Providers       []ProviderCfg `toml:"providers"`
	Models          []ModelCfg    `toml:"models"`
}

type ProviderCfg struct {
	Name    string `toml:"name"`
	Adapter string `toml:"adapter"`
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"`
}

type ModelCfg struct {
	ID           string   `toml:"id"`
	Name         string   `toml:"name,omitempty"`
	Provider     string   `toml:"provider"`
	Capabilities []string `toml:"capabilities"`
	MaxTokens    int      `toml:"max_tokens"`
	Temperature  float64  `toml:"temperature"`
	InputPrice   float64  `toml:"input_price_per_1k"`
	OutputPrice  float64  `toml:"output_price_per_1k"`
}

type StorageConfig struct {
	DataDir string `toml:"data_dir"`
	LogDir  string `toml:"log_dir"`
}

// ── 解析后的运行时配置 ──

// ResolvedProvider 是解析完成的提供商信息，可直接用于初始化 LLM 适配器。
type ResolvedProvider struct {
	Name    string
	Adapter string
	BaseURL string
	APIKey  string
}

// ResolvedModel 是解析完成的模型信息。
type ResolvedModel struct {
	ID           string
	Name         string   // 实际传入 API 的模型名
	Provider     string
	Adapter      string   // 适配器类型
	Capabilities []string
	MaxTokens    int
	Temperature  float64
	InputPrice   float64
	OutputPrice  float64
	Endpoint     string   // 解析后的 API 端点
	APIKey       string   // 解析后的密钥
}

// ── 配置加载 ──

func Load(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 用环境变量覆盖 api_key：LLM_{NAME}_API_KEY
	for i := range cfg.LLM.Providers {
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
			if p.APIKey == "" {
				return nil, fmt.Errorf("提供商 %s 未设置 api_key", name)
			}
			return &ResolvedProvider{
				Name:    p.Name,
				Adapter: p.Adapter,
				BaseURL: p.BaseURL,
				APIKey:  p.APIKey,
			}, nil
		}
	}
	return nil, fmt.Errorf("配置中未找到提供商: %s", name)
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
		return nil, fmt.Errorf("配置中未找到模型: %s", id)
	}

	rp, err := c.ResolveProvider(model.Provider)
	if err != nil {
		return nil, fmt.Errorf("模型 %s 的提供商 %s 解析失败: %w", id, model.Provider, err)
	}

	name := model.Name
	if name == "" {
		name = model.ID
	}

	return &ResolvedModel{
		ID:           model.ID,
		Name:         name,
		Provider:     model.Provider,
		Adapter:      rp.Adapter,
		Capabilities: model.Capabilities,
		MaxTokens:    model.MaxTokens,
		Temperature:  model.Temperature,
		InputPrice:   model.InputPrice,
		OutputPrice:  model.OutputPrice,
		Endpoint:     rp.BaseURL,
		APIKey:       rp.APIKey,
	}, nil
}

// ── 脱敏 ──

func (c *Config) Sanitized() *Config {
	cp := *c

	// 深拷贝 Providers（脱敏 api_key）
	providers := make([]ProviderCfg, len(cp.LLM.Providers))
	for i, p := range cp.LLM.Providers {
		if p.APIKey != "" {
			s := p.APIKey
			if len(s) > 4 {
				p.APIKey = s[:4] + "****"
			} else {
				p.APIKey = "****"
			}
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
