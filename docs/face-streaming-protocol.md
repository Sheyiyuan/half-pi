# Face 流式协议与可恢复传输设计

> 状态：已实现（2026-07-20）。本文定义 Half-Pi Face 流式传输的正式协议、状态机、背压、恢复、持久化和验收要求。现有 v2 四步加密握手保持不变；本文只演进注册完成后的 Face 应用协议。

## 1. 目标

一次 `face.chat` 可能经历多轮“模型响应 -> 工具调用 -> 模型响应”。Face 需要在最终回答产生前看到可见文本，并能同时观察远程 run 的 stdout/stderr。流式能力必须满足：

1. `face.accepted`、`face.result`、`face.error`、快照、审批和终态事件仍是可靠控制面。
2. 聊天文本增量和 run 输出是可丢弃、可补偿的数据面，不能阻塞模型、工具或可靠控制消息。
3. `face.result.Content` 和持久化消息是聊天终态的权威内容；增量不是持久化提交。
4. 多个具有权限并显式订阅的 Face 可以同时观察同一个 Chat 或 run。
5. Face 通过流快照恢复活动 Chat；完成后的恢复统一读取 conversation 消息。
6. 工具参数、模型隐藏 reasoning、token、application key 和内部错误不得进入流。
7. 所有增量仍使用现有 Envelope、单调连接序号和 AES-128-GCM 加密，不使用明文或 WebSocket fragmentation 表达业务流。

## 2. 非目标

- 不对每个模型 token 单独建立可靠确认。
- 不保证慢 Face 收到每个瞬时 chunk。
- 不把瞬时流写入普通 Face domain event 历史。
- 不启用 WebSocket `permessage-deflate`。payload 加密后不可有效压缩，压缩明文还会扩大侧信道面。
- 不向 Face 暴露 OpenAI、Anthropic 或 Gemini 的厂商原始流格式。

## 3. 协议分层

```text
Provider SSE/JSON stream
        |
        v
llm.StreamingProvider        厂商事件解析、工具调用分片组装
        |
        v
agentcore lifecycle          model.delta / assistant.before_deliver
        |
        v
agentcore.ChatTransport      只承担可见文本、响应边界和传输背压
        |
        v
facegateway stream broker    聚合、序列、订阅、恢复状态
        |
        v
outbound scheduler           reliable / transient 背压与单发送循环
        |
        v
encrypted Face Envelope      face.chat.delta / face.run.progress
```

控制面与数据面共用一条 WebSocket，因此最终线序仍由单发送循环决定。两者只在 Gateway 入队策略上分级。

## 4. 能力协商

不得给 `registered` 增加字段。现有客户端对 payload 使用未知字段拒绝，修改握手 DTO 会迫使 Hand 和 Face 同时升级，也会把应用能力与密码学版本错误耦合。

新客户端注册后首先发送：

```text
face.capabilities.get
```

成功结果包含：

```go
type FaceCapabilitiesResult struct {
    Revision     int                    `json:"revision"`
    Identity     FaceIdentity           `json:"identity"`
    Features     []FaceFeature          `json:"features"`
    Limits       FaceProtocolLimits     `json:"limits"`
}
```

初始 feature：

```text
chat_stream.v1
chat_stream_resume.v1
run_progress.v1
message_pagination.v1
```

旧 Mind 会对未知 command 返回 `invalid_request`，连接保持有效。新客户端收到该错误后可以退回现有非流式协议；只有服务端声明 feature 后，客户端才能发送对应新增字段或 command。

## 5. 订阅

`face.subscribe` 增加：

```go
TransientTypes []FaceTransientType `json:"transient_types,omitempty"`
```

语义：

- 省略或空列表表示不接收任何瞬时数据，保证旧客户端不会意外收到未知消息。
- `chat.delta` 需要 `face:sessions:read`。
- `run.progress` 需要 `face:runs:output`；该 scope 独立于只读 run 摘要的 `face:runs:read`。
- conversation 过滤同时作用于 domain event 和瞬时流。
- 瞬时订阅只影响推送，不改变 command response。

## 6. Chat 流

### 6.1 增量

Mind 向所有匹配订阅的 Face 发送：

```go
type FaceChatDelta struct {
    ConversationID string `json:"conversation_id"`
    RequestID      string `json:"request_id"`
    ResponseIndex  int    `json:"response_index"`
    Seq            int64  `json:"seq"`
    Offset         int64  `json:"offset"`
    Delta          string `json:"delta"`
}
```

