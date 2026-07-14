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

# 测试 gateway-core
cd modules/gateway-core && go test -race -count=1 ./...
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
make test         # 运行 mind 模块全部测试
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
  - `docs:` 文档
  - `chore:` 杂项（注释、依赖、构建）
- commit message 用英文（方便国际协作），代码注释用中文
- 不提交 `DEEPSEEK_API_KEY`、`.env`、二进制文件

---

## 开发进度

### Phase 1 — Mind 核心 + Gateway 通信（完成度 ~85%）

#### ✅ 已完成

##### 工具系统（10 个工具）
| 工具 | 功能 |
|------|------|
| `read_file` | 读取文件，支持行号/行范围/字符偏移/双上限 |
| `write_file` | 创建/覆盖文件（DefaultConfirm 保护） |
| `edit_file` | 精确替换，唯一性检查 + 上下文提示 |
| `grep` | 文件内容搜索（字面量），glob 过滤 + 上下文行 |
| `grep_regex` | 正则搜索，同 grep 参数 |
| `list_files` | 递归遍历 + glob 过滤 |
| `exec_command` | 跨平台 Shell 执行（Unix: sh, Windows: cmd），可设超时 |
| `check_security` | 预查安全策略结果 |
| `view_skill` | 按名称加载技能全文 |
| `list_dir` | 平铺目录（已由 list_files 替代，保留兼容） |

##### 安全策略 (`internal/security/`)
- 四模式：strict / normal / trust / yolo
- 硬编码黑名单（rm -rf /、mkfs、fork 炸弹等）
- Normal 模式灰名单（rm、sudo、chmod 等 → 需确认）
- 全局 `security.Check()` 函数

##### 审批流程 (`agentcore`)
- `Approver` 接口 + REPL 实现（y/n/Y/N）
- 自动放行/拒绝列表（autoAllow / autoDeny）
- LLM 通过 `confirm: true` 参数主动请求确认（覆盖 trust/yolo）

##### 事件总线 (`internal/events/`)
- `Event` 结构体（ID / Session / Source / Level / Type / Data）
- `EventBus`：`Publish()`（异步）/ `PublishSync()`（同步）
- `Writer` 接口 + `ConsoleWriter`（终端） + `FileWriter`（JSON Lines）
- `WaitGroup` 确保 `Close()` 等待所有写入完成
- Core 已整合事件总线，不再直接 `fmt.Fprintf`

##### 环境初始化 (`internal/setup/`)
- `~/.half-pi/` 目录结构创建（编译时 OS 区分）
- 默认 `config.toml` 生成（0600 权限，不覆盖已有）
- 自动创建 skills/、data/、logs/ 子目录

##### 配置加载 (`internal/config/`)
- TOML 解析（`github.com/BurntSushi/toml`）
- Provider / Model 分离定义
  - `[[llm.providers]]`：name、adapter、base_url、api_key
  - `[[llm.models]]`：id、name、provider、capabilities、参数、价格
- 环境变量密钥覆盖：`LLM_{NAME}_API_KEY`
- `ResolveModel()` / `ResolveProvider()` 解析
- `Sanitized()` 脱敏导出

##### LLM 适配器 (`internal/llm/`)
- OpenAI 兼容适配器完整实现（DeepSeek、Groq、OpenRouter 等）
- Gemini / Anthropic 适配器骨架

##### 技能系统 (`internal/skill/`)
- `skill.Store`：加载、缓存、查询 `.skill.md` 文件
- 手写 frontmatter 解析（name/description/tags/version/author）
- `Index()` 生成技能目录，注入 system prompt
- `view_skill` 工具按名称加载全文
- 启动时从 `~/.half-pi/skills/` 自动加载

##### SQLite 持久化 (`internal/store/`)
- `session_groups` 表：工作区管理（work_dir、soul_path）
- `sessions` 表：会话管理（关联 group、soul_path 覆盖）
- `messages` 表：消息持久化（role、tool_id、tool_calls、seq）
- 完整 CRUD + 事务批量写入 + 级联删除
- 15 个单元测试 + race 覆盖

##### Gateway-core 通信层 (`modules/gateway-core/`)
- `protocol/`：Envelope 消息协议，Session 重放防护（单调序号），AAD 构造
- `wss/crypto`：AES-128-GCM 加解密 + Envelope 集成
- `wss/server`：HTTP → WebSocket 升级
- `wss/client`：ConnectAndRegister 完整握手 + Send/Read 封装
- `hub/`：Peer 管理、ServeWS 生命周期、Broadcast、重放防护
- 25+ 测试 + race 覆盖

##### 设计文档
- `docs/skill-design.md` — 技能系统设计
- `docs/skill-session-memory-design.md` — 技能/会话/记忆组织设计

#### ⏳ 待完成
- [ ] **Hand** 远程执行器（基于 gateway-core）
- [ ] **Face** 远程交互终端（TUI / IM Bot）
- [ ] 会话持久化接入 agentcore（当前仍为内存历史）
- [ ] LLM 适配器工厂（根据 `provider.adapter` 自动选择）
- [ ] Skill → 工作区集成（SessionGroup 过滤）
- [ ] `/compact` 上下文压缩

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
- 远程 Face 通过订阅同一条总线获取输出

### 2026-07-14：配置设计
- `[[llm.providers]]` 数组，每个提供商独立配置
- `[[llm.models]]` 数组，模型关联到提供商
- API key 在 provider 层级，支持环境变量覆盖
- adapter 字段决定使用哪个 LLM 适配器

### 2026-07-14：Gateway 协议设计
- Envelope 统一消息格式（MsgID/Type/SessionID/From/To/Seq/Payload）
- AES-128-GCM 应用层加密，AAD 绑定消息头防挪用
- 单调序号防重放，SessionID 标识连接
- Hub 管理连接生命周期，callback 驱动消息处理

### 2026-07-14：Skill 系统
- 文件系统存储，frontmatter + markdown 格式
- 启动时扫描 → system prompt 索引 → LLM 按需 view_skill
- 无数据库、无权限管理、无版本控制

---

## 下一步

1. **Hand** — 远程执行器（基于 gateway-core 连接 Mind，执行远程指令）
2. **Face** — 远程交互终端（Telegram Bot / TUI）
3. 会话持久化接入 agentcore
4. LLM 适配器工厂
