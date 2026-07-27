# Mind MCP Server 设计

> 状态：提案（2026-07-26），尚未实现。所依赖的 `executor.ToolRuntime`、`executor.Catalog`、Approval Broker、Face 凭据与 scope、RemoteRun Authority 均已实现。
>
> 本文定义 Mind 关闭自身 Agent 循环后，作为 MCP server 向外部 Agent 暴露工具执行能力的信任模型、会话模型、协议映射、配置与实施顺序。

相关文档：

- [`face-protocol.md`](face-protocol.md)：Face 身份、scope、conversation 归属、审批投影和背压，MCP 接入复用同一套鉴权与命令语义；
- [`ai-face-protocol.md`](ai-face-protocol.md)：既有的 Headless Agent 接入方式，MCP 是它的协议同类而非替代；
- [`plugin-architecture.md`](plugin-architecture.md)：`tool.register` capability 与 MCP 的工具注入语义重叠，二者必须共用同一实现；
- [`archive/remote-execution-closed-loop.md`](archive/remote-execution-closed-loop.md)：RemoteRun、durable task 与 Hand 本地守门，MCP 暴露的核心价值来自这里。

---

## 1. 摘要

Half-Pi 目前把 LLM 放在 Mind 内部，由 `agentcore.Core` 驱动工具循环。但工具执行链路从设计上就与 LLM 解耦：`executor.ToolRuntime` 是唯一生产入口（`executor/runtime.go:224`），`Core.ExecuteTool` 已经是显式的非 LLM 复用入口（`agentcore/core.go:350`）。因此「关闭 Agent、把 Mind 变成别人的工具后端」不是重写，而是给同一条链路接一个新的调用方。

本文主张：

1. 对外暴露的价值不是本地文件工具，而是 **Hand**——加密多节点远程执行、durable 后台任务、进度流、取消、节点侧最终守门，外加人在环审批和可审计轨迹；
2. MCP server 是**第四种 Face**，必须复用 Face 凭据、scope、principal 绑定和审计，不新建鉴权面；
3. 「用户自己负责」实现为**显式的自动审批 principal**，不实现为绕过审批路径的开关；
4. 真正的能力边界由 `Catalog.Derive` 的工具白名单给出，不由安全模式给出；
5. 默认关闭，开启后必须显眼。

## 2. 目标与非目标

### 2.1 目标

- 外部 Agent（Claude Code、其他 MCP host）可以通过 MCP 调用 Half-Pi 的工具，重点是 Hand 远程执行族；
- 操作任何机器都经由该机器的 Hand，**包括 Mind 自己所在的机器**（见 6.1）；
- 调用全程进入 `ToolRuntime`，保留 freeze、schema 校验、Guard、Authorizer、审批、审计和终态语义；
- 人可以在 TUI Face 上看到并裁决由 MCP 发起的调用；
- MCP 发起的 run 与 task 与既有 `remote_runs` / `remote_tasks` 共用状态机和查询入口；
- Skill 以 MCP prompt 形式暴露。

### 2.2 非目标

- 不把 Mind 变成 MCP client（接入外部 MCP server 获取工具）。该方向复用同一 `Catalog` 抽象但语义相反，另行设计；
- 不通过 MCP 暴露 Chat、Compact 或 conversation 历史。Agent 关闭时这些能力没有消费者；
- 不实现 MCP 的 sampling（server 反向请求 client 采样）。它会把外部模型引入 Mind 的决策路径，与隔离 Reviewer 的设计冲突；
- 不为 MCP 建立第二套工具注册机制。MCP 只是 `Catalog` 的一个投影。

## 3. 现有基础与边界

### 3.1 可以直接复用

