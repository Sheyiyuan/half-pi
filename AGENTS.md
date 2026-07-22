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
- 所有生产执行必须进入 `executor.ToolRuntime`；参数变换、schema 校验、freeze/digest、Authorizer、审计和执行不得拆成可绕过的两步
- `executor.ToolRuntime` 是唯一生产工具执行入口；不存在 `Runner` 或 `SkipChecks` 旁路

### 安全策略集成
- 安全策略放 `modules/half-pi-core/security/`，不散落在各工具中
- `exec_command` 同时提供兼容 `Check` 和基于会话克隆策略的 `PolicyCheck`
- 新增敏感操作需同时更新 security 的黑/灰名单
- 新增工具参数必须声明 Reviewer 投影策略；无法安全交给 Reviewer 的字段使用 `ReviewRequireUser`

### 事件发布
- Chat/model/tool/security 事实通过 lifecycle Registry 发布；`c.publish()` 只保留兼容调试展示
- REPL / Face 层用 `bus.PublishSync()` 保证输出顺序
- 新增事件类型时在 `event.go` 中定义常量
- EventBus 不承担 Guard 或必需审计；普通 lifecycle Observer 异步、有界、fail open

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
cd modules/half-pi-face && go test -race -count=1 ./...
cd modules/half-pi-hand && go test -race -count=1 ./...
```
- 测试必须带 `-race`

### 构建
```bash
make build        # 编译 mind/face/hand 到 bin/
make run-mind     # 启动 Mind REPL（WS Hub 在 127.0.0.1:15707/ws）
make run-hand     # 启动 Hand（默认用 ~/.half-pi/hand/config.toml）
make run-hand ARGS="--token <token> --application-key <key> --id <name>"  # Hand CLI 覆盖
make test         # 运行全部 5 个模块的测试
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

### Phase 1 — Mind 核心 + Gateway 通信（完成度 ~98%）

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
- 四模式：strict / normal / review / yolo；`trust`、`ai_review` 仅为读取兼容别名，持久化统一写 `review`
- 硬编码黑名单（rm -rf /、mkfs、fork 炸弹等）
- Normal 模式灰名单（rm、sudo、chmod 等 → 需确认）
- Review 模式使用独立 AI 请求，只能返回 allow / require_user；Reviewer 故障升级用户审批，必需审计故障 fail closed
- strict/hard deny 与 Hand 本地拒绝不能被 Reviewer 或用户审批覆盖

##### 审批流程 (`agentcore`)
- 进程级 conversation Approval Broker，Face 与 REPL 共用同一首裁决和审计路径
- REPL 输入适配支持 y/n/Y/N，并可在远程 Face 先裁决时取消等待
- 自动放行/拒绝列表（autoAllow / autoDeny）
- LLM 通过 `confirm: true` 参数主动请求确认（覆盖 review/yolo 的自动放行）

##### 统一 Lifecycle 与审计 (`half-pi-core/lifecycle` / Mind lifecycle)
- Message、Model、Assistant、Tool、Security Review、Approval 和 Chat 终态共享 Meta/Phase/Outcome 契约
- Guard / Transformer / Observer / Auditor 四类通道具有独立能力、排序、scope、timeout 和失败语义
- `ChatTransport` 只承担 Face 流传输与背压，不是 Hook；完整输出 Guard/Transformer 自动启用 buffered delivery
- Mind 和 Hand 各自使用 ToolRuntime/Authorizer；`use_hand` 通过一次性 `PreparedExternal` 绑定 run/Hand/tool/args/background digest
- `security_decisions` 与 `lifecycle_outbox` 同事务提交；dispatcher/retry/dead-letter 已实现，正式 consumer 留给插件 runtime
- Skill frontmatter `groups` allowlist、`IndexForGroup` 和 `GetForGroup` 保证 SessionGroup 隔离

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
- v2 四步握手：register 只公开 type/label，token + application key 共同派生 proof/C→S/S→C 密钥，HandInfo 位于加密 proof claims
- v1 与首帧携带 token/Info 的注册严格拒绝；registered 及全部业务 payload 强制加密
- `wss/server`：HTTP → WebSocket 升级
- `wss/client`：ConnectAndRegister 完整握手 + Send/Read 封装
- `hub/`：Peer 管理、ServeWS 生命周期、Broadcast、重放防护、OnDisconnect 回调
- 25+ 测试 + race 覆盖

