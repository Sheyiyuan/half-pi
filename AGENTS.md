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
│   ├── half-pi-core/    # 共享核心（executor/security/events/tools）
│   ├── half-pi-mind/    # Mind：智能核心 + WebSocket Hub
│   ├── half-pi-face/    # Face：用户交互（远程）
│   └── half-pi-hand/    # Hand：远程执行节点
```

**依赖规则：** cross-module import 只能导入路径匹配 `github.com/Sheyiyuan/half-pi/modules/<module>` 的包。
- `half-pi-core` 零外部依赖，提供纯类型和工具基础设施
- `half-pi-mind` 依赖 gateway-core + half-pi-core
- `half-pi-hand` 依赖 gateway-core + half-pi-core
- 所有本地模块通过 `replace` 指令指向本地路径

### internal/ 边界
`internal/` 下的包仅限同模块内 import。外部模块通过导出接口（如 `agentcore.Core`、`executor.ToolExecutor`）交互，不依赖 internal 实现细节。

### 工具注册
- 通用工具放 `modules/half-pi-core/tools/`，通过 `init()` + `executor.Register()` 自注册
- Mind 特有工具放 `internal/executor/local/tool_<name>.go`
- `Tool.Check` 用于执行前安全检查（可选）
- `Tool.DefaultConfirm` 为 true 时每次调用需用户确认
- `executor.Runner` 封装工具查找 → Check → 执行流程，Support `SkipChecks` 和 `ConfirmFunc`

### 安全策略集成
- 安全策略放 `modules/half-pi-core/security/`，不散落在各工具中
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
# 全部模块用 make
make test

# 单独模块
cd modules/half-pi-core && go test -race -count=1 ./...
cd modules/gateway-core && go test -race -count=1 ./...
cd modules/half-pi-mind && go test -race -count=1 ./...
cd modules/half-pi-hand && go test -race -count=1 ./...
```
- 测试必须带 `-race`