| 能力 | 位置 | MCP 用途 |
|---|---|---|
| 非 LLM 工具入口 | `agentcore/core.go:350` | `tools/call` 的落点 |
| 唯一执行入口与终态语义 | `executor/runtime.go:224` | 换调用方不丢任何安全不变量 |
| JSON Schema 导出 | `executor/types.go:245` | `tools/list` 的 `inputSchema` |
| 工具目录裁剪 | `executor/catalog.go:95` `Catalog.Derive` | 每个 MCP principal 一份独立工具视图 |
| 异步审批、TTL、首裁决、审计 | `approval/broker.go:124` | 人在 TUI 上裁决 MCP 发起的调用 |
| 本地裁决适配器接缝 | `approval/types.go:112` `FallbackResolver` | 自动审批 principal 的挂载点 |
| 进度回调 | `executor.Progress` / `ProgressFunc` | MCP progress notification |
| 取消 | `remoteexec.Authority.CancelRun` | MCP `notifications/cancelled` |
| Skill 按 group 隔离 | `skill.IndexForGroup` / `GetForGroup` | MCP `prompts/list` 与 `prompts/get` |
| Face 凭据、scope、principal 绑定 | `store.FaceIdentityByLabel`、`protocol.FaceScope` | MCP 鉴权 |

### 3.2 硬约束

**状态锁独占。** Mind 启动时先取 `env.LockPath` 的 OS 状态锁再打开 SQLite（`cmd/half-pi-mind/main.go:107`）。任何「另起一个进程自己开库提供 MCP」的方案在 Mind 常驻时无法启动。MCP server 只能：

- 跑在 Mind 进程内，或
- 作为外部客户端通过已鉴权入口接入运行中的 Mind。

这条约束决定了第 7 节的方案排序。

**LLM provider 目前是硬依赖。** `conversation.NewManager` 要求 `Provider != nil`（`conversation/manager.go:158`），`cmd/conversation.go:65` 无条件构造 provider。「关闭 Agent」需要允许 provider 缺省。

**全局工具注册。** 工具通过 `init()` 注册进 `executor.DefaultCatalog()`，`local.SetSkillStore` 也是包级全局。MCP 的 per-principal 视图必须走 `Derive`，不能改动默认目录。

## 4. 信任模型

这是本设计的核心章节。MCP 把调用方从「Mind 自己配置的模型」换成「任意外部 Agent」，而人往往坐在那个 Agent 的宿主里，不在 Face 前面。

### 4.1 yolo 不等于不审批

`internal/lifecycle/authorizer.go:174` 的 `case "yolo":` 分支为空，注释明确说明 hard deny 已先执行、显式确认与默认确认仍然强制。实际后果：

| 场景 | yolo 下的行为 |
|---|---|
| 硬编码黑名单（`rm -rf /`、mkfs、fork 炸弹） | 仍然 `deterministic_deny` |
| `write_file` / `edit_file` | `DefaultConfirm: true`（`tool_write_file.go:24`、`tool_edit_file.go:27`）→ 仍需审批 |
| `use_hand` 调用 Mind 不认识的远程工具 | `authorizer.go:127` 强制 `ForceUserApproval` → 仍需审批 |
| `use_hand` + `background: true` | 强制确认 → 仍需审批 |
| 调用方自己传 `confirm: true` | 强制确认 → 仍需审批 |

没有 approver 时这些调用阻塞到 TTL（默认 2 分钟，`approval/broker.go:16`）后返回 `approval_unavailable`。因此「MCP 只在 yolo 下可用」不能成立：最值得暴露的写文件与后台任务恰好都走不通。

### 4.2 自动审批 principal

「用户自己负责」实现为一个显式的 `Approver`，而不是一个跳过审批的开关：

```text
MCP 调用
  → ToolRuntime.Prepare → Guard → MindAuthorizer
  → needsUser
  → mcpAutoApprover.Confirm
      → Broker 正常注册审批请求（Face/TUI 立即可见 approval.requested）
      → 立即以 Actor{ID: <mcp principal>, Source: "mcp-auto"} 裁决 allow_once
      → approval_audits 如实记录「非人类裁决」
  → 继续执行
```

相对「在 Authorizer 里为 MCP 开特例」，这个形状的收益：

- 不新增旁路，`ToolRuntime → Authorizer → Broker → Auditor` 链路结构完全不变；
- Approval digest 与实际执行参数的绑定不受影响；
- 审计表如实区分 `mcp-auto` 与人类 actor，事后可查、可统计、可告警；
- 换成真人 sink 时只替换这一个实现，其余代码不动；
- Face 依然收到 `approval.requested` / `approval.resolved`，TUI 能实时看到「刚刚自动放行了什么」。

