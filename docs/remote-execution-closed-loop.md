# Mind → Hand 远程执行闭环设计

## 状态

部分落地。本文承接 [`archived/remote-execution.md`](archived/remote-execution.md) 的 MVP 设计。审批证明、accepted/rejected、显式取消、唯一终态、服务级 Authority、SQLite 审计和会话隔离已经实现；`rpc_progress` 与后台任务生命周期仍未实现，Windows 进程树取消仍待原生环境验收。工程状态和后续安排分别以 [`remote-execution-implementation-plan.md`](remote-execution-implementation-plan.md) 和 [`next-development-plan.md`](next-development-plan.md) 为准。

## 背景

最初 MVP 已经能完成 Mind 调度 Hand 执行工具：

```text
use_hand → Hub.Send(RPC) → Hand 执行 → RPCResult → use_hand 返回
```

当时这个链路能跑通 demo，但还不是完整闭环。以下缺口除进度流和后台任务外现已落地：

- Mind 只知道是否等到了最终结果，不知道 Hand 是否接收、何时开始、为何拒绝。
- Mind 超时后没有显式取消协议，Hand 只能依赖 RPC 自带 timeout。
- `SkipChecks` 表达的是实现细节，容易被误解为 Hand 完全信任 Mind。
- 远程执行缺少统一状态机和持久化审计记录。
- Face/TUI/IM Bot 接入后，多入口并发会放大会话状态、审批状态和 activeHand 串扰风险。

本文方案以 `RemoteRun` 为核心对象，由 Mind 创建并持有权威生命周期，Hand 负责本地守门和执行事实上报。

## 设计目标

- 每一次远程执行都有唯一 `run_id`，贯穿审批、发送、接收、执行、取消和审计。
- Mind 能区分 `sent`、`accepted`、`running`、`succeeded`、`failed`、`rejected`、`cancelled`、`timed_out` 等状态。
- Hand 永远保留最终执行守门权，不因 Mind 审批而绕过工具存在性、allow/deny、平台检查和输出上限。
- Mind 主动取消后，Hand 能明确返回取消结果。
- 协议能支持后续进度流、后台任务、Face 多端同步和审计持久化。

## 非目标

- 不在本阶段设计完整 OS 沙箱。
- 不要求所有工具立即支持增量输出。
- 首版不实现后台任务持久恢复，只保留状态机扩展空间。
- 不改变 Mind 作为会话、审批、审计权威的定位。

## 核心模型：RemoteRun

Mind 为每次远程执行创建 `RemoteRun`。`run_id` 是协议层和审计层的主键。

```go
type RemoteRun struct {
	ID         string
	SessionID  string
	HandID     string
	Tool       string
	ArgsDigest string

	Approval *Approval
	Status   RemoteRunStatus

	CreatedAt  time.Time
	SentAt     time.Time
	AcceptedAt time.Time
	StartedAt  time.Time
	FinishedAt time.Time

	Result *protocol.RPCResult
	Error  string
}
```

状态定义：

```go
type RemoteRunStatus string

const (
	RunCreated         RemoteRunStatus = "created"
	RunApproved        RemoteRunStatus = "approved"
	RunSent            RemoteRunStatus = "sent"
	RunAccepted        RemoteRunStatus = "accepted"
	RunRunning         RemoteRunStatus = "running"
	RunSucceeded       RemoteRunStatus = "succeeded"
	RunFailed          RemoteRunStatus = "failed"
	RunRejected        RemoteRunStatus = "rejected"
	RunCancelRequested RemoteRunStatus = "cancel_requested"
	RunCancelled       RemoteRunStatus = "cancelled"
	RunTimedOut        RemoteRunStatus = "timed_out"
	RunLost            RemoteRunStatus = "lost"
)
```

状态流：

```text
created
  → approved
  → sent
  → accepted
  → running
      → succeeded
      → failed
      → cancel_requested → cancelled
      → timed_out

sent
  → rejected
  → timed_out
  → lost
```

## 审批证明

`SkipChecks` 应替换为结构化审批证明。它表达“Mind 已经完成了哪些审批”，不表达“Hand 可以跳过所有检查”。