##### Hand 远程执行器 (`modules/half-pi-hand/`)
- WebSocket 客户端连接 Mind Hub
- node-local `executor.ToolRuntime` + Hand Authorizer 驱动工具执行（Mind 审批证明 + Hand 侧最终权限过滤）
- RPC 消息收发（RPC → 执行工具 → RPCResult），支持 `timeout_ms`
- Unix `exec_command` 超时取消时杀整个进程组，避免 shell 子进程残留
- 6 个集成测试（正常执行、未知工具、安全拦截、权限拒绝、取消、远程超时）
- TOML 配置文件（`~/.half-pi/hand/config.toml`）
- CLI flag 覆盖 + 环境变量支持，默认连接 `ws://127.0.0.1:15707/ws`
- 工作目录切换、工具 allow/deny、输出上限、主动监控事件上报
- 断线自动重连：指数退避（1s → 2s → 4s → … → `hand.retry.max_backoff`，默认 60s）
- Windows `exec_cmd` / `exec_ps` 使用 Job Object 取消完整进程树；多架构交叉编译和原生 Windows 11 race 验收已通过

##### Mind Hub 服务器
- HTTP/WS 服务器（`hub.Hub.ServeWS`）
- 两种启动模式：
  - **服务模式（默认）**：仅 WS Hub，日志写入 `~/.half-pi/logs/mind.log`，写 PID 文件，等待信号退出
  - **REPL 模式（`--repl`）**：WS Hub + 交互式 REPL，事件输出到终端
- `--version` 打印版本号
- Hand/Face 独立凭据表，token 与 application key 分离
- REPL 命令：`/hand add/list/remove`、`/face add/list/remove`、`/peers`（复用本地管理服务）
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

##### Face P4 客户端与进程验收
- `half-pi-face` 默认提供人类终端模式，也可切换为 stdout 仅正式协议消息的 Headless JSONL 模式
- 两种客户端共用加密 `client.Connection`，不新增测试旁路或 wire 消息
- 终端 Face 支持 conversation、Chat/cancel、审批、Hand、run 和 task 操作，snapshot 后自动订阅当前 conversation
- 所有 Mind payload 在渲染前严格验证，嵌套 result 按 pending operation 校验，终端文本转义 C0/C1 控制字符
- 真实进程 E2E 使用动态端口、临时 HOME/SQLite/工作目录和 Scripted LLM，构建并运行 `-race` Mind/Hand/Face 二进制
- E2E 覆盖多 Face 持久化恢复、request replay/conflict、run-bound 审批、远程取消、后台 task 对账、终端 REPL 一致性与脱敏审计

##### Face 全屏 TUI
- Bubble Tea 单 reducer 全屏工作台，支持 Wide/Standard/Compact/Short 响应式布局、alternate screen、稳定 chat viewport 和多行 composer
- 默认本地新对话草稿，首次发送自动执行 create → subscribe → snapshot → Chat；delta 原地聚合，终态使用 Glamour Markdown
- conversation picker、独立草稿、消息分页、输入历史搜索、typed command registry、补全、palette 与键鼠路由
- Approvals/Runs/Tasks/Hands 权限感知 Activity；审批默认 deny once，foreground progress 与 durable task log 严格分离
- Connector 持有凭据，连接按 generation 隔离并自动退避重连；Chat 使用原 request ID replay，非幂等 mutation 只对账不自动重发
- `mode=tui` 非交互 stdin/stdout 明确失败并提示 `--mode headless`；旧行式 renderer 已删除，Headless JSONL 保持兼容

##### Mind 本地管理 CLI
- `half-pi-mind config init|path|validate|show`
- `half-pi-mind face add|list|remove`，支持 `--scopes` 或 `--profile observer|operator`
- `half-pi-mind hand add|list|remove`
- `half-pi-mind status` / `peers`
- Mind 运行时通过本地 IPC 调用进程内 `management.Service`；Mind 未运行时在取得状态锁后离线打开 Store
- service 与 REPL 在打开 Store 前持有 OS 状态锁；REPL `/face`、`/hand`、`/peers` 复用同一管理服务
- 新增 `management_audits`，凭据成功 mutation 与成功 audit 同事务提交，失败路径写无秘密失败审计
- Windows named pipe 使用 `go-winio`，状态锁使用 `LockFileEx`，新建 run/lock/pipe 的 DACL 仅允许当前用户 SID 和 SYSTEM
- Unix 真实进程 E2E 覆盖离线创建、在线 CRUD/立即断连、Hub disabled、REPL 一致性和 IPC fail closed；Windows `386/amd64/arm64` 已完成交叉编译（上游 SQLite 不支持 `windows/arm`）
- WinBoat Windows 11 Pro 原生验收已通过：七组 race 测试、named pipe handle DACL、`LockFileEx`、run/lock DACL、离线创建、在线 CRUD 和 Hub-disabled 管理链路均成功

