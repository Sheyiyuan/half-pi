# Mind 本地管理 CLI 与离线配置设计

## 状态

本文是 Mind 本地管理面的实施设计，状态为 **已实现并完成 Unix/Windows 验收**。它解决 Face/Hand 凭据只能通过 Mind REPL 创建和撤销、默认 service mode 缺少管理入口的问题。

已落地：`management.Service`、`management_audits`、状态锁、本地 IPC、`half-pi-mind config|face|hand|status|peers` CLI、真实进程 E2E，以及 REPL 管理命令复用同一服务。Windows named pipe、状态锁与 DACL 已完成代码、`386/amd64/arm64` 交叉编译和 WinBoat Windows 11 原生验收；上游 `modernc.org/sqlite` 不支持 `windows/arm`。现有凭据/config/database 的全面 ACL 加固仍属于独立发布任务。

### 验收快照（2026-07-19）

- 功能代码与 Unix/Windows 验收已完成。
- 五个 Go 模块的 `make test` 已使用 `-race -count=1` 全部通过，管理真实进程 E2E 包含在常规测试中。
- `make build`、五模块 `go vet ./...`、`git diff --check` 均通过。
- Windows `386`、`amd64`、`arm64` 的 Mind 命令、named pipe、状态锁和 setup 测试代码交叉编译通过。
- WinBoat Windows 11 Pro `AMD64`（build 26200）原生运行 `scripts/test-windows.ps1 -PrebuiltDir` 通过；七组 race 测试全部 PASS、stderr 为空，并完成 named pipe handle DACL、`LockFileEx`、run/lock DACL、离线创建、在线 CRUD 与 Hub-disabled 进程链路。

管理 CLI 本身不复用或扩展 Face/Hand wire protocol，不引入远程管理 HTTP API，也不改变 Mind 默认以 service mode 启动的行为。Face/Hand 当前使用独立的 v2 应用层加密握手；现有 REPL 命令继续保留，但改为复用本文定义的管理服务。

相关文档：

- [`archived/face-core-closure-plan.md`](archived/face-core-closure-plan.md)：Face/Hand 凭据、scope 和即时撤销边界的已完成实施规格。
- [`face-protocol.md`](face-protocol.md)：Face identity、scope 和连接生命周期。
- [`archived/mind-service-mode.md`](archived/mind-service-mode.md)：Mind service mode 与 PID 文件的历史设计。

## 背景

当前 Mind 默认以 service mode 运行，REPL 需要通过 `--repl` 显式启用。Face 和 Hand 凭据管理却只存在于 REPL：

```text
/hand add|list|remove
/face add|list|remove
```

这导致以下问题：

- 首次配置终端/Headless Face 必须临时启动 REPL。
- service mode 运行期间无法通过稳定 CLI 创建、查看或撤销凭据。
- REPL 同时承担聊天、调试和管理职责，边界不清晰。
- 直接修改 SQLite 虽然可能写入成功，但无法保证在线 peer 立即断开，也绕过统一验证和审计。
- 现有 `mind.pid` 只在 service mode 写入，不覆盖 REPL，且 PID 文件本身不提供进程互斥。

管理能力必须同时覆盖三种状态：

```text
Mind 运行且 Hub 启用
Mind 运行但 Hub 禁用
Mind 完全未运行
```

## 设计目标

- 使用同一个 `half-pi-mind` 二进制提供稳定、可脚本化的本地管理命令。
- Mind 运行时，CLI 通过受操作系统保护的本地 IPC 调用进程内管理服务。
- Mind 未运行时，凭据命令在取得独占状态锁后直接使用同一 Store 和业务服务。
- Hub 是否启用不影响本地管理 IPC；Hub 只影响 peer 查询和撤销后的即时断连。
- REPL、在线 CLI 和离线 CLI 复用同一套凭据验证、scope 规范化、审计和错误语义。
- 创建凭据时只显示一次 token 和 application key；list、日志、错误和审计永不显示秘密。
- Unix 和 Windows 使用 build tag 提供各自的 IPC、状态锁和权限实现。
- 所有命令提供稳定退出码和机器可读 JSON 输出。

## 非目标

