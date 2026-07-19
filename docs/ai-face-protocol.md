# AI Face 协议接入指南

## 状态

本文面向 Headless Agent Face、自动化客户端和其他 AI Agent。统一 wire protocol、独立凭据、四步挑战握手、强制加密、P1-P4 Face runtime、JSONL Headless 客户端和共用协议的人类终端 REPL 已实现并通过 race 测试。当前可使用 conversation/Hand/run/task 查询、快照、订阅、Chat/cancel、异步审批和 run/task cancel；真实 Mind/Hand/Headless/终端 REPL 进程级 E2E 已完成。真正的全屏交互式 TUI 尚未实现。

v2 握手已在 WinBoat Windows 11 Pro AMD64 原生运行 gateway/Mind race 测试及 Mind/Hand/Face 进程链路，验证 encrypted `registered`、双 peer 在线、撤销断连和输出秘密扫描；Windows `386/amd64/arm64` 交叉编译同时通过。更新后的官方 `scripts/test-windows.ps1 -PrebuiltDir` 使用当前源码执行，11 组测试全部 PASS 且 stderr 为空。

架构和完整生命周期见 [`face-protocol.md`](face-protocol.md)。本文只说明客户端应依赖的正式协议契约。

## 接入原则

- AI Face 与人类 Face 使用相同的 `face.*` 消息，不存在测试后门。
- 所有消息由 `protocol.Envelope` 包装。
- `Envelope.SessionID` 是单次 WebSocket 连接的防重放标识。
- Face payload 使用 `conversation_id` 表示持久化对话，不得发送 `session_id`。
- `Envelope.MsgID` 标识一次传输，`request_id` 标识一次业务 command，两者不能互换。
- Face 不直接调用 Hand，也不自行裁决 run 终态；Chat、审批和取消由 Mind 权威处理。

## 鉴权假设

Face 注册使用 version 2 WebSocket 四步挑战握手，必须同时持有与 Hand 分离的 Face token 和 application key。第一步 `register` 只公开 protocol version、peer type 和 label，不发送 token、application key 或设备信息；服务端据此从 Face 凭据表读取双秘密。客户端与服务端以 `token || application_key`、单次 challenge 和完整 transcript 通过 HKDF-SHA-256 派生 proof、C→S、S→C 三把方向/用途隔离的 AES-128-GCM 密钥。Face proof 只加密 challenge 回显，禁止携带 HandInfo；`registered` 和后续全部业务 payload 强制加密。旧 v1 和携带秘密字段的注册帧 fail closed，不提供明文兼容旁路。

连接成功后绑定认证时的稳定 principal ID。Gateway 按连接 label 解析不含秘密的 Face identity，并在每个 command 上重新校验 principal ID 与 scope，因此删除后同名重建凭据不会让旧连接继承新权限。

当前单用户 Alpha 采用以下访问模型：

- 有效 Face identity 默认可访问该 Mind 中全部 conversation。
- 写操作仍由 scope 控制。
- 默认 token 不包含 `face:approve`。
- Face token 不得注册为 Hand，Hand token 不得注册为 Face。

正式 scope：

| Scope | 能力 |
|---|---|
| `face:chat` | 发起和取消 Chat |
| `face:sessions:read` | 列出 conversation、读取快照 |
| `face:sessions:write` | 创建和重命名 conversation |
| `face:runs:read` | 查询远程执行 |
| `face:runs:cancel` | 取消远程执行 |
| `face:approve` | 裁决审批 |
| `face:hands:read` | 查看 Hand 和工具能力 |
| `face:tasks:read` | 查询后台任务和日志 |
| `face:tasks:cancel` | 取消后台任务；同时需要 `face:tasks:read` |

## 消息类型

AI Face 发送：

```text
face.chat
face.chat.cancel
face.conversation.list
face.conversation.create
face.conversation.snapshot
face.conversation.rename
face.subscribe
face.approval.resolve
face.run.get
face.run.cancel
face.hand.list
face.hand.get
face.task.list
face.task.get
face.task.log
face.task.cancel
```

Mind 返回：

```text
face.accepted
face.result
face.error
face.snapshot
face.event
```

客户端应使用 `protocol.ValidateFacePayload` 对收发测试夹具做严格结构验证。验证器拒绝未知字段、未知枚举、尾随 JSON 和在 Face payload 中出现的 `session_id`。权限、资源归属、幂等状态和审批是否过期属于运行时检查，不由协议验证器裁决。

## Command 生命周期

每个 command 必须带非空 `request_id`。当前 Chat 与 Chat cancel 按 `(Face principal, request_id)` 提供以下幂等语义；其他 command 的通用 registry 仍属于后续增强：

- 相同 ID 和相同 payload 返回已有 accepted 或最终结果，不重复副作用。
- 相同 ID 和不同 payload 返回 `request_conflict`。
- 已接受的异步操作先返回 `face.accepted`，完成后恰好返回一个 `face.result`。
- 鉴权、scope 或结构检查未通过时返回 `face.error`，不得先返回 accepted。

当前同步查询也先返回 `face.accepted`，随后返回一个 `face.result`；snapshot 返回 accepted 后跟一个 `face.snapshot`；subscribe 在安装过滤器后以 accepted 完成。

Chat 示例 payload：