##### 设计文档
- `docs/README.md` — 当前文档索引、归档入口与维护约定
- `docs/face-protocol.md` — 统一 Face 协议设计（Web/TUI/IM/Headless Agent Face、鉴权、快照、审批和事件投影）
- `docs/face-streaming-protocol.md` — Face revision 2 流式传输、背压、恢复和终态语义
- `docs/ai-face-protocol.md` — AI/Headless Face 正式协议接入指南（客户端、Mind runtime 与进程 E2E 可用）
- `docs/remote-execution-closed-loop.md` — Mind → Hand 闭环架构设计（含进度流和持久化后台任务）
- `docs/mind-management-cli.md` — Mind 本地管理 CLI、在线 IPC、离线 Store 与状态锁设计/实现记录
- `docs/lifecycle-hooks-and-security-audit.md` — 已实现的统一生命周期 Hook、隔离 Reviewer、安全审计与插件开放前置契约
- `docs/plugin-architecture.md` — 插件契约、Goja 宿主、process/WASM 运行时与实施顺序提案（尚未实现）
- `docs/archived/README.md` — 已完成、被替代或仅供决策追溯的设计文档索引

#### ⏳ 待完成
- [ ] `/compact` 上下文压缩
- [ ] 按 `docs/plugin-architecture.md` 实现首版插件 runtime
- [ ] Windows 原生凭据/config/database ACL 发布环境验收
- [ ] Windows ConPTY 与 macOS PTY 的全屏 TUI 原生发布验收

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
- 此决策中的执行编排已由 2026-07-22 ToolRuntime/lifecycle 决策取代；`Tool.Check` 仍作为确定性策略输入保留

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
- 无数据库、无版本控制；2026-07-22 增加 frontmatter `groups` SessionGroup 可见性过滤

### 2026-07-14：共享核心模块
- 提取 `executor`、`security`、`events`、`tools` 到 `half-pi-core`
- Mind 和 Hand 共同依赖，避免代码重复
- `executor.Runner` 封装工具查找 + Check + DefaultConfirm + 执行流程
- 注册表加 `sync.RWMutex`，线程安全
- Runner 的生产职责已由 2026-07-22 ToolRuntime 决策取代

### 2026-07-14：Hand 远程执行
- 基于 gateway-core `wss.Client` 连接 Mind Hub
- 使用 `executor.ToolRuntime` 执行工具，所有安全审批和生命周期 hook 均在统一入口完成
- RPC/RPCResult 协议：Mind 发 RPC，Hand 执行后回送 RPCResult
- 配置文件 `~/.half-pi/hand/config.toml`，优先级 CLI > 环境变量 > 文件

### 2026-07-14：Hand Token 管理
- SQLite `hand_credentials` 表，每 Hand 独立 token/application key；旧 `hand_tokens` fail closed
- REPL `/hand add <label>` 生成 32 字符 hex token
- `/hand list` / `/hand remove <id>` 管理
- `hub.OnHandshake` 按 type+label 读取双秘密并验证加密 proof
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

### 2026-07-19：Face P4 客户端与真实进程 E2E
- Headless JSONL 与人类终端 Face 共用正式加密连接和 `face.*` 协议，不建立测试旁路
- Mind 与 Hand 启动后分别输出结构化 `mind.ready` / `hand.ready`；进程错误与 race detector 失败使用非零退出码
- E2E 构建真实 `-race` 二进制，以动态端口和 Scripted LLM 验证跨 Face 恢复、审批、取消、后台任务与 SQLite 一致性
- `use_hand` 自行消费 `confirm`，确保远程审批绑定已生成的 run ID 和 RPC 参数摘要

