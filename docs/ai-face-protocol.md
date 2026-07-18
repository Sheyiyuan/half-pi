# AI Face 协议接入指南

## 状态

本文面向未来的 Headless Agent Face、自动化客户端和其他 AI Agent。统一 wire protocol、独立 Face 凭据、四步挑战握手和注册后强制加密已经实现并通过 race 测试；Face Gateway、Chat runtime、审批 broker 和客户端尚未实现，当前连接只会收到加密的“运行时未实现”错误，本文不声明业务可用。

架构和完整生命周期见 [`face-protocol.md`](face-protocol.md)。本文只说明客户端应依赖的正式协议契约。

## 接入原则

- AI Face 与人类 Face 使用相同的 `face.*` 消息，不存在测试后门。
- 所有消息由 `protocol.Envelope` 包装。
- `Envelope.SessionID` 是单次 WebSocket 连接的防重放标识。
- Face payload 使用 `conversation_id` 表示持久化对话，不得发送 `session_id`。
- `Envelope.MsgID` 标识一次传输，`request_id` 标识一次业务 command，两者不能互换。
- Face 不直接调用 Hand，也不自行裁决 run 终态；Chat、审批和取消由 Mind 权威处理。

## 鉴权假设

Face 注册使用 version 1 WebSocket 四步挑战握手，必须同时使用与 Hand 分离的 Face token 和 application key。服务端按 Face 凭据表认证，注册成功后业务 payload 全部强制加密；Face scope 已随凭据规范存储，后续 Gateway 将其解析为授权主体。

当前阶段已实现注册和安全分流，但尚未实现 scope 驱动的 command routing。未来单用户 Alpha 采用以下访问模型：

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

每个 command 必须带非空 `request_id`。未来 Mind 按 `(Face identity, request_id)` 提供幂等语义：

- 相同 ID 和相同 payload 返回已有 accepted 或最终结果，不重复副作用。
- 相同 ID 和不同 payload 返回 `request_conflict`。
- 已接受的异步操作先返回 `face.accepted`，完成后恰好返回一个 `face.result`。
- 鉴权、scope 或结构检查未通过时返回 `face.error`，不得先返回 accepted。

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
```

每种事件的 `data` 都有对应 Go DTO 和严格校验。工具事件只暴露参数摘要，不应暴露原始敏感参数；内部 debug 日志不属于正式事件流。

## JSONL 客户端约定

未来 Headless Agent Face 应遵循：

- stdin 每行一个 command JSON 对象。
- stdout 每行一个正式协议消息，不能混入日志。
- stderr 输出连接和诊断日志。
- 完成注册后输出结构化 ready 状态。
- 连接或协议失败使用非零进程退出码。
- 任务失败通过 `face.result.status` 和稳定错误码表达，不因普通任务失败退出客户端进程。

## 安全要求

- 非 loopback 连接必须使用 TLS/WSS 或完成应用层加密接入。
- token 不得进入日志、事件或 `face.result.data`。
- AI Face 默认使用最小 scope；执行审批测试时使用独立身份和隔离 Hand。
- 不发送原始工具参数、模型内部请求或未脱敏的工具结果到普通事件流。
- 客户端必须限制消息和本地缓存大小，不能把事件流当作无限历史。

## 未来实现清单

1. Face Gateway 的 scope 校验、幂等 registry、有界出站队列和慢连接策略。
2. 快照聚合和结构化事件投影。
3. Chat accepted/result/cancel 和异步审批 broker。
4. JSONL Headless Agent Face 与确定性 Scripted LLM E2E。

以上运行时能力全部复用当前 wire protocol，不新增 AI 专用消息语义。