### 构建
```bash
make build        # 编译 mind/face/hand 到 bin/
make run-mind     # 启动 Mind REPL（WS Hub 在 127.0.0.1:15707/ws）
make run-hand     # 启动 Hand（默认用 ~/.half-pi/hand/config.toml）
make run-hand ARGS="--token <token> --application-key <key> --id <name>"  # Hand CLI 覆盖
make test         # 运行全部 4 个模块的测试
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

### Phase 1 — Mind 核心 + Gateway 通信（完成度 ~97%）

#### ✅ 已完成

##### 工具系统（16 个工具）
| 工具 | 位置 | 功能 |
|------|------|------|
| `read_file` | half-pi-core/tools | 读取文件，支持行号/行范围/字符偏移/双上限 |
| `write_file` | half-pi-core/tools | 创建/覆盖文件（DefaultConfirm 保护） |
| `edit_file` | half-pi-core/tools | 精确替换，唯一性检查 + 上下文提示 |
| `grep` | half-pi-core/tools | 文件内容搜索（字面量），glob 过滤 + 上下文行 |
| `grep_regex` | half-pi-core/tools | 正则搜索，同 grep 参数 |
| `list_files` | half-pi-core/tools | 递归遍历 + glob 过滤 |
| `exec_command` | half-pi-core/tools | 跨平台 Shell 执行（Unix: sh, Windows: cmd），可设超时 |
| `check_security` | mind/internal/executor/local | 预查安全策略结果 |
| `view_skill` | mind/internal/executor/local | 按名称加载技能全文 |
| `list_hands` | mind/internal/executor/local | 列出在线 Hand 及静态信息 |
| `get_hand_info` | mind/internal/executor/local | 查询 Hand 动态信息与可用工具 |
| `select_hand` | mind/internal/executor/local | 设置/查询当前会话默认 Hand |
| `use_hand` | mind/internal/executor/local | 在指定 Hand 上远程执行工具 |
| `get_hand_task` | mind/internal/executor/local | 查询或列出会话拥有的后台任务 |
| `read_hand_task_log` | mind/internal/executor/local | 分页读取 Hand 后台任务日志 |
| `cancel_hand_task` | mind/internal/executor/local | 取消 Hand 后台任务 |

##### 安全策略 (`half-pi-core/security/`)
- 四模式：strict / normal / trust / yolo
- 硬编码黑名单（rm -rf /、mkfs、fork 炸弹等）
- Normal 模式灰名单（rm、sudo、chmod 等 → 需确认）
- 全局 `security.Check()` 函数

##### 审批流程 (`agentcore`)
- 进程级 conversation Approval Broker，Face 与 REPL 共用同一首裁决和审计路径
- REPL 输入适配支持 y/n/Y/N，并可在远程 Face 先裁决时取消等待
- 自动放行/拒绝列表（autoAllow / autoDeny）
- LLM 通过 `confirm: true` 参数主动请求确认（覆盖 trust/yolo）

##### 事件总线 (`half-pi-core/events/`)
- `Event` 结构体（ID / Session / Source / Level / Type / Data）
- `EventBus`：`Publish()`（异步）/ `PublishSync()`（同步）
- `Writer` 接口 + `ConsoleWriter`（终端） + `FileWriter`（JSON Lines）
- `WaitGroup` 确保 `Close()` 等待所有写入完成

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
- `server.enabled` 控制 WS Hub 是否启动

##### LLM 适配器 (`internal/llm/`)
- OpenAI 兼容适配器完整实现（DeepSeek、Groq、OpenRouter 等）
- Gemini 适配器完整实现
- Anthropic Claude 适配器完整实现
- `llm.New(adapter, ...)` 工厂函数，根据配置中 `provider.adapter` 自动选择

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
- `approval_audits` 表：审批绑定、Face/REPL identity、decision、时间与参数摘要，不保存原始参数
- `hand_credentials` / `face_tokens`：分类型 token + application key、scope 与认证管理；旧 `hand_tokens` 不再认证
- 完整 CRUD + 事务批量写入 + 级联删除
- 20 个单元测试 + race 覆盖

##### Gateway-core 通信层 (`modules/gateway-core/`)
- `protocol/`：Envelope 消息协议，Session 重放防护（单调序号），AAD 构造
- `wss/crypto`：AES-128-GCM 加解密 + Envelope 集成
- `wss/server`：HTTP → WebSocket 升级
- `wss/client`：ConnectAndRegister 完整握手 + Send/Read 封装
- `hub/`：Peer 管理、ServeWS 生命周期、Broadcast、重放防护、OnDisconnect 回调
- 25+ 测试 + race 覆盖

##### Hand 远程执行器 (`modules/half-pi-hand/`)
- WebSocket 客户端连接 Mind Hub
- `executor.Runner` 驱动工具执行（Mind 审批 + Hand 侧最终权限过滤）
- RPC 消息收发（RPC → 执行工具 → RPCResult），支持 `timeout_ms`
- Unix `exec_command` 超时取消时杀整个进程组，避免 shell 子进程残留
- 6 个集成测试（正常执行、未知工具、安全拦截、权限拒绝、取消、远程超时）
- TOML 配置文件（`~/.half-pi/hand/config.toml`）
- CLI flag 覆盖 + 环境变量支持，默认连接 `ws://127.0.0.1:15707/ws`
- 工作目录切换、工具 allow/deny、输出上限、主动监控事件上报
- 断线自动重连：指数退避（1s → 2s → 4s → … → `hand.retry.max_backoff`，默认 60s）

##### Mind Hub 服务器
- HTTP/WS 服务器（`hub.Hub.ServeWS`）
- 两种启动模式：
  - **服务模式（默认）**：仅 WS Hub，日志写入 `~/.half-pi/logs/mind.log`，写 PID 文件，等待信号退出
  - **REPL 模式（`--repl`）**：WS Hub + 交互式 REPL，事件输出到终端