### 2026-07-19：Windows 原生取消验收
- Windows 11 原生环境运行 `scripts/test-windows.ps1` 通过，完整 `half-pi-core/tools` race 测试和进程树取消专项测试均成功
- Windows Job Object 取消语义完成外部平台验收；Windows ACL 仍是独立发布环境检查项

### 2026-07-19：Mind 管理 CLI Windows 原生验收
- WinBoat Windows 11 Pro `AMD64`（build 26200）原生执行 `scripts/test-windows.ps1 -PrebuiltDir` 通过，七个 race 测试二进制全部 PASS 且 stderr 为空
- 原生链路覆盖 `go-winio` named pipe、handle DACL、动态 deadline、`LockFileEx`、run/lock DACL、离线 Face add、在线 Face/Hand CRUD、status 和 Hub-disabled peers
- 验收修复 Windows 交互终端测试注入、`go-winio` deadline 错误归一化、目录 DACL 继承和 PowerShell 非零 CLI 退出收集
- Windows `386/amd64/arm64` 继续保持交叉编译通过；`windows/arm` 因上游 `modernc.org/sqlite` 不支持而不纳入 Mind 构建矩阵

### 2026-07-19：Gateway v2 全程应用层加密
- 废弃首帧明文携带 token/HandInfo 的 v1 握手，旧版本和秘密字段严格 fail closed，不保留降级旁路
- register 仅公开 protocol version、peer type 和 label；Mind 按 type+label 查询分表凭据
- token 与 application key 共同参与 HKDF-SHA-256，按 transcript/challenge 派生 proof、C→S 和 S→C 三把 AES-128-GCM 密钥
- challenge 回显和 HandInfo 放入加密 proof claims；Face 必须省略 HandInfo，Hand 必须提供完整 HandInfo
- 原始 WebSocket 帧测试确认 token、application key、hostname 和 work_dir 不以明文出现；错误双秘密、角色字段和 transcript 篡改均拒绝
- WinBoat Windows 11 Pro `AMD64`（build 26200）原生运行 protocol/wss/hub/dispatcher race 测试和 Mind/Hand/Face v2 进程链路通过；Face 收到 version 2 encrypted registered，双 peer 在线与撤销断连均通过
- `scripts/test-windows.ps1` 已将 gateway protocol/hub/wss 与 Mind dispatcher 纳入多架构编译、原生 race 和 Prebuilt 门禁
- WinBoat 使用当前源码执行更新后的官方 `scripts/test-windows.ps1 -PrebuiltDir` 通过，11 组原生 race 测试全部 PASS，官方 stderr 为空

### 2026-07-22：统一 Lifecycle、ToolRuntime 与隔离 Reviewer
- `half-pi-core/lifecycle` 固定 Message/Model/Assistant/Tool/Security/Approval/Chat phase、Meta、Outcome 和 wire contract
- Hook 分为 Guard、Transformer、Observer、Auditor；Guard 只能单调收紧，Observer 异步有界且不能改变业务事实
- `executor.ToolRuntime` 成为唯一生产工具入口；freeze 后参数和工具定义不可失配，生产 `SkipChecks` 与远程预审降级路径删除
- Mind `review` 模式调用独立无工具 AI Reviewer，只接受 allow/require_user；`trust`/`ai_review` 只读兼容并自动迁移为 `review`
- `confirm: true` 与 `DefaultConfirm` 直接进入用户审批，Reviewer allow 不能覆盖强制审批、deterministic deny 或 Hand deny
- `ChatTransport` 取代 context 单值 `ChatHooks`；完整 assistant 策略自动缓冲，assistant 持久化和传输事实分离
- Mind `PreparedExternal` 与 Hand node-local ToolRuntime 保持双重守门，RemoteRun 持久化 trace/span，结果策略可抑制未过滤 progress 外投影
- `security_decisions` 与 `lifecycle_outbox` 事务提交，可靠 dispatcher/重试/dead-letter/retention 已具备；无正式插件 consumer 时不启动空 dispatcher
- Skill 使用 frontmatter `groups`、`IndexForGroup`、`GetForGroup` 落实 SessionGroup 可见性
- 插件 runtime 不在本决策内，后续以 `docs/plugin-architecture.md` 为实施入口

---

## 下一步

1. 实现 `/compact` 上下文压缩
2. 讨论并实现首版插件 runtime
3. 在发布环境验收 Windows 凭据/config/database ACL
4. 完成 Windows ConPTY 与 macOS PTY 的全屏 TUI 原生发布验收
