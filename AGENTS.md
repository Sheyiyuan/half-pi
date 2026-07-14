# Half-Pi · 半派

面向 AI 和开发者的项目规范、约定与进度追踪。

---

## 代码风格

### Go 风格
- 遵循 `gofmt` / `go vet`，不做例外
- 零值语义优先：`var buf strings.Builder` 而非 `buf := new(strings.Builder)`
- 导出的函数名、类型名使用中文注释；内部实现的零散注释中英文皆可，以清晰为准
- error 包装使用 `fmt.Errorf("...: %w", err)`，不吞原始错误
- 不要为没必要的场景写 defensive copy；只在确实需要隔离时深拷贝

### 注释规范
- 包注释写在 `package` 声明上方紧邻行，格式：`// Package xxx 做什么。`
- 导出符号注释以符号名开头：`// Load 读取配置文件。`
- TODO 统一格式：`// TODO(user): 描述。` 或者 `// TODO: 描述（含上下文）。`

### import 分组
```
标准库

外部依赖

内部包（github.com/Sheyiyuan/half-pi/...）
```
用空行分隔，不加注释。

### 文件组织
- 一个文件一个核心概念（`tool_exec.go`、`tool_list_dir.go`）
- 工具注册文件放 `internal/executor/local/tool_<name>.go`，通过 `init()` 自注册

### 跨平台
- 用 build tag + 文件名区分平台，不用运行时 `if runtime.GOOS`
- Unix：`_unix.go` + `//go:build !windows`
- Windows：`_windows.go` + `//go:build windows`
- 原则：编译时决定实现，零运行时开销

---

## 项目约定

### 模块结构（go.work 工作区）
```
half-pi/
├── go.work
├── modules/
│   ├── gateway-core/    # 公共通信库（go.mod 独立）
│   ├── half-pi-mind/    # Mind：智能核心
│   ├── half-pi-face/    # Face：用户交互
│   └── half-pi-hand/    # Hand：远程执行
```

**依赖规则：** cross-module import 只能导入路径匹配 `github.com/Sheyiyuan/half-pi/modules/<module>` 的包。Mind 不依赖 Face/Hand，gateway-core 无内部依赖。

### internal/ 边界
`internal/` 下的包仅限同模块内 import。外部模块通过导出接口（如 `agentcore.Core`、`executor.ToolExecutor`）交互，不依赖 internal 实现细节。

### 工具注册
- 所有工具通过 `init()` + `executor.Register()` 自注册
- 每个工具一个文件，放 `internal/executor/local/tool_<name>.go`
- `Tool.Check` 用于执行前安全检查（可选）
- `Tool.DefaultConfirm` 为 true 时每次调用需用户确认

### 安全策略集成
- 安全策略放在 `internal/security/`，不散落在各工具中
- `exec_command` 工具的 `Check` hook 调用 `security.Check()`
- 新增敏感操作需同时更新 security 的黑/灰名单

### 事件发布
- Core 层用 `c.publish()`（内部封装，nil-safe）
- REPL / Face 层用 `bus.PublishSync()` 保证输出顺序
- 新增事件类型时在 `event.go` 中定义常量

---

## 测试与构建

### 运行测试
```bash
# 进入 mind 模块运行全部测试
cd modules/half-pi-mind && go test -race -count=1 ./...
```
- 测试必须带 `-race`
- 测试文件就近放（`*_test.go`），不与源码分离

### 测试原则
- 核心逻辑必须有测试（安全策略匹配、事件总线、配置解析）
- 使用 `t.TempDir()` 处理临时文件，不污染工作区
- 使用 `t.Setenv()` 隔离环境变量
- 幂等操作必须测两次（如 `Init`）
- 测试 writer 实现 `events.Writer` 接口用于注入

### 构建
```bash
make build        # 编译 mind/face/hand 到 bin/
make run-mind     # 启动 Mind REPL
make lint         # golangci-lint 检查
```

### 首次运行约定
- `setup.Init()` 生成默认 config.toml（`0600`），不覆盖已有配置
- 配置中 `api_key` 留空，提示用户用环境变量或直接编辑

---

## 提交规范

- 每条 commit 只做一件事
- 格式：`<type>: <简短描述>`
  - `feat:` 新功能
  - `fix:` 修 bug
  - `refactor:` 重构（无功能变化）
  - `chore:` 杂项（注释、依赖、构建）
- commit message 用英文（方便国际协作），代码注释用中文
- 不提交 `DEEPSEEK_API_KEY`、`.env`、二进制文件

---

## 开发进度

### Phase 1 — Mind 核心（完成度 ~70%）

#### ✅ 已完成

