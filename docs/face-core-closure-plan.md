# Face 核心协议与身份加密最终收口方案

## 状态与边界

本文是 2026-07-18 经逐项确认后冻结的实施规格。除实现中发现安全冲突或不可实现条件外，不再扩大范围。

本轮完成后可以声明：Face wire contract 已冻结；Hand/Face 凭据和身份边界可用；四步挑战握手与注册后业务 payload 强制加密可用；Face 后台 task 协议已冻结。

本轮包含：

- 收紧 Face DTO、枚举、验证和测试。
- 增加 Face task list/get/log/cancel、快照和事件协议。
- 增加独立 `face_tokens`、identity、scopes 和 REPL 管理。
- Hand/Face 分表存储，共享凭据代码抽象。
- Hand/Face 四步挑战握手和所有注册后业务 payload 加密。
- Hand 配置、CLI、环境变量和连接流程接入 application key。
- Hub 使用 `(peer_type, label)` 复合身份，允许同名 Hand 和 Face。
- 旧 Hand 凭据和旧三步握手 fail closed。

本轮不包含：Face Gateway command routing、Conversation Actor、Chat runtime、异步 Approval broker、出站背压、Headless/Web/TUI/IM Face 客户端、`face.task.start` 或明文兼容模式。

## 冻结决策

| 主题 | 决策 |
|---|---|
| 凭据存储 | Hand/Face 分表，共享生成和校验代码 |
| identity | label/token/application key 严格一对一且不可变 |
| label | `Register.ClientID` 即 label；格式 `[A-Za-z0-9][A-Za-z0-9._-]{0,63}` |
| 唯一性 | Hand、Face 各自命名空间唯一；同名 Hand/Face 可并存 |
| token/key | 分别生成 16 随机字节，均编码为 32 字符小写 hex |
| 服务端存储 | SQLite 明文；创建时一次显示，list 不显示秘密 |
| scope | Face 创建时显式指定；规范 JSON；变更需删除重建 |
| 握手 | versioned register → challenge → proof → registered |
| challenge | 32 随机字节，10 秒过期，绑定连接且单次使用 |
| 密钥派生 | HKDF-SHA-256 派生 proof、C→S、S→C 三把会话密钥 |
| 加密 | AES-128-GCM；registered 自身及其后全部 payload 强制加密 |
| 重复连接 | 同 type+label 拒绝新连接，旧连接保持 |
| 删除 | 立即断开对应 peer；不主动取消已接受工作 |
| task 权限 | `face:tasks:read`、`face:tasks:cancel` |
| task API | 只管理不直接启动 |
| task list | conversation 必填；status/hand 过滤；cursor 分页 |
| task snapshot | 所有活动 task + 近期终态；终态默认 50、可配置、硬上限 500 |
| task page | 默认 50、最大 200 |
| task log | byte offset/limit，base64 bytes，单次最大 64 KiB |
| task event | 单一 `task.changed`，携带完整摘要 |
| snapshot version | 从 1 开始 |
| active runs | 只允许非终态 run |
| subscribe 空过滤 | 空 conversation/event 数组均表示全部 |

## 凭据与存储

共享代码概念：

```go
type Credential struct {
    ID             int64
    Label          string
    Token          string
    ApplicationKey string
    CreatedAt      time.Time
}

type FaceCredential struct {
    Credential
    Scopes []protocol.FaceScope
}
```

共享代码负责 label 格式、随机 token/key、hex 编解码和常量时间比较。Store 保留明确的 Hand/Face CRUD 和认证函数，不把两类授权规则合并成一个通用 switch。

目标 schema：