- `(conversation_id, request_id)` 唯一标识活动 Chat。
- `response_index` 从 1 开始，每次 LLM 请求递增。工具循环中的中间文本和最终文本属于不同 response。
- `seq` 在整个 Chat 内从 1 单调递增，按 Gateway 实际生成的聚合 chunk 计数。
- `offset` 是该 response 可见 UTF-8 文本中的起始字节位置。
- `delta` 必须是非空合法 UTF-8，最大 4 KiB，拆分不得切断 rune。
- Face 只有在 `seq == last_seq+1` 且 `offset == len(response)` 时直接追加；否则进入恢复流程。

Gateway 不逐 token 发包。默认每 30 ms 或累计 1 KiB 刷新一次，单 chunk 最大 4 KiB。响应完成、工具调用开始和 Chat 终态前必须同步 flush，避免文本被后续生命周期事件越过。

### 6.2 终止屏障

订阅流的 Face 对每个 Chat 恰好收到一个可靠终止屏障：

```go
type FaceChatStreamEnd struct {
    ConversationID string           `json:"conversation_id"`
    RequestID      string           `json:"request_id"`
    LastSeq        int64            `json:"last_seq"`
    ResponseCount  int              `json:"response_count"`
    Complete       bool             `json:"complete"`
    Status         FaceResultStatus `json:"status"`
}
```

- `complete=true` 仅表示 Core 得到并持久化了完整 Chat 结果。
- 取消、超时、provider 错误或持久化错误使用 `complete=false`。
- 屏障属于可靠控制面，排在该 Chat 已成功入队的 delta 之后。
- 发起连接随后仍收到唯一 `face.result`；其他订阅者通过 terminal event 和 conversation 快照取得终态。

### 6.3 活动流恢复

命令：

```go
type FaceChatStreamGet struct {
    RequestID       string `json:"request_id"`
    ConversationID  string `json:"conversation_id"`
    TargetRequestID string `json:"target_request_id"`
}
```

结果：

```go
type ChatStreamResponse struct {
    ResponseIndex int    `json:"response_index"`
    Content       string `json:"content"`
    Complete      bool   `json:"complete"`
}

type ChatStreamGetResult struct {
    TargetRequestID string               `json:"target_request_id"`
    LastSeq         int64                `json:"last_seq"`
    Responses       []ChatStreamResponse `json:"responses"`
    Terminal        bool                 `json:"terminal"`
    Status          FaceResultStatus     `json:"status,omitempty"`
}
```

恢复流程：

```text
subscribe transient streams
  -> accepted (subscription installed)
  -> request conversation snapshot
  -> for every pending Chat: face.chat.stream.get
  -> buffer deltas received before stream.get result
  -> install snapshot at last_seq=S
  -> discard buffered seq<=S, append contiguous seq>S
```

Gateway 在 request registry 中保留活动流的完整可见文本，并与 Chat 终态 replay 使用相同的 10 分钟/数量上限。流状态有界；超过硬上限时 Chat 失败，不允许无限占用内存。

## 7. Provider 流归一化

`llm.Provider.Chat` 保留为兼容路径，新增可选 `StreamingProvider`。Core 总是调用统一 helper：原生流 provider 边解析边回调；同步 provider 成功后产生一个完整文本 delta。

厂商适配要求：

- OpenAI compatible：`stream=true`，解析 SSE `data:`；按 `tool_calls[index]` 组装 id/name/arguments。
- Anthropic：解析 `message_start`、`content_block_start/delta/stop`、`message_delta`、`message_stop`；只投影 `text_delta`，内部组装 `input_json_delta`。
- Gemini：调用 `streamGenerateContent?alt=sse`；按 candidate/part 归一化文本和 function call。
- SSE decoder 必须支持 CRLF、多行 `data:`、注释和 EOF，且对单事件和错误 body 设置独立上限。
- provider 返回的 `LLMResponse.Content` 必须精确等于该 response 已回调文本的拼接结果。
- reasoning/thinking block 默认丢弃，不作为可见文本。

## 8. Core 与工具循环

`Core.ChatWithTransport` 接收显式 `ChatTransport`。它不是 context Hook，也不能注册安全策略；Core 为每次 LLM 调用分配 `response_index`，并保证：

1. 文本 delta 按 provider 顺序投影。
2. `ResponseCompleted` 在该 response 的所有 delta 后触发。
3. 工具参数必须完整组装后才能计算摘要并发布 `chat.tool_called`。
4. 原始工具参数永远不进入 Chat 流。
5. 当前 provider response 中途失败时，不把部分 assistant message 写入历史。
6. 完整 assistant response 通过 `assistant.before_deliver` 后先持久化，再进入 `ChatTransport`；传输成功后才发布 `assistant.delivered`。
7. 同一 response 的全部 tool results 在执行完成后作为后续批次持久化，再进入下一轮模型请求。