##### 工具系统 (`internal/executor/`)
- 工具注册表：`executor.Register()` + `init()` 自注册
- 类型定义：`Tool`、`ToolResult`、`ObjectSchema`、`PropertySchema`
- 安全检查 hook：`Tool.Check` 函数字段
- `DefaultConfirm`：工具可声明每次调用默认需用户确认
- 每个工具独立文件 `tool_<name>.go`

##### 已实现工具
| 工具 | 功能 |
|------|------|
| `read_file` | 读取文件内容 |
| `list_dir` | 列出目录内容 |
| `exec_command` | 执行 shell 命令（可设超时） |
| `check_security` | 预查安全策略结果 |

##### 安全策略 (`internal/security/`)
- 四模式：strict / normal / trust / yolo
- 硬编码黑名单（rm -rf /、mkfs、fork 炸弹等）
- Normal 模式灰名单（rm、sudo、chmod 等 → 需确认）
- 全局 `security.Check()` 函数

##### 审批流程 (`agentcore`)
- `Approver` 接口 + REPL 实现
- y = 允许一次 / Y = 始终允许 / n = 拒绝一次 / N = 始终拒绝
- 自动放行/拒绝列表（autoAllow / autoDeny）
- LLM 通过 `confirm: true` 参数主动请求确认（覆盖 trust/yolo）

##### 事件总线 (`internal/events/`)
- `Event` 结构体（ID / Session / Source / Level / Type / Data）
- `EventBus`：`Publish()`（异步）/ `PublishSync()`（同步）
- `Writer` 接口 + `ConsoleWriter`（终端） + `FileWriter`（JSON Lines）
- `WaitGroup` 确保 `Close()` 等待所有写入完成
- 单元测试 + race 测试
- Core 已整合事件总线，不再直接 `fmt.Fprintf(os.Stderr, ...)`

##### 环境初始化 (`internal/setup/`)
- `~/.half-pi/` 目录结构创建（编译时 OS 区分）
  - Linux/macOS: `~/.half-pi/`
  - Windows: `%APPDATA%/half-pi/`
- 默认 `config.toml` 生成（不覆盖已有）
- 配置权限 0600

##### 配置加载 (`internal/config/`)
- TOML 解析（`github.com/BurntSushi/toml`）
- Provider / Model 分离定义
  - `[[llm.providers]]`：name、adapter、base_url、api_key
  - `[[llm.models]]`：id、name、provider、capabilities、参数、价格
- 环境变量密钥覆盖：`LLM_{NAME}_API_KEY`
- `ResolveModel()` / `ResolveProvider()` 解析
- `Sanitized()` 脱敏导出
- `main.go` 已接入：`setup.Init()` → `config.Load()` → `ResolveModel()` → LLM 适配器

##### LLM 适配器 (`internal/llm/`)
- OpenAI 兼容适配器完整实现（DeepSeek、Groq、OpenRouter 等）
- Gemini / Anthropic 适配器骨架

#### 🔄 进行中
- 修复 `DefaultModel` 为空时的回退逻辑
- `go.work` 多模块（gateway-core、half-pi-face、half-pi-hand 骨架）

#### ⏳ 待完成
- [ ] `config.Load()` 参数校验（必填字段检查）
- [ ] LLM 适配器工厂（根据 `provider.adapter` 自动选择）
- [ ] `store/` SQLite 实现（会话持久化）
- [ ] `session/` 会话管理
- [ ] `/compact` 上下文压缩
- [ ] `skill` 加载系统

---

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
- `[[llm.providers]]` 数组，每个提供商独立配置
- `[[llm.models]]` 数组，模型关联到提供商
- API key 在 provider 层级，支持环境变量覆盖（环境变量优先级更高）
- adapter 字段决定使用哪个 LLM 适配器

### 2026-07-14：事件总线与 Core 集成
- `Bus` 字段注入到 Core，`publish()` 辅助方法 nil-safe
- `core.go` 移除 `"os"` import，不再直接写 stderr
- `ConsoleWriter` TypeToolResult 后保留空行，兼容旧格式
- `main.go` REPL 消息使用 `PublishSync`，保证提示符顺序

### 2026-07-14：配置从 stub 到可用
- `config.Load()` 从返回空结构体 → 完整 TOML 解析 + 环境变量覆盖
- `main.go` 接入：`setup.Init()` → `config.Load()` → `ResolveModel()` → `llm.NewOpenAI()`
- 删除了硬编码的 `DEEPSEEK_API_KEY` 环境变量读取

---

## 下一步

1. 修复 `DefaultModel` 回退逻辑 bug
2. `store/` — SQLite 会话持久化
3. `llm` — 适配器工厂（按 adapter 字段选择）
4. Face — 远程交互终端