```go
type Approval struct {
	Approved   bool      `json:"approved"`
	Source     string    `json:"source"` // user, policy, auto_allow, mode
	Mode       string    `json:"mode"`   // strict, normal, trust, yolo
	Approver   string    `json:"approver,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	OneShot    bool      `json:"one_shot"`
	ArgsDigest string    `json:"args_digest"`
	ApprovedAt time.Time `json:"approved_at"`
}
```

Hand 接收规则：

```text
unknown tool                         → reject
deny_tools 命中                      → reject
allow_tools 未命中                   → reject
工具需要确认但 Approval 缺失         → reject
Approval.ArgsDigest 与实际参数不匹配 → reject
Mind 已审批且 Hand 策略接受          → execute
Mind 已审批但 Hand 本地检查失败      → reject
```

Approval 只解决用户同意和 Mind 侧安全策略，不覆盖 Hand 本机边界。Hand 仍必须执行：

- 工具存在性检查
- allow/deny 工具过滤
- 远端专属工具本地 `Tool.Check`
- 输出上限
- 平台能力限制

## 协议消息

现有 `rpc` / `rpc_result` 保留，但字段和语义升级。新增接收、拒绝、进度、取消消息。

```go
const (
	TypeRPC             = "rpc"
	TypeRPCAccepted     = "rpc_accepted"
	TypeRPCRejected     = "rpc_rejected"
	TypeRPCProgress     = "rpc_progress"
	TypeRPCResult       = "rpc_result"
	TypeRPCCancel       = "rpc_cancel"
	TypeRPCCancelResult = "rpc_cancel_result"
)
```

### RPC

```go
type RPC struct {
	RunID     string         `json:"run_id"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args"`
	TimeoutMs int            `json:"timeout_ms,omitempty"`
	Approval  *Approval      `json:"approval,omitempty"`
}
```

### Accepted / Rejected

Hand 收到 RPC 后先完成本地守门，然后立即返回接收或拒绝。

```go
type RPCAccepted struct {
	RunID     string    `json:"run_id"`
	StartedAt time.Time `json:"started_at"`
}

type RPCRejected struct {
	RunID  string `json:"run_id"`
	Reason string `json:"reason"` // deny_tools, unknown_tool, check_failed, approval_required
}
```

### Progress

进度流是可选能力。第一阶段可只对 `exec_command` 或未来后台任务启用。

```go
type RPCProgress struct {
	RunID string `json:"run_id"`
	Seq   int64  `json:"seq"`
	Kind  string `json:"kind"` // stdout, stderr, status
	Data  string `json:"data"`
}
```

`Seq` 用于 Mind 去重和保持单个 run 内的顺序。

### Result

`RPCResult` 增加 `RunID`，并保留成功、输出、错误和截断信息。

```go
type RPCResult struct {
	RunID     string `json:"run_id"`
	Success   bool   `json:"success"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}
```

### Cancel

```go
type RPCCancel struct {
	RunID  string `json:"run_id"`
	Reason string `json:"reason"` // user, timeout, session_closed
}

type RPCCancelResult struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"` // cancelled, already_done, unknown_run, failed
	Error  string `json:"error,omitempty"`
}
```

## Mind 执行流程

`use_hand` 从同步等待 RPCResult 改为创建并等待 `RemoteRun` 的终态。

```text
use_hand.Execute()
  1. 解析 hand_id：参数指定 || session activeHand
  2. 校验 Hand 在线
  3. 对目标 tool 做 Mind 侧 CheckTool
  4. 如需确认，走 Approver
  5. 生成 ArgsDigest 和 Approval
  6. 创建 RemoteRun，状态 created / approved
  7. 注册 pendingRuns[runID]
  8. 发送 RPC{RunID, Tool, Args, TimeoutMs, Approval}
  9. 状态改为 sent
 10. 等待 terminal 状态
 11. 本地 context 取消或 timeout 时发送 rpc_cancel
 12. 返回结构化结果给 LLM
```

Mind 接收 Hand 消息：

```text
rpc_accepted      → run.status = accepted/running
rpc_rejected      → run.status = rejected
rpc_progress      → append run event
rpc_result        → run.status = succeeded/failed
rpc_cancel_result → run.status = cancelled 或保持 terminal 状态
hand disconnect   → 相关 running run 标记 lost 或等待重连策略
```

终态包括：

```text
succeeded
failed
rejected
cancelled
timed_out
lost
```

## Hand 执行流程

Hand 维护本地任务表：

```go
type Task struct {
	RunID     string
	Tool      string
	StartedAt time.Time
	Cancel    context.CancelFunc
	Done      chan struct{}
	Status    string
}

type Hand struct {
	tasksMu sync.Mutex
	tasks   map[string]*Task
}
```

处理 RPC：

```text
handleRPC()
  1. 解析 RPC
  2. 校验 tool 是否存在
  3. 校验 allow/deny
  4. 校验 Approval 是否满足本地策略
  5. 需要本地 Check 的工具继续执行 Check
  6. 失败：发送 rpc_rejected
  7. 成功：创建 task，保存 cancel func
  8. 发送 rpc_accepted
  9. goroutine 执行工具
 10. 完成后删除 task，发送 rpc_result
```

处理取消：

```text
handleCancel(runID)
  1. 查 task
  2. task 不存在：返回 unknown_run 或 already_done
  3. task 存在：调用 cancel()
  4. 等待任务退出或短超时
  5. 返回 rpc_cancel_result