- `--version` 打印版本号
- Hand/Face 独立凭据表，token 与 application key 分离
- REPL 命令：`/hand add/list/remove`、`/peers`
- 连接/断开事件通过 EventBus 发布到日志/终端
- `server.enabled` 配置开关

##### Mind → Hand 远程执行闭环
- LLM 可通过 `list_hands` / `get_hand_info` 感知在线 Hand
- `select_hand` 将默认 Hand 持久化到 `sessions.active_hand`
- `use_hand` 以普通 Tool 形式等待 `RemoteRun` 终态，不改 Chat 主循环
- RPC 使用一次性 Approval 摘要绑定 run、Hand、工具和参数，Hand 始终保留本地最终守门
- 服务级 `remoteexec.Authority` 统一路由 accepted/rejected/result/cancel，registry 校验 Hand 和连接来源
- `remote_runs` / `remote_run_events` 持久化状态迁移，原始参数不进入审计表
- `rpc_progress` 提供有界 stdout/stderr 增量事件，不改变 run 状态和终态裁决
- `task_id == start_run_id`；start run 与 durable task 使用独立状态机
- Mind `remote_tasks` 保存脱敏快照，Hand SQLite + 受限日志文件保存 durable task；断线继续、重启 lost、不自动重跑
- LLM 工具与 `/hand task start|status|log|cancel` 复用同一 TaskService、审批、Authority 和审计路径

##### Face P1 只读 Gateway
- Conversation Manager 为每个 conversation 恢复独立 Core/RemoteBridge、mode、active Hand 和 history
- Face Gateway 按凭据 label 解析无秘密 identity，并要求其稳定 ID 匹配握手时绑定的 principal；每个 command 都重新执行 scope 和资源归属校验
- 支持 conversation list/create/rename/snapshot、subscribe、Hand list/get、run get、task list/get/log
- 快照合并 SQLite 历史、Registry 活跃 run 和 Mind task best-known 状态；历史 run 查询有 Store fallback
- 每个 Face 连接独立有界队列、单发送循环和单调 `event_seq`；队列满只断开慢 Face
- conversation、Hand、run、task 变化通过显式 domain hook 投影，不解析 EventBus 展示文本

##### Face P2 Chat 生命周期
- `(principal_id, request_id)` 进程级 registry 提供 Chat/cancel payload 绑定、终态 replay、冲突检测和有界保留
- 同一 conversation 只允许一个 active Chat，不同 conversation 的独立 Actor 可并发执行
- Face 断线不取消已 accepted Chat；重连后同 principal 重发相同请求可取得已有 accepted 或终态 result
- `face.chat.cancel` 传播到 Core、工具和 `use_hand`，等待远程 run 时复用 `rpc_cancel` 链路
- Chat/tool 生命周期使用结构化事件；工具参数只投影 SHA-256，不进入普通事件流
- `remote_runs.request_id` 持久化 Face request/run 关联，旧库迁移后 legacy run 保持空关联并兼容读取
- `llm.ScriptedProvider` 支持不依赖真实模型的确定性多轮工具 fixture

##### Face P3 异步审批与取消
- conversation Approval 对象绑定 approval/request/run/tool/args digest/expiry，首个合法裁决生效
- `approval.requested` / `approval.resolved`、pending snapshot、过期/重复/scope/归属检查均走结构化协议
- session allow/deny 保留在各 conversation Core，不跨 Actor；Broker 重启恢复将 pending 标记 cancelled
- `face.run.cancel` 只调用 `remoteexec.Authority`；`face.task.cancel` 同时要求 task read/cancel scope 并经 TaskService 对账
- Registry 每次 run 状态迁移投影 `remote_run.changed`；result/cancel 竞争保持唯一终态
- 加密集成测试覆盖 Face 审批 → `use_hand`、Approval actor/digest、参数篡改拒绝及 run/task cancel 落库