`FallbackResolver`（`approval/types.go:112`）已经是这个形状，REPL 就是这么接的（`repl/repl.go:55`）。唯一需要的改动是它当前为进程级，MCP 需要按 conversation / principal 绑定。

### 4.3 不可让渡的不变量

无论配置如何，以下三条不允许被 MCP 配置关闭：

1. **硬编码黑名单 deny**。它拦的是无条件破坏性操作，不属于「用户可以自己承担」的范畴，且它已经存在，保留是零成本。
2. **Hand 侧本地守门**。Hand 的 allow/deny、工作目录限制和 node-local ToolRuntime 是节点所有者的权利，Mind 侧配置不能覆盖。
3. **审计**。`approval_audits` 与 `lifecycle_outbox` 的必需审计失败保持 fail closed。审计正是「用户自己负责」得以成立的前提——出事之后能查清楚。

### 4.4 责任边界

| 层 | 责任方 | 可配置 |
|---|---|---|
| 硬编码黑名单 | Half-Pi | 否 |
| **某台机器上允许做什么**（含 Mind 本机） | 该机器的 Hand 所有者 | 是（该 Hand 的 `allow_tools` / `deny_tools` / `work_dir`） |
| 谁可以提出请求 | Mind 用户 | 是（Face 凭据与 scope） |
| 暴露哪些工具族 | Mind 用户 | 是（`[mcp].tools`） |
| 灰名单是否需人裁决 | Mind 用户 | 是（`auto_approve`） |
| 调用内容是否合理 | 外部 Agent 及其用户 | — |
| 审计留存 | Half-Pi | 否 |

第二行是关键：**机器的能力边界属于该机器，不属于 Mind。** 用户想要对本机的无限制修改能力，正确的表达是本机 Hand 放开 `allow_tools` 与 `work_dir`，而不是放宽 Mind 侧的门。见 6.1。

### 4.5 双重审批与自批

MCP host（如 Claude Code）通常会先问用户「是否允许调用这个 MCP 工具」。**该确认不构成 Half-Pi 审批**，理由是调用方对自身的确认等价于自批，与「Reviewer 不能覆盖强制审批」是同一条不变量。Half-Pi 侧的审批 sink 只有两种合法形态：

- 人类：Face / TUI / REPL 裁决；
- 显式配置的 `mcp-auto`：由用户在配置文件里一次性授权，审计中可识别。

不采用 MCP elicitation 把审批弹回调用方宿主，原因同上。

## 5. 会话与标识符模型

### 5.1 映射

```text
MCP client 连接  ──1:1──▶  Face principal（face_credentials 中的一条）
                            │
                            └──1:1──▶  conversation（sessions 表中的一条）
                                        ├── active_hand
                                        ├── remote_runs
                                        ├── remote_tasks
                                        └── approval_audits
```

一个 MCP principal 绑定一个持久化 conversation。理由：

- `select_hand` 写 `sessions.active_hand`，需要稳定归属；
- durable 后台任务按 conversation 归属，重连后要能对账；
- 审批归属校验、run/task 的 ownership 检查全部按 conversation 键；
- 人在 TUI 里可以打开这个 conversation，看到 MCP 产生的 run、task 和审批。

conversation 在 principal 首次连接时惰性创建，名称取 `mcp:<label>`。断线重连复用同一 conversation，不新建。

### 5.2 已知取舍：无消息历史

MCP 调用不写 `messages` 表，所以该 conversation 在 TUI 中有 run / task / approval，但没有对话记录。这是 M0–M2 的接受项。是否把工具调用投影为伪消息（便于人类回看和 `/compact`）留到 M3 决定；在决定前，`lifecycle_outbox` 中的 tool 事件已经是完整的事实来源。

### 5.3 并发

`Actor` 的 operation lease 只覆盖 Chat / Compact / context mutation（`conversation/actor.go:56`），`ExecuteTool` 不取 lease，因此同一 conversation 内的并发 MCP 工具调用天然允许。需要额外约束的只有：

- `select_hand` 改持久状态，MCP 白名单中默认**不包含**它，目标 Hand 由 `use_hand` 的 `hand_id` 显式给出；
- 单连接的并发上限由 MCP bridge 限流，避免一个 client 打满 Hand。

## 6. 工具目录

### 6.1 一切经由 Hand

