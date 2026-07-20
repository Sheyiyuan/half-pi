// Package config 加载 Face 客户端配置并校验长期凭据。
package config

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultServerURL 是 Mind Hub 的默认监听地址。
	DefaultServerURL = "ws://127.0.0.1:15707/ws"
	// ModeHeadless 是机器可消费的 JSONL 客户端模式。
	ModeHeadless = "headless"
	// ModeTUI 是全屏人类终端工作台模式。
	ModeTUI = "tui"
)

var faceLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Config 是 Face 客户端运行时配置。
type Config struct {
	Server ServerConfig `toml:"server"`
	Face   FaceConfig   `toml:"face"`
}

// ServerConfig 是 Mind 连接和凭据信息。
type ServerConfig struct {
	URL            string `toml:"url"`
	Token          string `toml:"token"`
	ApplicationKey string `toml:"application_key"`
}

// FaceConfig 是当前 Face 的稳定 label 和运行模式。
type FaceConfig struct {
	ID   string `toml:"id"`
	Mode string `toml:"mode"`
}

// DefaultPath 返回默认 Face 配置路径。
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "face.toml"
	}
	return filepath.Join(home, ".half-pi", "face", "config.toml")
}

// Load 加载配置，并使用环境变量覆盖连接信息和凭据。
func Load(path string) (*Config, error) {
	if err := secureConfigPath(path, false); err != nil {
		return nil, fmt.Errorf("secure config: %w", err)
	}
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if value := os.Getenv("HALF_PI_FACE_SERVER"); value != "" {
		cfg.Server.URL = value
	}
	if value := os.Getenv("FACE_TOKEN"); value != "" {
		cfg.Server.Token = value
	}
	if value := os.Getenv("HALF_PI_FACE_APPLICATION_KEY"); value != "" {
		cfg.Server.ApplicationKey = value
	}
	if value := os.Getenv("HALF_PI_FACE_ID"); value != "" {
		cfg.Face.ID = value
	}
	if value := os.Getenv("HALF_PI_FACE_MODE"); value != "" {
		cfg.Face.Mode = value
	}
	if cfg.Server.URL == "" {
		cfg.Server.URL = DefaultServerURL
	}
	if cfg.Face.Mode == "" {
		cfg.Face.Mode = ModeTUI
	}
	return &cfg, nil
}

// Validate 校验 Face 注册凭据和当前支持的运行模式。
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("Face config is required")
	}
	serverURL, err := url.Parse(c.Server.URL)
	if err != nil || serverURL.Host == "" || (serverURL.Scheme != "ws" && serverURL.Scheme != "wss") {
		return fmt.Errorf("server.url must be an absolute ws:// or wss:// URL")
	}
	if serverURL.User != nil {
		return fmt.Errorf("server.url must not contain user info")
	}
	if !faceLabelPattern.MatchString(c.Face.ID) {
		return fmt.Errorf("face.id must match [A-Za-z0-9][A-Za-z0-9._-]{0,63}")
	}
	if c.Face.Mode != ModeHeadless && c.Face.Mode != ModeTUI {
		return fmt.Errorf("face.mode must be %q or %q", ModeTUI, ModeHeadless)
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
	if c.Server.Token == c.Server.ApplicationKey {
		return fmt.Errorf("server.token and server.application_key must differ")
	}
	return nil
}

// WriteDefault 写入受限权限的默认配置，且不覆盖已有文件。
func WriteDefault(path string) error {
	if err := secureConfigPath(path, true); err != nil {
		return fmt.Errorf("secure config path: %w", err)
	}
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	content := `[server]
url = "ws://127.0.0.1:15707/ws"
token = ""
application_key = ""

[face]
id = ""
mode = "tui"
`
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return secureConfigPath(path, false)
}
