// Package config Hand 配置文件的加载和环境变量覆盖。
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config Hand 运行时配置。
type Config struct {
	Server ServerConfig `toml:"server"`
	Hand   HandConfig   `toml:"hand"`
}

// ServerConfig Mind 连接配置。
type ServerConfig struct {
	URL   string `toml:"url"`
	Token string `toml:"token"`
}

// HandConfig Hand 自身标识。
type HandConfig struct {
	ID string `toml:"id"`
}

// DefaultPath 返回默认配置文件路径。
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "hand.toml"
	}
	return filepath.Join(home, ".half-pi", "hand", "config.toml")
}

// Load 加载 TOML 配置文件，并用环境变量覆盖 token。
func Load(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if v := os.Getenv("HAND_TOKEN"); v != "" {
		cfg.Server.Token = v
	}

	if cfg.Server.URL == "" {
		cfg.Server.URL = "ws://localhost:8080/ws"
	}

	return &cfg, nil
}

// WriteDefault 写入默认配置文件（0600），已存在时不覆盖。
func WriteDefault(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	defaultCfg := `# half-pi Hand 配置文件
[server]
url = "ws://localhost:8080/ws"
token = ""

[hand]
id = ""
`
	return os.WriteFile(path, []byte(defaultCfg), 0600)
}