##### 设计文档
- `docs/face-protocol.md` — 统一 Face 协议设计（Web/TUI/IM/Headless Agent Face、鉴权、快照、审批和事件投影）
- `docs/ai-face-protocol.md` — AI/Headless Face 正式协议接入指南（P3 Mind runtime 可用，客户端待实现）
- `docs/remote-execution-closed-loop.md` — Mind → Hand 闭环架构设计（含进度流和持久化后台任务）
- `docs/remote-execution-implementation-plan.md` — 远程执行闭环实施与验收记录
- `docs/next-development-plan.md` — 当前 Face Alpha 主线与远程执行收尾计划
- `docs/archived/remote-execution.md` — 已归档的 Mind → Hand MVP 设计
- `docs/archived/mind-hand-mvp-followups.md` — 已归档的 MVP 设计债清单
- `docs/archived/architecture.md` — 完整系统架构设计（三层模型、术语定义、通信协议、安全审计）
- `docs/archived/mind-service-mode.md` — Mind 服务模式设计（默认后台，`--repl` 选交互）
- `docs/archived/provider-adapter.md` — LLM 适配器模式设计（内部格式、各厂商适配器细节）
- `docs/archived/skill-design.md` — 技能系统设计
- `docs/archived/skill-session-memory-design.md` — 技能/会话/记忆组织设计

#### ⏳ 待完成
- [ ] **Face P4** JSONL Headless 客户端已实现；人类终端 Face 与真实 Mind/Hand/Face 进程级 E2E 待完成
- [ ] Skill → 工作区集成（SessionGroup 过滤）
- [ ] `/compact` 上下文压缩
- [ ] Mind → Hand 外部验收 — 原生 Windows 运行 `scripts/test-windows.ps1`

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
- EventBus 是进程内观察总线；远程 Face 不直接注册为 Writer，而由 Face Gateway 做鉴权、过滤、有序投递和结构化事件投影

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

### 2026-07-14：共享核心模块
- 提取 `executor`、`security`、`events`、`tools` 到 `half-pi-core`
- Mind 和 Hand 共同依赖，避免代码重复
- `executor.Runner` 封装工具查找 + Check + DefaultConfirm + 执行流程
- 注册表加 `sync.RWMutex`，线程安全

### 2026-07-14：Hand 远程执行
- 基于 gateway-core `wss.Client` 连接 Mind Hub
- 使用 `executor.Runner{Confirm: nil}` 执行工具（nil → auto-deny 确认操作）
- RPC/RPCResult 协议：Mind 发 RPC，Hand 执行后回送 RPCResult
- 配置文件 `~/.half-pi/hand/config.toml`，优先级 CLI > 环境变量 > 文件

### 2026-07-14：Hand Token 管理
- SQLite `hand_credentials` 表，每 Hand 独立 token/application key；旧 `hand_tokens` fail closed
- REPL `/hand add <label>` 生成 32 字符 hex token
- `/hand list` / `/hand remove <id>` 管理
- `hub.OnHandshake` 回调验证 token
- `hub.OnDisconnect` 回调通知终端

### 2026-07-14：WS Hub 启动策略
- `server.enabled` 配置控制是否启动
- 连接/断开通过 EventBus 发布 `[HUB]` 事件
- `/peers` 命令查看在线设备

### 2026-07-16：Mind → Hand 远程执行 MVP
- 以四个 Mind 本地工具暴露远程执行能力，不改 `Core.Chat()` 主循环
- `list_hands` / `get_hand_info` / `select_hand` / `use_hand` 通过 `RemoteBridge` 注入 Hub、activeHand、pending call、审批函数
- RPC 增加 `timeout_ms`，Hand 执行时用该值派生 context；Unix 命令取消时杀进程组
- Hand 默认连接 Mind Hub `ws://127.0.0.1:15707/ws`，与 Mind 默认监听端口一致
- 当前协议为 MVP 版本：能完成一轮 RPC → 一次结果，暂不支持进度流、后台任务和显式 cancel RPC