MCP 暴露的工具**不包含 Mind 本地执行的文件与命令工具**（`read_file`、`write_file`、`edit_file`、`grep`、`grep_regex`、`list_files`、`exec_command`），并且这一条不提供「配置里加回来」的选项。需要操作某台机器，就在那台机器上注册一个 Hand——**包括 Mind 自己所在的机器**。

理由不是攻击面，而是一致性：Mind 本地工具是当前架构里唯一绕过节点侧守门的执行路径。

| | Mind 本地工具 | 经 Hand 执行 |
|---|---|---|
| 节点侧第二道门 | 无（只有 Mind 侧裁决） | `allow_tools` / `deny_tools` + node-local Authorizer |
| 工作目录 | 进程 cwd，全局单一 | `work_dir` 显式配置，可切换 |
| RunID 与状态持久化 | 无 | `remote_runs` 完整状态机 |
| 进度流 | 仅进程内回调 | `rpc_progress`，有界、可投影 |
| durable 后台任务 | 无 | `remote_tasks` + Hand SQLite，跨重连继续 |
| 取消 | 仅 ctx 取消 | `rpc_cancel` 经 Authority 统一路由 |
| 最小权限部署 | Mind 进程需持有目标机器全部权限 | Mind 可运行在受限用户下 |

第 4.3 节「Hand 侧本地守门不可让渡」这条不变量，对 Mind 自己所在的机器目前并不生效——因为那条路上没有 Hand。本机 Hand 补上这个缺口。

这也让责任归属落到正确位置：**「机器 X 上能做什么」由机器 X 的 Hand 配置决定**（`allow_tools`、`deny_tools`、`work_dir`），Mind 与 MCP 配置只决定「谁可以提出请求」。用户想要对本机的无限制修改能力，表达方式是本机 Hand 放开 `allow_tools` 与 `work_dir`，而不是放宽 Mind 侧的门。这与第 4.4 节的责任边界表一致。

本机 Hand 不是新概念：Hand 默认连接 `ws://127.0.0.1:15707/ws`，与 Mind 默认监听端口一致，本机部署已经是既有默认假设。

**本机 Hand 必须是独立进程。** 进程内嵌 Hand 会与 Mind 共享权限，节点侧守门退化为同一信任域内的自我约束，失去这一节的全部收益。首次运行体验的成本由文档与 `make run-hand` 承担，不通过内嵌规避。

### 6.2 默认白名单

```text
list_hands
get_hand_info
use_hand
get_hand_task
read_hand_task_log
cancel_hand_task
```

不包含 `select_hand`（改持久状态，见 5.3）和 `view_skill`（改由 MCP prompts 暴露，见 8.4）。

### 6.3 扁平投影

`use_hand(tool="read_file", args={…})` 的嵌套形式对调用方模型明显不友好，且 `args` 是无嵌套 schema 的裸 `object`（见 6.4）。因此 M3 应在 `Catalog.Derive` 之上加一层路由包装：对外暴露 `read_file`、`exec_command` 等**顶层工具名与完整 schema**，实现是向目标 Hand 发起 RPC。

投影后的工具与 `use_hand` 共用同一条 admission 链路（`PrepareExternal` + 一次性 approval digest + Hand 侧最终守门），不新增执行路径。目标 Hand 由 principal 绑定的 conversation 的 `active_hand` 决定，或由投影工具名前缀显式指定。

在扁平投影落地前，MCP 调用方只能使用 `use_hand`。

### 6.4 schema 保真度

`Tool.SchemaParameters()`（`executor/types.go:245`）目前只输出一层 `type` + `description`，`use_hand.args` 这类 `object` 参数没有嵌套 schema。外部 Agent 因此得到的引导弱于 Mind 自己的模型。M2 应扩展 `PropertySchema` 支持嵌套或 `enum`；在此之前用 description 补偿。

注意 `validateObjectSchema` 拒绝未知参数（`executor/runtime.go:905`），这对外部调用方是好事：拼错参数会立刻失败而不是被静默忽略。

## 7. 传输与鉴权

受第 3.2 节状态锁约束，只有三个可行形态。

### 7.1 方案 A：外部 bridge 进程，走 Face 协议（推荐）