- 不提供远程管理端口、Web 管理页或公网管理 API。
- 不让 Face/Hand 通过业务 WebSocket 自助创建或提升自己的凭据。
- 不在第一阶段实现任意 TOML 路径的 `config set`。
- 不自动编辑另一台设备上的 Face/Hand 配置文件。
- 不自动启动 Mind 或 Hub 来完成离线命令。
- 不用 PID 存活探测代替操作系统文件锁。
- 不允许 CLI 在无法确认 Mind 状态时强行修改 SQLite。

## 术语

| 术语 | 含义 |
|---|---|
| Mind runtime | 正在运行的 `half-pi-mind` 进程，包括 service 和 REPL mode |
| Hub | 可选的 WebSocket Hub，由 `server.enabled` 控制 |
| 管理服务 | Mind 进程内执行配置查询、凭据 CRUD、状态和 peer 查询的服务 |
| 管理 IPC | 当前用户可访问的 Unix socket 或 Windows named pipe |
| 在线管理 | CLI 通过管理 IPC 调用正在运行的 Mind |
| 离线管理 | Mind 未运行，CLI 取得状态锁后直接打开 Store |
| 状态锁 | 覆盖 Mind runtime 和离线 Store 写入者的 OS 级独占锁 |

## 核心决策

| 主题 | 决策 |
|---|---|
| CLI 载体 | 继续使用 `half-pi-mind`，增加子命令，不新增二进制 |
| 默认行为 | 无子命令仍启动 Mind service；`--repl` 行为保持 |
| 在线入口 | 本地 IPC，不复用 Face/Hand WebSocket |
| Hub 依赖 | 管理 IPC 独立于 Hub，`server.enabled=false` 时仍启动 |
| 离线入口 | 仅在成功取得状态锁后允许打开 Store |
| 互斥权威 | OS 文件锁；PID 和锁文件内容只用于诊断 |
| 业务逻辑 | REPL、IPC 和离线 CLI 共用 `management.Service` |
| 撤销顺序 | 先提交数据库撤销与审计，再断开匹配 peer |
| 输出秘密 | 仅 add 成功响应包含，list/show/status/audit 均不包含 |
| 配置查看 | `config show` 始终脱敏，不提供显示原始密钥的开关 |
| 远程管理 | 本阶段禁止 |

## CLI 命令面

### 兼容现有启动方式

```bash
half-pi-mind
half-pi-mind --repl
half-pi-mind --version
```

根命令在识别到 `config`、`face`、`hand`、`status` 或 `peers` 时进入管理 CLI；否则沿用现有启动参数解析。未知子命令返回 usage 错误，不回退为启动服务。

### 配置命令

```bash
half-pi-mind config init
half-pi-mind config path
half-pi-mind config validate
half-pi-mind config show [--format toml|json]
```

语义：

- `config init`：创建默认目录和配置，不覆盖已有文件，重复执行成功。
- `config path`：输出实际使用的配置、数据库、日志、状态锁和管理端点位置，不读取秘密。
- `config validate`：检查路径安全、TOML 语法、provider/model 引用、默认模型、adapter 必填字段和 server 配置；不发起模型网络请求。
- `config show`：输出解析环境变量覆盖后的脱敏配置；API key 只显示固定脱敏形式。

第一阶段不实现通用 `config set`。任意 TOML 修改涉及数组、注释保留、原子替换和密钥输入安全，应在独立设计中处理。

### Face 凭据命令

```bash
half-pi-mind face add <label> --scopes <comma-separated-scopes>
half-pi-mind face add <label> --profile <observer|operator>
half-pi-mind face list [--format table|json]
half-pi-mind face remove --id <id> [--yes]
half-pi-mind face remove --label <label> [--yes]
```

`--profile` 与 `--scopes` 互斥。profile 只在创建时展开为实际 scopes，数据库仍保存规范 scope 数组，不保存 profile 名，避免未来 profile 变化隐式改变已有权限。

REPL 的 `/face add` 使用相同参数约定，同样支持 `--profile`、`--scopes` 和 `--format text|json|toml`；两种入口都调用同一个 `management.Service`，profile 展开和 scope 校验保持一致。

初始 profile：

