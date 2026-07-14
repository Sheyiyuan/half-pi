// Package setup 初始化 half-pi 运行环境。
// 在用户目录下创建 .half-pi/ 目录结构，生成默认配置文件和数据库。
package setup

import (
	"fmt"
	"os"
	"path/filepath"
)

// Env 持有 half-pi 运行环境的路径。
type Env struct {
	HomeDir   string
	Config    string
	DataDir   string
	LogDir    string
	DBPath    string
	EventLog  string
	SkillsDir string
}

// Init 初始化环境：创建目录结构、写入默认配置。
// 已存在时不会覆盖，只确保目录存在。
func Init() (*Env, error) {
	halfPiDir, err := halfPiHome()
	if err != nil {
		return nil, err
	}

	env := &Env{
		HomeDir:   halfPiDir,
		DataDir:   filepath.Join(halfPiDir, "data"),
		LogDir:    filepath.Join(halfPiDir, "logs"),
		SkillsDir: filepath.Join(halfPiDir, "skills"),
		Config:    filepath.Join(halfPiDir, "config.toml"),
		DBPath:    filepath.Join(halfPiDir, "data", "half-pi.db"),
		EventLog:  filepath.Join(halfPiDir, "logs", "events.jsonl"),
	}

	// 创建目录
	for _, dir := range []string{env.HomeDir, env.DataDir, env.LogDir, env.SkillsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// 写入默认配置（不覆盖已存在的）
	if err := writeDefaultConfig(env.Config); err != nil {
		return nil, err
	}

	return env, nil
}

func writeDefaultConfig(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil // 文件已存在，不覆盖
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("failed to check config file: %w", err)
	}

	defaultCfg := `# half-pi 配置文件
[server]
host = "127.0.0.1"
port = 15707

[llm]
default_provider = "deepseek"
default_model = "ds-v4-flash"

  # ── Provider 定义 ──
  # name    内部标识
  # base_url  API 地址
  # api_key 密钥，可用环境变量 LLM_{NAME}_API_KEY 覆盖（如 LLM_DEEPSEEK_API_KEY）
[[llm.providers]]
name = "deepseek"
adapter = "openai"
base_url = "https://api.deepseek.com/v1"
api_key = ""

  # ── Model 定义 ──
  # id       配置标识（路由时使用）
  # name     传给 API 的实际模型名，省略时等于 id
  # provider 使用的 Provider name
[[llm.models]]
id = "ds-v4-flash"
name = "deepseek-v4-flash"
provider = "deepseek"
max_tokens = 384000
temperature = 0.3
`
	if err := os.WriteFile(path, []byte(defaultCfg), 0600); err != nil {
		return fmt.Errorf("failed to write default config: %w", err)
	}
	return nil
}
