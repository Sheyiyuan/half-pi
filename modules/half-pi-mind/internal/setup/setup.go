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
		DBPath:    filepath.Join(halfPiDir, "db", "half-pi.db"),
		EventLog:  filepath.Join(halfPiDir, "logs", "events.jsonl"),
	}

	// 运行目录可能包含配置、数据库和凭据，统一限制为当前用户访问。
	for _, dir := range []string{env.HomeDir, env.DataDir, env.LogDir, env.SkillsDir, filepath.Dir(env.DBPath)} {
		if err := secureDirectory(dir); err != nil {
			return nil, fmt.Errorf("secure directory %s: %w", dir, err)
		}
	}

	// 写入默认配置（不覆盖已存在的）
	if err := writeDefaultConfig(env.Config); err != nil {
		return nil, err
	}

	return env, nil
}

func writeDefaultConfig(path string) error {
	if exists, err := secureOptionalRegular(path); err != nil {
		return fmt.Errorf("secure config file: %w", err)
	} else if exists {
		return nil
	}

	defaultCfg := `# half-pi 配置文件
[server]
enabled = true
host = "127.0.0.1"
port = 15707

[storage]
data_dir = ""
log_dir = ""

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

# Gemini adapter 示例：base_url 为 Gemini API 基础地址
# [[llm.providers]]
# name = "gemini"
# adapter = "gemini"
# base_url = "https://generativelanguage.googleapis.com/v1beta"
# api_key = ""

# Anthropic adapter 示例：
# [[llm.providers]]
# name = "anthropic"
# adapter = "anthropic"
# base_url = "https://api.anthropic.com"
# api_key = ""

# 确定性 Scripted adapter 示例（script_path 相对本配置文件解析）：
# [[llm.providers]]
# name = "fixture"
# adapter = "scripted"
# script_path = "fixtures/chat.json"

  # ── Model 定义 ──
  # id       配置标识（路由时使用）
  # name     传给 API 的实际模型名，省略时等于 id
  # provider 使用的 Provider name
[[llm.models]]
id = "ds-v4-flash"
name = "deepseek-v4-flash"
provider = "deepseek"
capabilities = []
max_tokens = 384000
temperature = 0.3
input_price_per_1k = 0.0
output_price_per_1k = 0.0
`
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("failed to write default config: %w", err)
	}
	if _, err := f.Write([]byte(defaultCfg)); err != nil {
		f.Close()
		return fmt.Errorf("failed to write default config: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close default config: %w", err)
	}
	return secureRegular(path)
}