只要当前 scope 匹配 `assistant.before_deliver` Guard 或 Transformer，Core 自动使用 buffered 模式：provider delta 只生成内部 lifecycle 事件，审核通过后把完整可见内容作为一个 transport delta 交付。没有完整输出拦截器时保持 passthrough 增量体验。这样完整输出策略不会在文本已经发给 Face 后才尝试撤回。

## 9. Run 输出流

现有 Hand `rpc_progress` 的 run 内 `seq`、4 KiB chunk、1 MiB/256 事件上限保持不变。Mind Authority 增加显式 progress observer，Face Gateway 不解析 EventBus 展示文本。

```go
type FaceRunProgress struct {
    ConversationID string       `json:"conversation_id"`
    RequestID      string       `json:"request_id,omitempty"`
    RunID          string       `json:"run_id"`
    Seq            int64        `json:"seq"`
    Kind           ProgressKind `json:"kind"`
    Data           string       `json:"data"`
    Gap            bool         `json:"gap"`
}
```

- `seq` 沿用 Hand 的 run sequence，Face 可同时检测 Hand 侧和 Face 队列侧缺口。
- `gap` 表示 Mind 在接纳该进度前已发现来源缺口；Face 自身还必须比较本地 `last_seq`。
- 只投影 foreground run。durable task 日志继续使用 `face.task.log` 的 offset/limit 拉取，避免同一输出出现两套恢复权威。
- 进度审计已持久化，但首版不新增 foreground run 日志下载命令；缺口时 UI 标记输出不完整，run 终态仍由 `remote_run.changed`/`run.get` 决定。
- Mind 为远程真实工具创建 `PreparedExternal`。如果当前 lifecycle scope 注册了 `tool.result_before_commit` Transformer，原始 `rpc_progress` 仍进入 RemoteRun 的受限 operational audit 和内部脱敏 lifecycle 事实，但 progress policy 返回 false，Face 不再收到未经同等过滤的原始输出；最终结果经过 Transformer 后再交付。

## 10. 出站调度与背压

每个 Face 连接保留一个发送 goroutine，但普通 channel 改为有界 scheduler：

| 类别 | 示例 | 队列满策略 |
|---|---|---|
| reliable | accepted/result/error/snapshot、审批、终态事件、stream.end | 先驱逐 transient；仍无法入队则断开慢 Face |
| transient | chat.delta、run.progress | 与同流尾项合并或丢弃，不得导致业务失败 |

初始限制：

- 总队列最多 256 项。
- transient 最多占 128 项和 512 KiB。
- 总 queued payload 最多 8 MiB；单个合法 snapshot/result 可独占队列预算。
- scheduler 在锁内只做有界内存操作；加密和 socket write 仍只在发送 goroutine执行。

任何被丢弃的 transient 都已经拥有源 `seq`。下一条成功消息会产生序号跳跃，客户端据此调用恢复或标记 gap。可靠消息永不静默丢弃。

## 11. Append-only 消息与历史分页

当前 `ReplaceMessages` 每轮删除并重建整个会话，会改变 message ID/created_at，无法作为稳定分页和流终态对账依据。消息存储改为 append-only：

- `messages` 增加 `request_id`，并建立 `UNIQUE(session_id, seq)`。
- 用户消息在 Chat 开始时幂等追加。
- 每个完整工具 response batch 和最终 assistant message 按 expected last seq 事务追加。
- `FaceMessage.RequestID` 必须从存储投影，不再恒为空。
- migration 保留现有消息和 seq，不重写历史。

新增分页 command：

```go
type FaceConversationMessages struct {
    RequestID      string `json:"request_id"`
    ConversationID string `json:"conversation_id"`
    BeforeSeq      int    `json:"before_seq,omitempty"`
    Limit          int    `json:"limit,omitempty"`
}

type ConversationMessagesResult struct {
    Messages      []FaceMessage `json:"messages"`
    NextBeforeSeq int           `json:"next_before_seq,omitempty"`
    HasMore       bool          `json:"has_more"`
}
```

`before_seq=0` 表示从最新消息向前读取。响应中的消息始终按 seq 升序排列。默认 100，最大 500。conversation 快照继续提供现有权威状态；TUI 使用分页 command 构建长历史视图。

## 12. 顺序与失败语义

发起连接的典型顺序：

```text
face.chat
face.accepted
face.event(chat.started)
face.chat.delta(response=1)*
face.event(chat.tool_called/tool_completed)*
face.chat.delta(response=2)*
face.chat.stream.end
face.event(chat.completed|failed|cancelled)
face.result
```

`conversation.changed` 可能在流中穿插，因为每个完整持久化批次都会推进权威版本。客户端不得用它推断 Chat 已终止。

错误规则：