| Profile | Scopes |
|---|---|
| `observer` | `face:sessions:read`, `face:runs:read`, `face:hands:read`, `face:tasks:read` |
| `operator` | `face:chat`, `face:sessions:read`, `face:sessions:write`, `face:runs:read`, `face:runs:cancel`, `face:approve`, `face:hands:read`, `face:tasks:read`, `face:tasks:cancel` |

profile 权限列表是版本化的显式集合。未来新增 Face scope 时不得自动加入已有 profile，必须经过独立安全评审并更新文档与测试。

add 默认输出一次性文本，并支持 `--format json` 或 `--format toml`。TOML 形式用于生成 Face 客户端配置片段，但不自动写入 `~/.half-pi/face/config.toml`。

list 只返回：

```text
id, label, scopes, created_at
```

list 永不返回 token 或 application key。

### Hand 凭据命令

```bash
half-pi-mind hand add <label> [--format text|json|toml]
half-pi-mind hand list [--format table|json]
half-pi-mind hand remove --id <id> [--yes]
half-pi-mind hand remove --label <label> [--yes]
```

Hand add 输出一次 label、token 和 application key。Hand list 与 Face list 一样不显示秘密。

### 状态命令

```bash
half-pi-mind status [--format text|json]
half-pi-mind peers [--format text|json]
```

`status` 在所有状态下可执行：

- IPC 可连接：返回 Mind PID、启动时间、mode、Hub enabled/disabled、WS URL 和 peer 数量。
- IPC 不可连接且状态锁可取得：返回 `stopped`。
- IPC 不可连接且状态锁被占用：返回 `degraded`，说明 Mind 可能正在启动、关闭或管理端点异常，并使用非零退出码。

`peers` 只在 Mind 正在运行且 Hub 启用时成功。Mind 未运行返回 `mind_not_running`；Mind 运行但 Hub 禁用返回 `hub_disabled`。

### 删除确认

凭据删除是可恢复但会立即中断连接的操作：

- 交互式终端默认显示目标 label、类型和 scopes（Face），要求确认。
- 非交互环境必须传 `--yes`，否则返回 usage 错误。
- `--format json` 不隐式代表确认，脚本仍必须显式传 `--yes`。

## 在线与离线路由

### 命令分类

| 命令 | Mind 运行 | Mind 未运行 | Hub 禁用 |
|---|---|---|---|
| `config init/path/validate/show` | 本地文件操作 | 本地文件操作 | 不受影响 |
| `face/hand add/list/remove` | 管理 IPC | 状态锁 + Store | IPC 可用；remove 无 peer 可断开 |
| `status` | 管理 IPC | 锁探测 | IPC 返回 Hub disabled |
| `peers` | 管理 IPC | 拒绝 | 拒绝 |

### 路由算法

凭据命令使用以下固定顺序：

```text
1. 连接管理 IPC
   ├─ 成功：严格执行 IPC 协议，禁止离线回退
   └─ endpoint 不存在或连接被拒绝：进入第 2 步

2. 使用非阻塞尝试在有界时间内获取状态锁
   ├─ 成功：确认没有 Mind runtime，进入离线模式
   └─ 失败：fail closed，返回 control_unavailable/state_busy

3. 离线模式打开 Store
   → 执行 management.Service
   → 关闭 Store
   → 释放状态锁
```

以下情况禁止离线回退：

- 已连接 IPC 但协议版本不匹配。
- IPC 返回格式损坏或身份校验失败。
- endpoint 存在且连接超时，而状态锁被占用。
- 无法判断锁是否受当前文件系统支持。
- 状态目录、锁文件、socket 或数据库路径不满足安全要求。

这样可以避免攻击者或异常进程诱导 CLI 绕过正在运行的 Mind 直接修改 SQLite。

单次锁尝试必须非阻塞；CLI 可以在最多 2 秒的总 deadline 内短间隔重试，以覆盖 Mind 正常关闭的瞬间，但不得无限等待。

## Mind 启动与关闭顺序

状态锁必须覆盖 service mode 和 REPL mode，并在 Store 打开前取得。建议启动顺序：

