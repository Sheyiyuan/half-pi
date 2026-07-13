# Half-Pi 开发进度

## Phase 1 — Mind 核心（完成度 ~60%）

### ✅ 已完成

#### 工具系统 (`internal/executor/`)
- 工具注册表：`executor.Register()` + `init()` 自注册
- 类型定义：`Tool`、`ToolResult`、`ObjectSchema`、`PropertySchema`
- 安全检查 hook：`Tool.Check` 函数字段
- `DefaultConfirm`：工具可声明每次调用默认需用户确认
- 每个工具独立文件 `tool_<name>.go`
- 添加指南：`local/README.md`

#### 已实现工具
| 工具 | 功能 | 默认暴露 |
|------|------|----------|
| `read_file` | 读取文件内容 | ✅ |
| `list_dir` | 列出目录内容 | ✅ |
| `exec_command` | 执行 shell 命令 | ✅ |
| `check_security` | 预查安全策略结果 | ✅ |

#### 安全策略 (`internal/security/`)
- 四模式：strict / normal / trust / yolo
- 硬编码黑名单（rm -rf /、mkfs、fork 炸弹等）
- Normal 模式灰名单（rm、sudo、chmod 等 → 需确认）
- 全局 `security.Check()` 函数

#### 审批流程 (`agentcore`)
- `Approver` 接口 + REPL 实现
- y = 允许一次 / Y = 始终允许 / n = 拒绝一次 / N = 始终拒绝
- 自动放行/拒绝列表（autoAllow / autoDeny）
- LLM 通过 `confirm: true` 参数主动请求确认（覆盖 trust/yolo）

#### 事件总线 (`internal/events/`)
- `Event` 结构体（ID / Session / Source / Level / Type / Data）
- `EventBus`：Publish（异步）/ PublishSync（同步）
- `Writer` 接口 + `ConsoleWriter`（终端） + `FileWriter`（JSON Lines）
- `WaitGroup` 确保 Close() 等待所有写入完成
- 单元测试 + race 测试

#### 环境初始化 (`internal/setup/`)
- `~/.half-pi/` 目录结构创建（编译时 OS 区分）
  - Linux/macOS: `~/.half-pi/`
  - Windows: `%APPDATA%/half-pi/`
- 默认 `config.toml` 生成（不覆盖已有）
- 配置权限 0600

#### 配置加载 (`internal/config/`)
- TOML 解析（`github.com/BurntSushi/toml`）
- 模型/提供商定义结构
  - `[[llm.providers]]`：name、adapter、base_url、api_key
  - `[[llm.models]]`：id、name、provider、capabilities、参数、价格
- 环境变量密钥覆盖：`LLM_{NAME}_API_KEY`
- `ResolveModel()` / `ResolveProvider()` 解析
- `Sanitized()` 脱敏导出

### 🔄 进行中
- `main.go` 从 config 读取模型配置启动
- `go.work` 多模块（gateway-core、half-pi-face、half-pi-hand 骨架）

### ⏳ 待完成
- [ ] `config.Load()` 校验（必填字段检查）
- [ ] LLM 适配器自动选择（根据 `provider.adapter`）
- [ ] `store/` SQLite 实现（会话持久化）
- [ ] `session/` 会话管理
- [ ] `/compact` 上下文压缩
- [ ] `skill` 加载系统

## 架构决策记录

### 2026-07-13：工具注册表
- 所有工具通过 `init()` + `executor.Register()` 自动注册
- 注册表位于 `executor` 包，实现位于 `executor/local/`
- 弃用手动维护 `ToolDefs()` + switch 的路由

### 2026-07-13：安全审批
- 安全策略作为 `Tool.Check` hook，在 `agentcore` 层统一执行
- LLM 可通过 `confirm: true` 参数要求用户确认
- 审批不通过 exec_command 内的 `/force` hack，REPL 层直接交互

### 2026-07-13：事件系统
- 所有输出通过 EventBus，不再是 `fmt.Fprintf`
- REPL 交互消息用 `PublishSync` 保证顺序
- 诊断事件用 `Publish` 异步（不影响延迟）
- 远程 Face 通过订阅同一条总线获取输出

### 2026-07-14：配置设计
- `~~llm.providers` 数组，每个提供商独立配置
- `[[llm.models]]` 数组，模型关联到提供商
- API key 在 provider 层级，支持环境变量覆盖
- adapter 字段决定使用哪个 LLM 适配器

## 下一步

1. `store/` — SQLite 会话持久化
2. `llm` — 适配器工厂（按 adaper 字段选择）
3. Face — 远程交互终端