```text
Claude Code ──stdio MCP──▶ half-pi-mcp ──加密 Face 连接──▶ 运行中的 Mind
                            (轻量 bridge)                    (常驻，持有状态锁)
```

Mind 侧扩展 Face 协议，新增：

- `face.tool.list` / `face.tool.call`（命令），`face.tool.progress`（transient）；
- scope `face:tools:exec`；
- `FaceOperation` 与 `face_validate.go` 对应校验。

优点：

- 零新鉴权面，完全复用 `face_credentials` 的双秘密加密握手、scope、principal 绑定与撤销语义；
- bridge 崩溃或被 MCP host 杀死不影响 Mind 与 Hand 连接；
- 多个 MCP client 可并存，各自一个 principal 一个 conversation；
- 审批、审计、有序投递、背压全部沿用既有实现。

代价：`protocol/face_validate.go`（1445 行严格校验）与 `facegateway/commands.go` 需要同步扩展。这是本方案的主要工作量。

### 7.2 方案 B：Mind 进程内 MCP over stdio（仅作验证）

`half-pi-mind --mcp` 作为第三种启动模式（与 `--repl` 并列），独占状态锁，正常启动 Hub 让 Hand 连接，stdout 让给 MCP，日志改写文件。

优点：最快，不动 Face 协议。缺点是致命的：MCP host 杀掉进程就等于杀掉 Mind 和全部 Hand 连接，且同时只能有一个 client。**只用于 M0 验证人机工程，不作为交付形态。**

### 7.3 方案 C：Mind 进程内 MCP over HTTP

挂在既有 `server` 监听上，新增 `/mcp` 端点。生命周期与多客户端问题都解决，但需要为 HTTP 引入一类新凭据（既有 Face 凭据是双秘密加密握手，不适用于 bearer），等于开第二个入站鉴权面，与「统一协议、不建旁路」的既有决策冲突。除非将来出现远程 MCP 需求，否则不采纳。

### 7.4 结论

M0 用方案 B 做一次性验证，M1 起按方案 A 实现。方案 C 保留为远程接入的后备。

## 8. MCP 协议映射

### 8.1 `tools/list`

来源为该 principal 的派生 `Catalog`。`name`、`description` 直取，`inputSchema` 取 `Tool.SchemaParameters()`。`DefaultConfirm` / `OwnsConfirm` 为 true 的工具在 description 尾部追加固定后缀，提示调用方该操作会触发审批。

目录 revision 变化时发送 `notifications/tools/list_changed`。

### 8.2 `tools/call`

```text
tools/call
  → 解析 principal → 取 conversation Actor
  → Core.ExecuteTool(ctx, name, args)
      → ToolRuntime.Execute（freeze / Guard / Authorizer / 审批 / 审计 / 执行）
  → 映射 Result
```

结果映射：

| `executor.Result` | MCP 响应 |
|---|---|
| `ExecutionSucceeded` + `OutcomeSucceeded` | `content: [{type:"text", text: Output}]`，`structuredContent: Data` |
| `DeliveryOutcome == OutcomeDenied` | `isError: true`，text 为拒绝原因，附 `ErrorCode` |
| 其余（failed / timed_out / cancelled / panicked） | `isError: true`，text 为 Output，附 `ErrorCode` |
| `AuditDegraded` | `isError: true`，明确告知终态审计未落库 |

拒绝与失败一律用 `isError: true` 的工具结果而非 JSON-RPC error，让调用方的模型能看到原因并自行调整。JSON-RPC error 只用于协议层错误（未知工具、参数不是对象、principal 失效）。

### 8.3 进度与取消

- `executor.ProgressFunc` 的每次回调映射为 MCP progress notification，携带调用方传入的 `progressToken`；
- 审批等待期间发送心跳 progress，避免 MCP client 默认超时早于审批 TTL；
- MCP `notifications/cancelled` 取消对应 context，既有链路会传播到 `remoteexec.Authority.CancelRun` 与 Hand 的 `rpc_cancel`；
- 无 `progressToken` 时不发送进度，行为退化为一次性结果。

### 8.4 Prompts（Skill）

`prompts/list` 输出 `skills.IndexForGroup(groupID)` 的条目，`prompts/get` 输出 `GetForGroup` 的全文。group 取该 conversation 的 group，因此 Skill 的 SessionGroup 可见性规则原样生效。Skill 变化时发送 `notifications/prompts/list_changed`。