```text
解析启动参数
  → setup.Init
  → 获取状态锁
  → 加载配置
  → 打开 Store / 执行迁移
  → 创建 management.Service
  → 启动本地管理 IPC
  → 初始化 Approval / Conversation / Authority / Face Gateway
  → 按 server.enabled 决定是否启动 WS Hub listener
  → 进入 service 或 REPL 循环
```

管理 IPC 启动失败时，默认 service mode 必须启动失败，避免形成“锁被占用但无法管理”的长期状态。REPL mode 同样启动 IPC，使外部 CLI 和 REPL 始终共享一个管理权威。

关闭顺序：

```text
停止接受管理请求
  → 等待在途管理 mutation 完成
  → 关闭 Hub 和业务 runtime
  → 关闭 Store
  → 删除 Unix socket（Windows pipe 自动消失）
  → 清理 PID 提示文件
  → 释放状态锁
```

状态锁最后释放，保证 CLI 不会在 Mind 尚未关闭 Store 时进入离线模式。

## 状态锁

### 路径与元数据

新增运行目录：

```text
~/.half-pi/run/
├── mind.lock
└── mind-admin.sock   # Unix only
```

`run/` 权限为 `0700`，`mind.lock` 为 `0600`。Windows 使用等价 ACL。

取得锁后可将以下诊断信息写入锁文件：

```json
{"pid":1234,"mode":"service","started_at":"2026-07-19T12:00:00Z"}
```

锁文件内容和现有 `mind.pid` 都不是互斥权威。进程崩溃后内核自动释放锁，即使文件仍存在也不表示 Mind 正在运行。

### 平台实现

- Unix：`state_lock_unix.go`，使用非阻塞 `flock(LOCK_EX|LOCK_NB)`，文件描述符在整个生命周期保持打开。
- Windows：`state_lock_windows.go`，使用 `LockFileEx` 非阻塞独占锁，文件 handle 在整个生命周期保持打开。
- 禁止使用 `runtime.GOOS` 分支。
- 文件系统不支持可靠锁时直接失败，不退化为 PID 探测。

现有 `mind.pid` 可在兼容期保留用于人工诊断，但 `status`、启动互斥和离线 CLI 均不得依赖它。后续可单独弃用。

## 本地管理 IPC

### 端点

- Unix：`~/.half-pi/run/mind-admin.sock`。
- Windows：`\\.\pipe\half-pi-mind-<home-digest>`，其中 digest 从规范化 Half-Pi home 路径派生，只用于稳定命名。

Unix socket 所在目录必须是当前用户所有的真实目录，禁止 symlink；socket 权限收紧为 `0600`。服务端在支持的平台额外校验 peer UID 与当前进程 UID 一致。

Windows named pipe 由 `github.com/Microsoft/go-winio` 的 `ListenPipe` / `DialPipeContext` 实现，使用字节流模式，并配置仅允许当前用户 SID 和 SYSTEM 的 DACL。实现放在 `_windows.go` 文件中，不回退为无认证的 loopback TCP 端口。

服务启动时只有在持有状态锁后才能删除 Unix stale socket。CLI 不得在未持锁时删除 endpoint。

### 协议

管理协议是本地 versioned JSON request/response，不复用 Face Envelope，也不使用 Face token。传输层的 OS 用户权限是本地身份边界。

每个连接第一阶段只处理一个请求，随后关闭，降低状态和背压复杂度。请求和响应上限均为 1 MiB，读写 deadline 为 5 秒，JSON 严格拒绝未知字段。

请求：

```json
{
  "version": 1,
  "request_id": "uuid",
  "operation": "face.add",
  "params": {
    "label": "terminal",
    "scopes": ["face:chat", "face:sessions:read"]
  }
}
```

成功响应：

```json
{
  "version": 1,
  "request_id": "uuid",
  "ok": true,
  "result": {}
}
```

失败响应：

```json
{
  "version": 1,
  "request_id": "uuid",
  "ok": false,
  "error": {
    "code": "invalid_argument",
    "message": "face scopes must not be empty",
    "retryable": false
  }
}
```

初始 operation：

```text
status.get
peers.list
face.add
face.list
face.remove
hand.add
hand.list
hand.remove
```