```sql
CREATE TABLE hand_credentials (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    label TEXT NOT NULL UNIQUE,
    token TEXT NOT NULL UNIQUE,
    application_key TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE face_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    label TEXT NOT NULL UNIQUE,
    token TEXT NOT NULL UNIQUE,
    application_key TEXT NOT NULL UNIQUE,
    scopes TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

Face scopes 创建时去重、字典序排序并编码为无多余空白的 JSON 数组。空集合、未知 scope、数据库中的损坏 JSON 或非规范 scope 记录均拒绝。完整 scopes 增加：

```text
face:tasks:read
face:tasks:cancel
```

Mind SQLite 明文保存 token/key，因此 Mind 和 Hand 的凭据/config 目录必须为 `0700`，credential/config/database/backup 以及 SQLite WAL/SHM 文件必须为 `0600`。已有路径权限应收紧，无法收紧则启动失败；Unix 需要权限和 symlink 测试，Windows 使用等价 ACL。日志、错误、事件和 list 不得输出 token/key。数据库泄露等同凭据泄露。派生 S→C key 能证明服务端持有 application key，但不能证明网络端点属于预期主机，生产环境仍需 TLS。

## 迁移

现有 `hand_tokens` 没有 application key，不自动补 key、不从 token 派生 key、不接受旧握手。迁移幂等创建新的 `hand_credentials`，不复制旧记录；原 `hand_tokens` 原样保留但认证代码永久不再查询它，启动只记录旧表行数。后续删除旧表使用单独迁移。用户必须重新执行 `/hand add <label>`。

Hand 三要素 label/token/key 缺任一项时启动或首次连接失败，不进入无限重连。配置入口：

```toml
[server]
token = "<32-char-hex>"
application_key = "<32-char-hex>"