### 8.5 Resources

M0–M2 不实现。将来可考虑把 Hand 列表与 task 日志暴露为 resource，但 task 日志已有 `read_hand_task_log` 工具，重复暴露会绕开该工具的分页与上限约束。

## 9. 配置

```toml
[mcp]
enabled = false                # 默认关闭
transport = "face"             # "face" | "stdio"（stdio 仅供 M0 验证）
tools = [                      # 留空则使用 6.2 的默认白名单；本地执行工具不可加入
  "list_hands", "get_hand_info", "use_hand",
  "get_hand_task", "read_hand_task_log", "cancel_hand_task",
]
auto_approve = false           # true 时启用 4.2 的自动审批 principal
max_concurrent_calls = 4       # 单连接并发上限
```

启动校验（**fail closed at startup, not at call time**）：

- `enabled && transport == "face"` 但没有任何持有 `face:tools:exec` scope 的凭据 → 启动失败并提示如何创建；
- `enabled && !auto_approve` 且运行在服务模式（无 REPL fallback）→ 启动时输出显著警告，说明需要 TUI Face 在线才能裁决；
- `tools` 中出现未注册工具名 → 启动失败；
- `tools` 中出现 Mind 本地执行工具（`read_file`、`write_file`、`edit_file`、`grep`、`grep_regex`、`list_files`、`exec_command`）→ 启动失败，错误信息指向 6.1 并提示注册本机 Hand；
- `enabled` 且没有任何 Hand 曾经注册过凭据 → 启动时输出显著提示，说明 MCP 目前只能查询、无法执行；
- `auto_approve = true` → 启动 banner 与 `events.LevelWarn` 事件明确声明「MCP 自动放行已开启」。

不允许通过 `[mcp]` 配置改变 conversation 的 `sessions.mode`。安全模式是 per-conversation 的持久状态，若为 MCP 强推 yolo，之后人在 TUI 打开同一 conversation 会继承它。MCP 的宽松度由白名单和 `auto_approve` 表达，不由 mode 表达。

## 10. 可观测性

- 启动 banner 与 EventBus 事件声明 MCP 已开启、transport、工具白名单大小、`auto_approve` 状态；
- 新增 `events` 类型 `mcp.connected` / `mcp.disconnected` / `mcp.tool_call`；
- `lifecycle.Meta.Source` 增加 MCP 来源标识，使 `lifecycle_outbox` 与 `approval_audits` 能按来源过滤；
- TUI Activity 面板中 MCP 发起的 run / task / approval 带明显来源标记；
- `half-pi-mind status` 输出当前 MCP 连接数与绑定的 conversation。

「默认关闭 + 开启后显眼」两件事必须成对，否则用户不知道自己承担了什么。

## 11. 失败语义

| 情形 | 行为 |
|---|---|
| principal 凭据被撤销 | 立即断开连接，进行中的 run 按既有取消链路处理 |
| 无审批 sink 且需要审批 | 不等满 TTL，立即返回 `isError` 并提示需要 Face 在线 |
| 审批超时 | 返回 `isError`，run 走既有终态 |
| bridge 断线 | 已 accepted 的远程 run 不取消（与 Face Chat 一致）；重连后可用 `get_hand_task` 对账 |
| 必需审计失败 | fail closed，工具不执行；已执行的终态审计失败返回 `AuditDegraded` |
| 白名单外工具 | JSON-RPC error，不进入 ToolRuntime |
| Mind 重启 | MCP 连接断开；durable task 按既有 stale/对账逻辑恢复 |

## 12. 实施顺序

### M0：可行性验证（不交付）

方案 B 的 `--mcp` 模式，只支持 `tools/list` + `tools/call`，白名单硬编码，`auto_approve` 强制 true。目标是用真实 Claude Code 跑通一次 `use_hand`，验证 schema 保真度与人机工程。**完成后代码不合入 main。**

### M1：协议与身份

- `face.tool.list` / `face.tool.call` / `face.tool.progress` 的 payload 定义与 `face_validate.go` 校验；
- scope `face:tools:exec`，`half-pi-mind face add --profile` 增加对应 profile；
- `facegateway/commands.go` 的命令处理与 scope 校验；
- `Catalog.Derive` 按 principal 派生目录。