配置命令直接读取本地文件，不通过 IPC；配置不支持运行时 reload，输出应明确提示修改仅在下次启动生效。

## 管理服务

### 共享服务

新增 `management.Service`，由以下入口共同调用：

```text
REPL ─────────────┐
本地管理 IPC ─────┼──> management.Service ──> Store
离线 CLI ─────────┘             └───────────> Hub（可选）
```

服务持有：

```go
type Service struct {
    Store   CredentialStore
    Peers   PeerController // nil 表示没有 Hub
    Auditor Auditor
    Now     func() time.Time
}
```

接口必须表达业务能力，不暴露 `*sql.DB` 或 Hub 内部 map。REPL 不再自行实现 selector 解析、Store 删除和 peer 断开。

### 输出 DTO

Store 的内部 `Credential` 包含秘密，禁止直接作为管理 list 或 IPC DTO。管理层使用不同类型：

```go
type CredentialSummary struct {
    ID        int64
    Label     string
    Scopes    []protocol.FaceScope
    CreatedAt time.Time
}

type CreatedCredential struct {
    CredentialSummary
    Token          string
    ApplicationKey string
}
```

只有 add 返回 `CreatedCredential`。list、remove、status 和 peers 只能使用不含秘密的 DTO。

### 创建

- label、token/key 生成和 Face scope 规范化继续复用 Store 的现有规则。
- profile 在 CLI 端展开后仍由服务端完整验证，IPC 不信任客户端展开结果。
- add 成功后秘密只写 stdout；stderr、EventBus、日志和 audit 不得包含。
- 同类型重复 label 返回稳定 `conflict`。

### 删除与即时断连

在线删除固定顺序：

```text
解析 id/label 并取得目标 summary
  → 数据库删除 + 管理审计提交
  → Hub.RemoveByType(type, label)
  → 确认目标 peer 已不在 Hub
  → 返回 removed summary 和 disconnected 状态
```

数据库撤销是权威边界。提交后即使连接关闭返回错误，该凭据也不能重新握手；Face Gateway 每个 command 的身份重查继续提供额外保护。已经 accepted 的 Chat/remote run 不因 Face 凭据删除自动取消，沿用现有协议决策。

离线删除没有 Hub，返回：

```json
{"removed":true,"disconnected":false,"reason":"hub_absent"}
```

Mind 运行但 Hub 禁用时通过 IPC 执行相同语义，reason 为 `hub_disabled`。

## 管理审计

凭据 add/remove 是安全边界变更，在线、离线和 REPL 必须产生一致审计。当前表结构：

```sql
CREATE TABLE management_audits (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id     TEXT NOT NULL,
    source         TEXT NOT NULL,
    actor          TEXT NOT NULL,
    operation      TEXT NOT NULL,
    target_type    TEXT NOT NULL,
    target_id      TEXT NOT NULL DEFAULT '',
    target_label   TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL,
    code           TEXT NOT NULL DEFAULT '',
    message        TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL
);
```

约束：

- `source` 为 `ipc`、`offline_cli` 或 `repl`。
- `actor` 只记录 OS UID/SID 的稳定表示或 `repl:<pid>`，不记录环境变量。
- 不保存 token、application key、原始配置、命令行或未脱敏 payload。
- 成功 mutation 与成功 audit 在同一事务提交；audit 写入失败时 mutation 回滚。
- 无法进入事务的早期协议/权限错误只写受限结构化日志，不写秘密。

## 错误码与退出码

稳定错误码：

```text
invalid_argument
invalid_request
invalid_config
not_found
conflict
mind_not_running
state_busy
control_unavailable
hub_disabled
permission_denied
unauthorized
result_unknown
internal
```

CLI 退出码：

| Exit | 含义 |
|---:|---|
| 0 | 成功 |
| 1 | 内部错误 |
| 2 | 命令行 usage 或缺少确认 |
| 3 | 配置/参数验证失败 |
| 4 | 命令要求运行中的 Mind/Hub，但当前未运行或禁用 |
| 5 | 状态锁繁忙、IPC 异常、结果未知或无法安全判断在线状态 |
| 6 | 资源不存在或冲突 |