[hand]
id = "my-pc"
```

- CLI：`--token`、`--application-key`、`--id`。
- 环境变量：`HAND_TOKEN`、`HALF_PI_HAND_APPLICATION_KEY`。
- TOML：`server.token`、`server.application_key`。

## 四步挑战握手

新增消息：`register_challenge`、`register_proof`。四条握手消息都携带 `protocol_version=1`；不匹配直接拒绝，不降级。

### 1. Register

```go
type Register struct {
    ProtocolVersion int      `json:"protocol_version"`
    ClientID        string   `json:"client_id"`
    Token           string   `json:"token"`
    Type            string   `json:"type"`
    Info            *HandInfo `json:"info,omitempty"`
}
```

服务端严格解码，校验版本/type/label/token hex 格式和 type-specific info，之后才查询凭据。Hand 必须提供非空 OS、Arch、Hostname，WorkDir 可空；Face 必须省略 info，携带即拒绝。服务端按 type 查询分表并常量时间比较 label+token。此时连接不得加入 Hub。

### 2. Challenge

```go
type RegisterChallenge struct {
    ProtocolVersion int    `json:"protocol_version"`
    HandshakeID     string `json:"handshake_id"`
    ServerID        string `json:"server_id"`
    SessionID       string `json:"session_id"`
    Challenge       string `json:"challenge"`
    ExpiresAt       int64  `json:"expires_at"`
    Algorithm       string `json:"algorithm"`
}
```

challenge 是 32 随机字节的 base64，绑定当前 WebSocket 和候选 session，单次使用。整个四步握手 read deadline 和 challenge expiry 均为 10 秒；发送 challenge 后 deadline 不延长。算法固定 `AES-128-GCM`。

### 3. Proof

```go
type RegisterProof struct {
    ProtocolVersion int    `json:"protocol_version"`
    HandshakeID     string `json:"handshake_id"`
    Algorithm       string `json:"algorithm"`
    Proof           string `json:"proof"`
}
```

challenge 明文携带 Mind server ID。application key 只作为长期根密钥，不直接用于 GCM。规范 transcript 使用以下固定字段顺序的结构体 `json.Marshal`；Challenge 是收到的规范 base64 文本，解码后必须恰好 32 字节：

```go
type HandshakeTranscript struct {
    ProtocolVersion int    `json:"protocol_version"`
    PeerType        string `json:"peer_type"`
    Label           string `json:"label"`
    HandshakeID     string `json:"handshake_id"`
    ServerID        string `json:"server_id"`
    SessionID       string `json:"session_id"`
    Challenge       string `json:"challenge"`
}
```

令 `transcriptJSON=json.Marshal(struct)`、`transcriptHash=SHA256(transcriptJSON)`、`challengeBytes=base64.StdEncoding.DecodeString(Challenge)`。三把 16 字节 key 使用 HKDF-SHA-256：IKM 是解码后的 16 字节 application key，salt 是 challengeBytes，info 分别是 ASCII `half-pi/v1/register-proof/`、`half-pi/v1/client-to-server/`、`half-pi/v1/server-to-client/` 拼接 transcriptHash。派生 key 只保存在当前连接内。

proof 是用 `proof_key` 加密 challenge 原始字节所得的 `nonce || ciphertext` base64。`proofAAD=json.Marshal(RegisterProofAAD{...})`，精确结构为：

```go
type RegisterProofAAD struct {
    ProtocolVersion int    `json:"protocol_version"`
    Type            string `json:"type"`
    PeerType        string `json:"peer_type"`
    Label           string `json:"label"`
    HandshakeID     string `json:"handshake_id"`
    ServerID        string `json:"server_id"`
    SessionID       string `json:"session_id"`
}
```

其中 Type 固定为 `register_proof`，其他字段必须与 transcript 一致。

服务端验证连接、ID、type、label、server ID、session、expiry 和 GCM proof。无论成功失败，challenge 立即作废；失败关闭连接。未知 label、错误 token/key、畸形或过期 proof 对客户端统一为 `authentication_failed`，详细原因只写脱敏安全日志。

握手失败分类：本地缺失/格式错误凭据在连接前永久失败；`unsupported_protocol` 和统一的 `authentication_failed` 是永久错误，Hand 停止重试；`duplicate_peer` 是可重试错误，使用现有 capped backoff；网络断开和服务暂不可用没有握手协议错误码，同样可重试。`register_failed` 只允许作为内部日志类别，不是 wire error code。

### 4. Registered

proof 成功后创建 Session。Hub 先在锁内为 `(type,label)` 建立不可路由 reservation；密码和网络操作不持 Hub 锁。随后用派生的 S→C key 加密 registered payload并发送：

```go
type Registered struct {
    ProtocolVersion int    `json:"protocol_version"`
    ClientID        string `json:"client_id"`
    ServerID        string `json:"server_id"`
    SessionID       string `json:"session_id"`
}
```

registered Envelope 绑定候选 session、Mind/peer label，并作为服务端到客户端 `seq=1`。发送成功后 Hub 才把 reservation 原子提升为 routable peer；发送失败则释放 reservation。reservation 状态拒绝或排队任何业务发送，本轮选择拒绝。客户端使用 challenge 中的 server ID 构造候选 Session，先验证并 Accept Envelope，再用 S→C key 解密，并要求 registered 的 label/version/server/session 与 challenge 完全一致；只有成功后连接才成立。这同时证明服务端持有 application key。客户端首条业务消息使用 C→S `seq=1`，服务端后续业务消息从 S→C `seq=2` 开始。

## 业务加密

仅 register/challenge/proof 和握手失败 error 可明文。registered 及之后所有 Hand RPC、progress、task、info/event、Face command/response/snapshot/event 和业务 error 的 payload 都必须是 `EncryptedPayload`。

发送顺序：

```text
NewEnvelope → Session.Stamp(final headers) → Encrypt payload with Envelope.AAD() → WriteJSON
```

接收顺序：

```text
ReadJSON → Session.Accept(headers/seq) → DecodeEncryptedPayload → Decrypt with AAD → strict typed decode
```

必须新增 strict decrypt-and-decode helper，使用 `DisallowUnknownFields` 并拒绝 trailing JSON；不能直接复用当前宽松 `json.Unmarshal` helper。

保持工具原始 output 最大 1 MiB。为覆盖 JSON 对任意控制字节最坏约 6 倍转义，解密后的单个 plaintext payload 最大 7 MiB，WebSocket 加密 Envelope read limit 固定为 10 MiB。接收顺序先拒绝超过 10 MiB 的 frame，再将解密输出限制为 7 MiB，最后严格解码；1 MiB 工具原始输出仍是字段级上限。边界测试必须使用 1 MiB 最坏控制字节内容，而不只测试普通 ASCII。

注册后收到明文、未知算法、非法 base64、短密文、GCM/AAD 失败或解密后非法 JSON，立即关闭连接并写脱敏安全事件；不返回明文错误，不尝试降级。

## Hub 与服务分流

Hub 当前以裸 ID 为 key，必须改为：

```go
type PeerKey struct {
    Type  PeerType
    Label string
}
```

registry、重复检查、remove、lookup 和 send 都使用复合 key，并提供 `PeerByType`、`RemoveByType`、`SendToType`。裸 ID API 不得在歧义时静默选 peer。Envelope From/To 仍使用 label，但调用方必须显式指定目标 type。

同 type+label 在线时拒绝新连接，不替换旧连接。删除 Face 凭据立即断开 Face，但不取消已接受工作；删除 Hand 凭据立即断开 Hand，前台 run 按现有 disconnect 规则 lost，durable task 留在 Hand 进程继续运行。

本轮不做 Face Gateway，但必须做最小安全分流。Mind-level dispatcher 是 `Hub.OnHandshake`、`OnMessage`、`OnDisconnect` 的唯一所有者；`remoteexec.Authority` 改为暴露 Hand-specific handler，不再自行覆盖 Hub callback。dispatcher 按已认证 `Peer.Type` 路由：Hand 消息和 lifecycle 只进入 Authority，Face 消息绝不进入 Authority。Authority 的 disconnect handler 仍需防御性检查 `PeerHand`。Face handler 只解密、识别和校验协议，然后返回未实现错误，不发送 accepted。Face 断连不得影响 Hand run。

## REPL 管理

```text
/hand add <label>
/hand list
/hand remove --id <id>
/hand remove --label <label>
/face add <label> --scopes <comma-separated-scopes>
/face list
/face remove --id <id>
/face remove --label <label>
```

add 一次显示 label/token/application key；Face 还显示规范 scopes。list 只显示 ID、label、scopes（Face）和创建时间。Face scopes 必须显式、至少一个。删除后按 type 立即断开在线 peer。显式 `--id`/`--label` 避免数字 label 歧义。

即时断连只保证在拥有 Hub 的同一 Mind `--repl` 进程中执行。默认 service mode 本轮没有在线管理 IPC；离线修改数据库不视为即时撤销入口，也不列入本轮完成条件。后续本地管理 API 单独设计。单用户 Alpha 中任一有效 Face identity 可读取全部 conversation，写操作仍由 scope 控制。

## Face Task Wire Contract

新增 command/operation：

```text
face.task.list    / task.list
face.task.get     / task.get
face.task.log     / task.log
face.task.cancel  / task.cancel
```

不增加 `face.task.start`，启动仍经 Chat/tool/Approval/Authority/审计链路。

```go
type FaceTaskList struct {
    RequestID      string       `json:"request_id"`
    ConversationID string       `json:"conversation_id"`
    HandID          string       `json:"hand_id,omitempty"`
    Statuses        []TaskStatus `json:"statuses,omitempty"`
    Cursor          string       `json:"cursor,omitempty"`
    Limit           int          `json:"limit,omitempty"`
}