### M2：MCP bridge 与自动审批

- `half-pi-mcp` 二进制：stdio MCP ↔ 加密 Face 连接；
- `mcpAutoApprover` 与 `FallbackResolver` 的 per-conversation 化；
- 配置、启动校验、banner 与事件；
- 结果映射与错误语义。

### M3：进度、取消与 Prompts

- progress notification 与审批心跳；
- `notifications/cancelled` 链路；
- Skill → prompts；
- `PropertySchema` 嵌套 schema 支持；
- 扁平投影（6.3）：顶层工具名 + 完整 schema，实现路由到目标 Hand；
- 决定是否把 MCP 工具调用投影为消息。

### M4：验收

真实进程 E2E 与 Windows 交叉编译/原生验收。

## 13. 测试要求

- **协议单测**：`face.tool.*` payload 的合法/非法用例，与既有 Face 命令同标准；
- **Gateway 单测**：scope 缺失、principal 撤销后复用 label、conversation 归属校验、白名单外工具；
- **Authorizer 单测**：`mcp-auto` 裁决写入审计且 actor source 正确；硬 deny 在 `auto_approve = true` 下仍然拒绝；`DefaultConfirm` 工具在无 sink 时快速失败而非等满 TTL；
- **race 测试**：同一 conversation 的并发 MCP 调用、调用中撤销凭据、调用中 Mind 关停；
- **进程级 E2E**：真实 `-race` 二进制，动态端口，临时 HOME，Scripted LLM。覆盖 MCP bridge → Mind → Hand 的完整 `use_hand`、审批（人类与 `mcp-auto` 两条路径）、取消、后台任务对账，以及审计表脱敏检查。

## 14. 完成定义

1. `[mcp].enabled = false` 时，二进制行为与当前完全一致，无新增监听、无新增表、无新增 goroutine；
2. 开启后，外部 Agent 能通过 MCP 完成 `use_hand` 的前台与后台调用，并能取消；
3. 每一次 MCP 调用在 `lifecycle_outbox` 与（需审批时）`approval_audits` 中留下可归因记录，来源可与人类调用区分；
4. `auto_approve = false` 时，人在 TUI 上能看到并裁决 MCP 发起的审批；
5. `auto_approve = true` 时，硬编码黑名单与 Hand 本地策略仍然拒绝相应调用；
6. 撤销 Face 凭据后，对应 MCP 连接立即失效且不能继承新凭据的 scope。

## 15. 未决问题

1. **MCP 工具调用是否投影为消息。** 影响 TUI 回看体验和 `/compact` 语义，M3 决定。
2. **与插件 runtime 的 `tool.register` 关系。** MCP 本质是跨进程的工具注入，与 [`plugin-architecture.md`](plugin-architecture.md) §3.2 的 `tool.register` 语义重叠。二者应共用同一注册与 scope 实现，先落地者定义契约。
3. **多 MCP client 共享 Hand 的公平性。** 单连接并发有上限，但多个 principal 同时打满一个 Hand 没有节流。是否需要 per-Hand 配额，等真实负载数据。
4. **「一切经由 Hand」是否应推广到 Mind 自身的 Agent 循环。** 本文只把 6.1 的原则应用于 MCP。但同一条论证对 `agentcore` 的工具循环同样成立：Mind 直接本地执行工具时，节点侧守门、RunID、进度持久化和 durable 任务全部缺失。若推广，`half-pi-core/tools` 将成为纯 Hand 侧实现，Mind 只保留 `check_security`、`view_skill`、Hand 路由与 task 查询这类元工具。

   代价不小：需要处理本机 Hand 离线时的降级、改写 Mind 侧全部工具测试与 E2E，并且首次运行从「跑一个进程」变成「跑两个进程」。**这是全局架构决策，超出本文范围**，应单独立 ADR 讨论；在此之前 MCP 与 Chat 在这一点上语义不一致，属于已知不一致而非疏漏。

5. **首次运行体验。** 若采纳 6.1，`setup.Init()` 是否应同时生成本机 Hand 的配置与凭据，使 `make run-hand` 开箱可用。注意本机 Hand 必须是独立进程，不能为了简化体验退回进程内嵌。