```

Unix `exec_command` 在 context 取消时终止进程组。Windows 实现使用 kill-on-close Job Object：命令先挂起启动，加入独立 job 后恢复，取消时终止 job 内完整进程树；正常完成时解除 kill-on-close，避免误杀有意启动的后台进程。该实现已通过多架构交叉编译，仍需原生 Windows 集成测试后才能宣称跨平台完整取消。

## 并发状态模型

闭环协议只解决单次 run 的生命周期，不解决会话并发。Face/TUI/IM Bot 接入前需要明确状态边界。

统一 Face 的连接、鉴权、command/response、快照、事件投影和 Headless Agent Face 方案见 [`face-protocol.md`](face-protocol.md)。其中 Face payload 使用 `conversation_id`；本节历史设计中的业务 `session_id` 均指持久化对话，不是 `Envelope.SessionID` 的连接级防重放会话。

建议拆成两类状态：

```text
SessionActor
  - Chat
  - history
  - Mode
  - activeHand
  - approval cache

RemoteRunRegistry
  - pending run
  - run status
  - progress event
  - Hand result dispatch
```

会话状态通过 `session_id` 串行化。Hub 消息可以并发接收，但凡要影响会话状态，应投递到对应 SessionActor。RemoteRunRegistry 只负责按 `run_id` 更新执行生命周期。

## 审计持久化

Mind 侧应维护权威执行记录。EventBus 继续用于实时输出，但不作为唯一审计来源。

建议表：

```sql
CREATE TABLE remote_runs (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    hand_id TEXT NOT NULL,
    tool TEXT NOT NULL,
    args_digest TEXT NOT NULL,
    approval_source TEXT NOT NULL DEFAULT '',
    approval_mode TEXT NOT NULL DEFAULT '',
    approval_reason TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    sent_at TEXT,
    accepted_at TEXT,
    finished_at TEXT,
    duration_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE remote_run_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL,
    seq INTEGER NOT NULL DEFAULT 0,
    type TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

敏感参数不直接入库，只保存摘要。必要时为特定安全工具定义脱敏摘要。

## 错误语义

Hand 拒绝原因应结构化，避免 Mind 和用户只能看到普通错误文本。

建议拒绝原因：

```text
unknown_tool
deny_tools
allow_tools_miss
approval_required
approval_digest_mismatch
check_failed
timeout
cancelled
output_truncated
internal_error
```

Mind 返回给 LLM 的结果也应包含来源信息：

```json
{
  "run_id": "...",
  "hand_id": "...",
  "tool": "exec_command",
  "status": "succeeded",
  "duration_ms": 1234,
  "truncated": false
}
```

## 与现有 MVP 的迁移

第一阶段保持用户可见行为不变：`use_hand` 仍阻塞直到终态，再把结果返回给 LLM。

内部迁移顺序：

1. 在 `gateway-core/protocol` 增加新消息结构。
2. 将 `RPC.ID` 直接替换为 `RunID`。
3. 将 `RPC.SkipChecks` 替换为 `RPC.Approval`。
4. Mind 增加 `RemoteRunRegistry`，先以内存实现。
5. Hand 增加 `tasks map` 和 `rpc_cancel` 处理。
6. `use_hand` 超时时发送 `rpc_cancel`。
7. 增加 SQLite 审计表。
8. 再接入进度流和后台任务。

## 测试计划

协议测试：

- RPC accepted 后 result 能正确匹配 run。
- rejected 不进入 running。
- result 来源 Hand 不匹配时拒绝投递。
- cancel unknown run 返回 `unknown_run`。

Hand 集成测试：

- allow/deny 拒绝不会执行工具。
- Approval 缺失时需要确认的工具被拒绝。
- ArgsDigest 不匹配时拒绝。
- Unix `exec_command` 被 cancel 后进程组退出。

Mind 集成测试：

- `use_hand` 正常成功。
- `use_hand` 收到 rejected 后返回结构化拒绝。
- `use_hand` timeout 后发送 cancel。
- Hand disconnect 时 running run 标记为 lost。

Race 测试：

- 并发 Chat + select_hand。
- 并发多个 use_hand。
- Hub result 与 session 切换同时发生。
- cancel 与 result 竞争到达时只产生一个终态。

## 建议优先级

1. 审批语义：`SkipChecks` → `Approval`。
2. RemoteRun 状态机和 run registry。
3. `rpc_accepted` / `rpc_rejected`。
4. `rpc_cancel` / `rpc_cancel_result`。
5. 审计表。
6. 进度流。
7. 后台任务模式。

这个顺序能先关闭最关键的安全和生命周期缺口，再逐步增强用户体验。