type TaskListResult struct {
    Tasks      []TaskSummary `json:"tasks"`
    NextCursor string        `json:"next_cursor,omitempty"`
}
```

conversation 必填；status 空表示全部；hand 精确过滤；默认 50、最大 200；排序 `(updated_at DESC, task_id DESC)`。opaque cursor 绑定 conversation/status/hand/排序锚点，修改过滤后复用 cursor 返回 `invalid_request`。

cursor 是 `versioned canonical JSON || HMAC-SHA256` 的 base64url 表示，HMAC key 在 Mind 进程启动时随机生成，不持久化；Mind 重启后旧 cursor 返回 `invalid_request`。下一页严格使用 `< (updated_at, task_id)` 的降序锚点。并发更新可能导致重复或跳页，客户端按 task ID 去重；重新 list 获取最新 best-known 视图。

```go
type FaceTaskGet struct {
    RequestID      string `json:"request_id"`
    ConversationID string `json:"conversation_id"`
    TaskID          string `json:"task_id"`
}

type TaskGetResult struct {
    Task TaskSummary `json:"task"`
}

type FaceTaskLog struct {
    RequestID      string `json:"request_id"`
    ConversationID string `json:"conversation_id"`
    TaskID          string `json:"task_id"`
    Offset          int64  `json:"offset"`
    Limit           int    `json:"limit"`
}

type TaskLogResult struct {
    TaskID     string `json:"task_id"`
    Offset     int64  `json:"offset"`
    NextOffset int64  `json:"next_offset"`
    Data       []byte `json:"data"`
    EOF        bool   `json:"eof"`
    Truncated  bool   `json:"truncated"`
}