stdout 只承载结果；stderr 只承载诊断。JSON 成功模式下 stdout 恰好输出一个 `{"ok":true,"result":...}` 对象；失败时 stdout 为空，stderr 输出一个 `{"ok":false,"error":{"code":"...","message":"..."}}` 对象。

## 安全边界

- 管理 IPC 只允许当前 OS 用户，禁止监听 TCP。
- 所有路径继续执行 symlink、类型、owner 和权限检查。
- 状态锁必须在打开 SQLite 前取得，不能依赖 SQLite WAL 代替生命周期互斥。
- 在线 IPC 成功建立后，任何协议错误都不得降级为离线直写。
- list/status/peers/config show 不输出长期秘密。
- add 的秘密响应不得进入 EventBus、`mind.log`、shell 补全或审计表。
- CLI 不接受用户提供 token/application key；凭据必须由 Mind 使用安全随机源生成。
- `config show` 永远脱敏，不提供 `--show-secrets`。
- remove 必须使用精确 `--id` 或 `--label`，禁止模糊匹配和数字 label 猜测。
- 管理 IPC 的请求大小、deadline 和并发数必须有界；慢客户端不能阻塞 Hub 或 Chat。
- 管理 mutation 使用单独串行锁；只读 status/list 可并发，但不得观察到半提交状态。

## 建议文件组织

```text
modules/half-pi-mind/
├── cmd/half-pi-mind/
│   ├── cli.go
│   ├── cli_config.go
│   ├── cli_face.go
│   ├── cli_hand.go
│   └── cli_status.go
└── internal/
    ├── management/
    │   ├── service.go
    │   ├── credential.go
    │   ├── audit.go
    │   ├── protocol.go
    │   ├── client.go
    │   ├── server.go
    │   ├── transport_unix.go
    │   └── transport_windows.go
    └── state/
        ├── lock.go
        ├── lock_unix.go
        └── lock_windows.go
```

一个文件只承载一个核心概念。平台差异通过 build tag 和文件名解决，不在共享代码中判断 `runtime.GOOS`。

## 并发与故障语义

### Mind 启动与离线 CLI 竞争

状态锁决定唯一胜者：

- Mind 先取得锁：CLI 无法离线打开 Store，等待 IPC 或返回 `state_busy`。
- CLI 先取得锁：Mind 启动失败并提示管理命令正在访问状态；离线命令应短时完成。
- 不实现无限等待，默认锁等待上限不超过 2 秒。

### Mind 已持锁但 IPC 不可用

CLI 返回 `control_unavailable`，不得修改数据库。这覆盖 Mind 正在启动、正在关闭、管理 listener 崩溃或 endpoint 权限异常等情况。

### Stale PID、锁文件和 socket

- stale PID 不影响判断。
- stale lock 文件不影响内核锁获取。
- Unix stale socket 只有持有状态锁的 Mind 启动路径可以删除。
- Windows named pipe 随服务 handle 关闭自动消失。

### IPC 请求与 shutdown 竞争

- 已进入 mutation 的请求在 shutdown 前完成或事务回滚。
- shutdown 停止接收新请求后才关闭 Store。
- CLI 收到连接中断但无法确认 mutation 结果时返回 `result_unknown` 类诊断，不自动重试 add/remove。
- 用户可通过 list 确认最终状态；add 不自动重试，避免生成第二组凭据。

## 测试与验收

所有 Go 测试使用 `-race -count=1`。

### 状态锁

- service 和 REPL 都在 Store 打开前持锁。
- 两个离线 CLI 不能同时打开 Store。
- stale PID/lock 文件不造成假阳性。
- Mind 启动与离线 CLI 竞争只有一个成功。
- Unix `flock` race 测试通过；Windows `LockFileEx` race 测试已由 `scripts/test-windows.ps1` 在 WinBoat Windows 11 原生环境执行通过。
- 锁不受支持、路径为 symlink 或权限无法收紧时 fail closed。

### IPC

- 当前用户连接成功，其他用户连接被拒绝。
- 协议版本、未知字段、超限 payload、超时和多请求连接被拒绝。
- Hub disabled 时 IPC 仍可执行凭据 CRUD 和 status。
- listener 启动失败使默认 service mode 启动失败。
- shutdown 等待在途 mutation，不发生 Store close race。