### 2026-07-17：Mind 服务模式 + Hand 自动重连
- Mind 默认后台服务模式（仅 WS Hub），`--repl` 切换到交互 REPL
- 服务模式写 PID 文件（`~/.half-pi/mind.pid`），日志写 `~/.half-pi/logs/mind.log`，SIGINT 优雅退出
- Hand 断线自动重连，指数退避上限可配置（`hand.retry.max_backoff`，默认 60s）
- Hub 回调（握手鉴权、连接/断开、HandEvent）从 core 移到 main.go，服务模式不启动 Agent Core

### 2026-07-17：统一 Face 协议
- Web、TUI、IM 和 Headless Agent Face 共用同一正式协议，不建立测试旁路
- `Envelope.SessionID` 保持连接级防重放语义，Face payload 使用 `conversation_id` 表示持久化对话
- Face Gateway 负责 Face 鉴权、command 路由、快照、事件投影、有序发送和背压
- EventBus 保持进程内观察职责，SQLite 保持恢复和审计的权威来源
- Face token 与 Hand token 分离，审批能力通过显式 scope 授予
- Headless Agent Face 使用 JSONL，支持其他 Agent 和进程级 E2E 测试

### 2026-07-18：远程进度流与持久化后台任务
- `rpc_progress` 使用独立 run seq，单块 4 KiB、每 run 1 MiB/256 事件，允许有界丢弃但不影响终态
- `task_id == start_run_id`，start run 在 durable admission 后终止，后台 task 独立继续
- Hand 使用 SQLite 元数据和受限日志文件，任务跨重连继续；Hand 重启标记 lost，绝不自动重跑
- Mind 使用 `remote_tasks` 脱敏快照，重启标 stale，在线后 status 对账
- Hand token 绑定 Hand ID；旧未绑定 token fail closed，需重新生成

### 2026-07-18：Face P1 只读 Gateway
- 默认服务模式初始化 Conversation Manager 和 Face Gateway，REPL 与远程 Face 共用同一 Actor 工厂
- 所有已验证的同步查询遵循 accepted → result；snapshot 使用 accepted → snapshot；subscribe 安装过滤器后 accepted
- snapshot version 进程内单调；每连接事件序号和出站线序在同一锁内分配
- task cursor 使用随机进程密钥 HMAC 绑定 conversation、filter 和排序锚点，重启后旧 cursor fail closed
- Face 投影不包含 token、application key、原始工具参数、Hand 工作路径或原始内部错误
- Face 凭据删除后即使复用同一 label 创建新凭据，旧连接也不能继承新 principal 的 scopes

### 2026-07-18：Face P2 Chat 生命周期
- Chat 在 accepted 后异步运行，并恰好保存一个 succeeded/failed/cancelled/timed_out 终态
- registry 先处理相同请求 replay/conflict，再执行 conversation busy 仲裁；终态保留 10 分钟且最多 256 条
- Chat context 独立于 Face 连接生命周期，但显式 cancel 会贯穿 LLM、本地工具和远程 run
- terminal Chat event 在 result 前投递；断线或慢连接丢失终态时通过相同 request replay 恢复
- Core 的 tool hook 不依赖 debug，事件仅包含 tool、success 和规范参数摘要

### 2026-07-19：Face P3 异步审批与取消
- 唯一进程级 Approval Broker 统一 Face 与 REPL；每个 conversation Core 保留独立 session allow/deny
- 裁决在 SQLite 审计成功后进入 Face accepted 队列，再发布 resolved 事件并唤醒工具；首裁决、过期和重复状态原子化
- Approval 审计仅保存绑定摘要、identity、decision、reason 和时间，启动恢复将 pending 标记 cancelled
- 所有 `rpc_cancel` 只由 `remoteexec.Authority.CancelRun` 发送；Face/REPL/Chat 取消复用同一路径
- `face.task.cancel` 复用 TaskService，并在 Hand 确认后查询状态再投影结构化终态

---

## 下一步

1. 原生 Windows 运行取消验收脚本（外部环境）
2. **Face Alpha Runtime P4** — Headless Face 与进程级 E2E
3. `/compact` 上下文压缩与 Skill 工作区集成