```json
{
  "request_id": "req-123",
  "conversation_id": "conv-1",
  "content": "在开发机上运行测试"
}
```

完整 envelope 示例：

```json
{
  "msg_id": "msg-1",
  "type": "face.chat",
  "session_id": "connection-session-1",
  "from": "agent-face-1",
  "to": "mind",
  "seq": 3,
  "payload": {
    "request_id": "req-123",
    "conversation_id": "conv-1",
    "content": "在开发机上运行测试"
  }
}
```

这里 envelope 的 `session_id` 合法；同名字段不得出现在 `payload`。

## 快照与订阅

AI Face 连接或重连后应以快照恢复，不把本地缓存视为权威：

```text
authenticate
  → face.subscribe
  → face.accepted(snapshot_version=N)
  → face.conversation.snapshot
  → face.snapshot(version>=N)
  → consume face.event
```

Gateway 必须先安装订阅再返回 accepted。`FaceEvent.EventSeq` 只在当前连接内递增；断线后不得用它推断遗漏状态，应重新获取 snapshot。

快照中的数组必须显式编码为空数组而不是 `null`：

- `messages`
- `pending_chats`
- `pending_approvals`
- `active_runs`

AI Face 只能依赖结构化 `data` 和稳定枚举，不得解析 `message` 展示文本。

## 审批与远程执行

- `approval.requested` 提供 `approval_id`、conversation、Chat request、工具、参数摘要和过期时间。
- 只有 `face:approve` identity 可以发送 `face.approval.resolve`。
- 决策值为 `allow_once`、`deny_once`、`allow_session` 或 `deny_session`。
- session 级决策只影响所属 conversation。
- Face 看到的批准不取代 Hand 最终守门；Hand 仍验证 Approval digest。
- `face.run.cancel` 只能请求 Mind 的 RemoteRun Authority 取消，不得直接向 Hand 发送 `rpc_cancel`。
- `face.task.cancel` 同时需要 `face:tasks:read` 和 `face:tasks:cancel`，成功响应包含 Hand outcome 与对账后的 task 快照。
- 裁决 reason 最多 1024 UTF-8 字节；审批事件、审计和 run 元数据都不包含原始工具参数。

## 正式事件

当前协议冻结以下业务事件：

```text
chat.started
chat.tool_called
chat.tool_completed
chat.completed
chat.failed
chat.cancelled
approval.requested
approval.resolved
remote_run.changed
hand.connected
hand.disconnected
conversation.changed
task.changed
```

每种事件的 `data` 都有对应 Go DTO 和严格校验。工具事件只暴露参数摘要，不应暴露原始敏感参数；内部 debug 日志不属于正式事件流。

## JSONL 客户端约定

`half-pi-face --mode headless` 已实现以下约定：

- stdin 每行一个 command JSON 对象。
- stdout 每行一个正式协议消息，不能混入日志。
- stderr 输出连接和诊断日志。
- 完成注册后首先输出已解密的正式 `registered` envelope，作为结构化 ready 状态。
- 连接或协议失败使用非零进程退出码。
- 任务失败通过 `face.result.status` 和稳定错误码表达，不因普通任务失败退出客户端进程。

stdin command 使用尚未 stamp 的 `protocol.Envelope`；客户端负责生成缺省 `msg_id`，并填充、绑定和加密连接字段：

```json
{"type":"face.conversation.list","payload":{"request_id":"req-list-1"}}
```

调用方可以显式提供 `msg_id`，但不得提供 `session_id`、`from`、`to` 或 `seq`。客户端会在发送前调用正式 payload 验证器，并拒绝服务端消息类型、未知字段和超限输入。stdout 返回的 envelope 已解密但保留 Mind 签发的连接字段，可直接按 `type` 和 typed payload 解码。

默认配置位于 `~/.half-pi/face/config.toml`，权限在 Unix 上收紧为目录 `0700`、文件 `0600`。凭据也可通过 `FACE_TOKEN`、`HALF_PI_FACE_APPLICATION_KEY`、`HALF_PI_FACE_ID` 和 `HALF_PI_FACE_SERVER` 注入。跨设备地址可以使用 `ws://` 或 `wss://`；Half-Pi 始终执行应用层双向鉴权、方向隔离密钥派生和 Envelope 加密，TLS/WSS 由反向代理等部署层按需提供。

## 安全要求

- 所有 Face 连接必须完成应用层四步鉴权和加密；禁止增加远程明文兼容旁路。
- token 不得进入日志、事件或 `face.result.data`。
- AI Face 默认使用最小 scope；执行审批测试时使用独立身份和隔离 Hand。
- 不发送原始工具参数、模型内部请求或未脱敏的工具结果到普通事件流。
- 客户端必须限制消息和本地缓存大小，不能把事件流当作无限历史。

## 验收状态与后续清单

异步审批 Broker、run/task cancel 和 Face identity 审计已作为 P3 runtime 基线完成。2026-07-19 的真实进程 E2E 使用动态端口、临时 HOME/SQLite/工作目录和 Scripted LLM，验证同 principal replay/conflict、跨 Face 恢复、run-bound 审批、远程取消、后台 task 对账、终端 REPL 快照与 SQLite 脱敏审计。

后续协议增强项是 conversation 写命令等非 Chat command 的通用幂等 registry。

以上运行时能力全部复用当前 wire protocol，不新增 AI 专用消息语义。