type FaceTaskCancel struct {
    RequestID      string `json:"request_id"`
    ConversationID string `json:"conversation_id"`
    TaskID          string `json:"task_id"`
    Reason          string `json:"reason,omitempty"`
}

type FaceTaskCancelResult struct {
    Outcome string      `json:"outcome"`
    Task    TaskSummary `json:"task"`
}
```

四个 task command 都使用统一生命周期：成功登记后发送一个 `face.accepted`，之后恰好一个 `face.result`。result data 分别严格解码为 `TaskListResult`、`TaskGetResult`、`TaskLogResult` 和 `FaceTaskCancelResult`。

list/get/log 需要 `face:tasks:read`；cancel 同时需要 `face:tasks:cancel` 和 `face:tasks:read`，因为结果包含完整 TaskSummary。Gateway 必须核对 conversation 归属，跨 conversation 查询与不存在统一映射 `task_not_found`，避免泄露。日志 offset 非负，limit 1..65536，`[]byte` 使用 JSON base64。cancel 复用 `TaskService.Cancel`，outcome 为 `cancelled|already_done|unknown_task|failed`：cancelled/already_done 在成功对账后返回 succeeded；unknown_task 映射 failed/task_not_found；failed 映射 failed/task_failed；确认取消但 status 对账失败映射 failed/task_stale，并在 data 中返回 stale best-known task。

```go
type TaskSummary struct {
    TaskID         string        `json:"task_id"`
    ConversationID string        `json:"conversation_id"`
    HandID          string        `json:"hand_id"`
    Tool            string        `json:"tool"`
    ArgsDigest      string        `json:"args_digest"`
    Status          TaskStatus    `json:"status"`
    CreatedAt       time.Time     `json:"created_at"`
    StartedAt       *time.Time    `json:"started_at,omitempty"`
    FinishedAt      *time.Time    `json:"finished_at,omitempty"`
    UpdatedAt       time.Time     `json:"updated_at"`
    LogBytes        int64         `json:"log_bytes"`
    Truncated       bool          `json:"truncated"`
    Stale           bool          `json:"stale"`
    ErrorCode       FaceErrorCode `json:"error_code,omitempty"`
    Error           string        `json:"error,omitempty"`
}
```

不得暴露原始 args、Hand DB/log path、token/key。新增稳定错误码：`task_failed`、`task_cancelled`、`task_timed_out`、`task_lost`、`task_stale`、`task_not_found`、`hand_offline`、`log_unavailable`。客户端只依赖 code；message 仅展示。

TaskSummary 的终态 code 只由 status 决定：failed/cancelled/timed_out/lost 分别映射同名 task code，succeeded 无 code；pending/running 不因 stale 改写 code，`Stale` 单独表达新鲜度。`task_not_found`、`hand_offline`、`log_unavailable` 是操作级 FaceResult/FaceError code，不写入 TaskSummary。Face projection 必须把内部错误映射为有界、脱敏文本，不得透传 Hand ErrorMsg、路径、SQLite 或 transport 详情；原始细节只进入受保护日志。stale 对账错误不能覆盖 Hand 已确认的 execution error。

## Snapshot、事件与现有不变量

Snapshot 新增：

```go
Tasks                []TaskSummary `json:"tasks"`
TaskHistoryLimit     int           `json:"task_history_limit"`
TaskHistoryTruncated bool          `json:"task_history_truncated"`
```

只有具备 `face:tasks:read` 的 identity 才能接收 task 数据。缺少该 scope 时 conversation snapshot 仍可成功，但 `tasks=[]`、`task_history_limit=0`、`task_history_truncated=false`；订阅 `task.changed` 显式返回 `forbidden`，空 event filter 的“全部”自动排除 task.changed。其他 snapshot 字段继续按各自 scope 规则投影。

包含全部非终态 task（不计上限）和最近 N 个终态 task。N 默认 50，可配置为 0；500 是终态历史硬上限，不是 snapshot 总 task 上限。配置越界启动失败。超过 N 时 truncated=true，tasks 必须非 nil。

snapshot/list 首先返回 Mind SQLite 的 best-known 状态，并保留 `stale`。对于在线 Hand 上的非终态 task，snapshot/list/reconnect 异步触发有界对账，不阻塞首个响应；start/get/cancel 继续按现有路径对账。本轮不引入周期轮询，因此不承诺 task completion 近实时推送。

新增 `task.changed`，data 携带完整 `TaskSummary`。顶层 conversation ID 必填并与 task 一致，request ID 可选。事件仅在 start/get/cancel/snapshot/list/reconnect 的观察或对账发现快照变化时发送；未发生观察时 Hand 终态可以暂未投影。客户端按完整摘要替换本地状态。

事件关联冻结：chat.* 顶层 conversation/request 必填；approval.* conversation 必填且有 Chat 关联时 request 必填；`remote_run.changed` conversation 必填；task.changed 和 conversation.changed conversation 必填、request 可选；Hand connect/disconnect 不得带 conversation/request。

其他不变量：

- `FaceResult.succeeded` 禁止 error/code；failed/cancelled/timed_out 必须有合法非空 code 和 error。
- 权威 snapshot version 和 conversation.changed version 从 1 开始；accepted 中 0 表示省略。
- `active_runs` 只允许 created/approved/sent/accepted/running/cancel_requested。
- subscribe 的空 conversation/event 数组表示全部；空元素、重复值和未知枚举拒绝。

## 五阶段提交

1. `feat: finalize Face task wire protocol`
   - task DTO/scope/error/event/snapshot；现有不变量；gateway-core race/vet。
2. `feat: add typed peer credentials`
   - 分表 schema、迁移、共享凭据 helper、Store CRUD、scope JSON、REPL 存储层；Mind store race/vet。
3. `feat: require encrypted peer sessions`
   - versioned 四步握手、HKDF 三 key、加密 registered/Session、复合 peer key、10 MiB wire limit、重复连接和失败断连；gateway-core race/vet。
4. `feat: secure Hand transport with application keys`
   - Hand config/CLI/env、Mind 分流、REPL 完整管理、所有 Hand 消息加密；四模块 race/vet、build、Windows 交叉编译。
5. `docs: freeze Face identity and encryption contract`
   - 更新 Face/AI Face/README/AGENTS/活动计划和旧 Hand 重建步骤；最终全仓验收。

## 测试与完成定义

必须覆盖：

- 所有新 payload 严格 JSON round-trip、未知字段和 trailing data。
- scope/operation/error/status/event 全枚举与 FaceResult 真值表。
- task cursor/limit/status/log/base64/offset、snapshot 截断和 active_runs 约束。
- Hand/Face 同类型 label 唯一、跨类型同名允许；token/key 独立唯一；scope JSON 规范化。
- migration 可重复，legacy Hand 凭据不可认证。
- 错 label/token/key/type/version，challenge 过期/重放/跨连接，proof/AAD 篡改。
- 派生 key 用途/方向隔离，伪造 registered 无法通过服务端持钥证明。
- proof 前 peer 不可见；同 type+label 新连接拒绝且旧连接不受影响。
- 注册后明文、未知 alg、非法密文立即断连。
- Envelope msg/type/session/from/to/seq 任一篡改均无法解密。
- 最大合法 1 MiB 最坏控制字节 RPCResult 经加密后可在 10 MiB wire limit 内往返，超限 frame 被拒绝。
- authentication/protocol 永久错误停止重试，duplicate/network 错误按 capped backoff 重试。
- snapshot/list 的 best-known/stale 语义和按观察触发的异步 task 对账事件。
- Unix 权限、symlink、SQLite WAL/SHM 和 Hand config 权限；Windows 等价 ACL 行为。
- Face/Hand 分流，Face 断连不影响 Hand run，按类型删除只断开目标 peer。
- 现有远程执行、progress、durable task 在加密 transport 下无回归。
- `make test`、`make build`、四模块 vet、Windows tools 四架构测试交叉编译、Windows amd64 Mind/Hand 构建通过。

只有以上五阶段全部完成，且文档明确 Face Gateway、Conversation Actor、Chat runtime 和客户端仍未实现，才可将本轮标记完成。之后原则上不再修改 wire contract，除非发现安全漏洞或不可实现冲突。
