// Package config Hand 配置文件的加载和环境变量覆盖。
package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultServerURL 是 Mind Hub 的默认监听地址。
	DefaultServerURL = "ws://127.0.0.1:15707/ws"
)

var handLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Config Hand 运行时配置。
type Config struct {
	Server ServerConfig `toml:"server"`
	Hand   HandConfig   `toml:"hand"`
}

// ServerConfig Mind 连接配置。
type ServerConfig struct {
	URL            string `toml:"url"`
	Token          string `toml:"token"`
	ApplicationKey string `toml:"application_key"`
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
	Tasks      TasksConfig      `toml:"tasks"`
}

// TasksConfig 后台任务持久化和配额配置。
type TasksConfig struct {
	Dir         string `toml:"dir"`
	MaxRunning  int    `toml:"max_running"`
	MaxRuntime  string `toml:"max_runtime"`
	MaxLogBytes int64  `toml:"max_log_bytes"`
	Retention   string `toml:"retention"`
	MaxRetained int    `toml:"max_retained"`
}

// MaxRuntimeDuration 返回后台任务最长运行时间。
func (c TasksConfig) MaxRuntimeDuration() time.Duration {
	d, _ := time.ParseDuration(c.MaxRuntime)
	return d
}

// RetentionDuration 返回后台任务保留时间。
func (c TasksConfig) RetentionDuration() time.Duration {
	d, _ := time.ParseDuration(c.Retention)
	return d
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

// Load 加载 TOML 配置文件，并用环境变量覆盖凭据。
func Load(path string) (*Config, error) {
	if err := secureConfigPath(path, false); err != nil {
		return nil, fmt.Errorf("secure config: %w", err)
	}
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if v := os.Getenv("HAND_TOKEN"); v != "" {
		cfg.Server.Token = v
	}
	if v := os.Getenv("HALF_PI_HAND_APPLICATION_KEY"); v != "" {
		cfg.Server.ApplicationKey = v
	}

	if cfg.Server.URL == "" {
		cfg.Server.URL = DefaultServerURL
	}

	if cfg.Hand.Limits.MaxOutputSize <= 0 {
		cfg.Hand.Limits.MaxOutputSize = 1 << 20
	} else if cfg.Hand.Limits.MaxOutputSize > 1<<20 {
		return nil, fmt.Errorf("hand.limits.max_output_size must not exceed 1048576")
	}

	if cfg.Hand.Retry.MaxBackoff <= 0 {
		cfg.Hand.Retry.MaxBackoff = 60
	}
	if cfg.Hand.Tasks.Dir == "" {
		cfg.Hand.Tasks.Dir = filepath.Join(filepath.Dir(path), "tasks")
	}
	if cfg.Hand.Tasks.MaxRunning <= 0 {
		cfg.Hand.Tasks.MaxRunning = 4
	}
	if cfg.Hand.Tasks.MaxRuntime == "" {
		cfg.Hand.Tasks.MaxRuntime = "24h"
	}
	maxRuntime, err := time.ParseDuration(cfg.Hand.Tasks.MaxRuntime)
	if err != nil || maxRuntime <= 0 || maxRuntime > 24*time.Hour {
		return nil, fmt.Errorf("hand.tasks.max_runtime must be between 1ns and 24h")
	}
	if cfg.Hand.Tasks.MaxLogBytes <= 0 {
		cfg.Hand.Tasks.MaxLogBytes = 1 << 20
	}
	if cfg.Hand.Tasks.Retention == "" {
		cfg.Hand.Tasks.Retention = "168h"
	}
	retention, err := time.ParseDuration(cfg.Hand.Tasks.Retention)
	if err != nil || retention <= 0 {
		return nil, fmt.Errorf("hand.tasks.retention must be positive")
	}
	if cfg.Hand.Tasks.MaxRetained <= 0 {
		cfg.Hand.Tasks.MaxRetained = 1000
	}

	return &cfg, nil
}

// ValidateCredentials 校验 Hand 注册所需的不可变三要素。
func (c *Config) ValidateCredentials() error {
	if !handLabelPattern.MatchString(c.Hand.ID) {
		return fmt.Errorf("hand.id must match [A-Za-z0-9][A-Za-z0-9._-]{0,63}")
	}
	for name, value := range map[string]string{
		"server.token":           c.Server.Token,
		"server.application_key": c.Server.ApplicationKey,
	} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != value {
			return fmt.Errorf("%s must be 32 lowercase hex characters", name)
		}
	}
	return nil
}

// WriteDefault 写入默认配置文件（0600），已存在时不覆盖。
func WriteDefault(path string) error {
	if err := secureConfigPath(path, true); err != nil {
		return fmt.Errorf("secure config path: %w", err)
	}
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	defaultCfg := `# half-pi Hand 配置文件
[server]
url = "ws://127.0.0.1:15707/ws"
token = ""
application_key = ""

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

[hand.tasks]
dir = ""
max_running = 4
max_runtime = "24h"
max_log_bytes = 1048576
retention = "168h"
max_retained = 1000

# [[hand.monitors]]
# name = "disk_high"
# interval = 60
# command = "df / | awk 'NR==2{print $5}' | tr -d '%'"
# condition = "> 85"
`
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(defaultCfg)); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return secureConfigPath(path, false)
}
