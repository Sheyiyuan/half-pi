// Package config Hand 配置文件的加载和环境变量覆盖。
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultServerURL 是 Mind Hub 的默认监听地址。
	DefaultServerURL = "ws://127.0.0.1:15707/ws"
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

// RetryConfig 断线重连策略。
type RetryConfig struct {
	MaxBackoff int `toml:"max_backoff"` // 最大退避间隔（秒，默认 60）
}

// HandConfig Hand 自身标识和运行参数。
type HandConfig struct {
	ID         string           `toml:"id"`
	WorkDir    string           `toml:"work_dir"`
	Permission PermissionConfig `toml:"permission"`
	Limits     LimitsConfig     `toml:"limits"`
	Monitors   []MonitorConfig  `toml:"monitors"`
	Retry      RetryConfig      `toml:"retry"`
}

// PermissionConfig 工具权限白名单/黑名单。
type PermissionConfig struct {
	AllowTools []string `toml:"allow_tools"` // 白名单，空 = 全部允许
	DenyTools  []string `toml:"deny_tools"`  // 黑名单，优先于 allow
}

// LimitsConfig 工具执行资源限制。
type LimitsConfig struct {
	MaxOutputSize int64 `toml:"max_output_size"` // 工具输出上限（字节），0 = 默认 1MB
}

// MonitorConfig 主动监控项配置。
type MonitorConfig struct {
	Name      string `toml:"name"`
	Interval  int    `toml:"interval"`
	Command   string `toml:"command"`
	Condition string `toml:"condition"`
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
		cfg.Server.URL = DefaultServerURL
	}

	if cfg.Hand.Limits.MaxOutputSize <= 0 {
		cfg.Hand.Limits.MaxOutputSize = 1 << 20
	}

	if cfg.Hand.Retry.MaxBackoff <= 0 {
		cfg.Hand.Retry.MaxBackoff = 60
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
url = "ws://127.0.0.1:15707/ws"
token = ""

[hand]
id = ""
work_dir = ""

[hand.permission]
allow_tools = []
deny_tools = []

[hand.limits]
max_output_size = 1048576

[hand.retry]
max_backoff = 60

# [[hand.monitors]]
# name = "disk_high"
# interval = 60
# command = "df / | awk 'NR==2{print $5}' | tr -d '%'"
# condition = "> 85"
`
	return os.WriteFile(path, []byte(defaultCfg), 0600)
}