### 凭据管理

- REPL、在线 CLI 和离线 CLI 对同一输入产生相同规范 scopes 和错误码。
- add 输出 token/key 一次，list/日志/audit 不包含秘密。
- 在线 remove 提交数据库后立即断开对应 type+label，不影响同 label 的另一 peer type。
- 离线 remove 成功且下次 Mind 启动后旧凭据无法认证。
- duplicate label、空 scopes、未知 scopes 和错误 selector 稳定失败。
- `observer`、`operator` profile 展开结果固定并通过协议 scope 校验。
- mutation 与 audit 原子提交。

### CLI

- 无子命令的启动行为与现有版本兼容。
- text/table/json/toml 输出快照测试覆盖。
- JSON stdout 不混入日志，stderr 不包含秘密。
- 非交互 remove 缺少 `--yes` 时拒绝。
- 退出码与稳定错误码逐项覆盖。
- `config show` 对文件和环境变量中的 API key 都脱敏。

### 进程级 E2E

至少覆盖：

```text
Mind 未运行 → offline face add → 启动 Mind → 终端 Face 认证成功
Mind service + Hub enabled → online face add/list/remove → Face 立即断开
Mind service + Hub disabled → online face add/remove 成功，peers 返回 hub_disabled
Mind --repl → 外部 CLI 管理凭据 → REPL list 观察同一状态
Mind 持锁但管理 socket 不可用 → CLI fail closed，不修改 SQLite
```

Unix E2E 纳入常规 race 测试；Windows named pipe、ACL 和 `LockFileEx` 使用原生 Windows 验收脚本覆盖。

`scripts/test-windows.ps1` 默认在原生 Windows 使用本地 Go 工具链构建并测试；`-PrebuiltDir` 模式用于 WinBoat 等未安装 Go 的验收环境，接收交叉构建的 Windows race 测试二进制和 `half-pi-mind-race.exe`，但所有测试与进程链路仍在 Windows 内原生执行。

## 实施阶段

### Phase 0：共享业务服务

- 提取 `management.Service`、summary/created DTO 和稳定错误码。
- REPL 改用 Service，保持现有命令行为。
- 增加管理审计表和事务边界。

### Phase 1：状态锁与启动顺序

- `setup.Env` 增加 RunDir、LockPath 和 ControlEndpoint。
- 实现 Unix/Windows 状态锁。
- service 与 REPL 在 Store 前持锁；调整 shutdown 顺序。
- 保留 `mind.pid` 仅作兼容诊断。

### Phase 2：管理 IPC

- 实现 version 1 本地协议、server/client 和平台 transport。
- Mind 在 Hub 之前启动管理 listener，Hub disabled 时保持可用。
- 完成权限、大小、deadline、并发和 shutdown 测试。

### Phase 3：CLI

- 增加 config、face、hand、status、peers 子命令。
- 实现在线优先、锁保护的离线降级。
- 增加 text/table/json/toml 输出和稳定退出码。

### Phase 4：进程验收与文档

- 增加无 Hub、Hub disabled、service、REPL 和启动竞争 E2E。
- 更新 README、Face 接入指南和 AGENTS.md 进度。
- 已在 WinBoat Windows 11 原生验收 named pipe、ACL、状态锁和管理 CLI 进程链路。

## 完成标准

“创建 Face/Hand 凭据必须进入 REPL”的限制已移除。跨平台完成标准如下：

- Mind 运行时凭据命令全部通过受保护的本地 IPC。
- Mind 未运行时凭据命令仅在持有独占状态锁时离线执行。
- Hub disabled 不影响管理 IPC 和凭据 CRUD。
- 在线撤销会立即断开匹配 peer，离线撤销在下次认证时生效。
- REPL 与 CLI 共享业务逻辑和审计，不存在第二套 CRUD 实现。
- list、日志、错误、事件和 audit 均通过秘密泄漏测试。
- Unix race E2E 与原生 Windows IPC/锁/DACL、管理进程链路均已通过。Windows 凭据/config/database 全面 ACL 发布验收作为独立后续任务。