- delta 之后 provider 失败：发送 `stream.end complete=false`，不持久化当前部分 response，最终 `face.result failed`。
- Face 断开：Chat 不取消；其他订阅 Face 继续接收。
- 原发起 Face 重连：相同 command replay 只返回 accepted/result，不隐式重放 delta；活动前缀通过 `stream.get` 恢复。
- transient 缺口：Chat/run 状态不失败。
- reliable 队列无法入队：只断开该慢 Face，不影响 Chat、其他 Face 或 Hand。

## 13. 安全边界

- Chat delta 只包含 provider 标记为用户可见的 text。
- tool call arguments 只在 Core 内组装，Face 仍只看到 SHA-256 摘要。
- run progress 新增独立 `face:runs:output` scope；observer profile 默认不包含，operator profile显式包含。
- 所有字符串执行 UTF-8、字节上限和枚举校验；客户端渲染前仍需移除 C0/C1/ANSI 控制字符。
- 日志不得记录 delta 正文、工具参数、token、application key 或 SSE 原始错误 body。受保护 debug 日志也只记录长度、序号和稳定错误类别。

## 14. 实现映射

| 层 | 主要实现 |
|---|---|
| Protocol | `modules/gateway-core/protocol/face.go`、`face_validate.go` |
| Provider | `modules/half-pi-mind/internal/llm/sse.go`、`openai.go`、`anthropic.go`、`gemini.go` |
| Core | `modules/half-pi-mind/internal/agentcore/chat.go`、`chat_hooks.go` |
| Store | `modules/half-pi-mind/internal/store/message.go`、`store.go` |
| Gateway | `modules/half-pi-mind/internal/facegateway/chat_stream.go`、`chat_registry.go`、`commands.go` |
| Transport | `modules/half-pi-mind/internal/facegateway/outbound.go`、`gateway.go` |
| Run | `modules/half-pi-mind/internal/remoteexec/authority.go`、`registry.go` |
| Terminal Face | `modules/half-pi-face/internal/tui/app.go`、`reducer.go`、`requests.go`、`view.go` |

Headless Face 继续严格透传所有正式 server message；全屏 terminal Face 在注册后查询 capability，只在服务端声明 feature 且当前 identity 具有对应 scope 时订阅瞬时流。

## 15. 验收矩阵

- 三个真实 adapter 的文本、Unicode、工具参数分片、usage、provider error 和 context cancel。
- 同步 Provider fallback 只产生一个 delta。
- 一次 Chat 多个 response_index，文本不会越过 tool lifecycle。
- 两个订阅 Face 收到相同源 seq；未订阅或缺 scope 的 Face 收不到数据。
- 慢 Face 丢 transient 或断开时，另一个 Face 和 Chat result 不受影响。
- delta 丢失后 `stream.get` 可以恢复完整活动前缀。
- 断线期间 Chat 完成后，conversation snapshot/messages 和 request replay 返回权威终态。
- run progress 保持 seq/kind/gap，且终态后不再投影。
- 旧客户端未协商 transient 时只收到原有五类 Face server message。
- message ID、seq、created_at 在后续 Chat 后保持稳定；分页无重复、无跳项。
- `go test -race -count=1 ./...` 覆盖全部五个模块，并通过真实 v2 加密链路秘密扫描。

## 16. 兼容与运维

- 旧 Face 不发送 `transient_types`，因此不会收到新增瞬时消息；原有 command、response 和 event 行为保持不变。
- 新 Face 连接旧 Mind 时，`face.capabilities.get` 返回 `invalid_request` 后退回非流式模式，连接无需重建。
- 不修改 v2 握手、`registered` 或密钥派生，Hand 与未升级 Face 不需要同步升级密码学实现。
- SQLite 启动迁移为消息增加 `request_id` 和稳定 `(session_id, seq)` 唯一约束，保留既有消息，不重写 ID、seq 或时间。
- 单 Chat 可恢复文本上限 2 MiB/2048 chunk；单连接 transient 队列上限 128 项/512 KiB，总队列上限 256 项/8 MiB。达到 Chat 硬上限时 Chat 明确失败；瞬时队列满只产生可检测缺口。
- durable task 输出仍以 Hand 日志和 `face.task.log` 为权威，不进入 foreground progress 推送。
- 排障优先检查 capability、identity scopes、`stream.end`、最终 `face.result` 和消息分页；不得依赖 delta 正文日志，因为正文按安全要求不记录。

验证命令：

```bash
cd modules/gateway-core && go test -race -count=1 ./...
cd modules/half-pi-core && go test -race -count=1 ./...
cd modules/half-pi-mind && go test -race -count=1 ./...
cd modules/half-pi-face && go test -race -count=1 ./...
cd modules/half-pi-hand && go test -race -count=1 ./...
make build
```
