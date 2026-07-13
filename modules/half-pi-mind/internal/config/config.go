// Package config handles TOML configuration loading.
package config

// Config holds the half-pi mind configuration.
type Config struct {
	Server  ServerConfig  `toml:"server"`
	AI      AIConfig      `toml:"ai"`
	Storage StorageConfig `toml:"storage"`
}

type ServerConfig struct {
	Host string `toml:"host"`
	Port int    `toml:"port"`
}

type AIConfig struct {
	Provider  string `toml:"provider"`
	Model     string `toml:"model"`
	MaxTokens int    `toml:"max_tokens"`
}

type StorageConfig struct {
	DataDir string `toml:"data_dir"`
	LogDir  string `toml:"log_dir"`
}

// Load 读取配置文件。
// TODO: 实现 TOML 解析，当前忽略 path 返回空配置。
func Load(path string) (*Config, error) {
	return &Config{}, nil
}
