# 工具透明可见性

## 状态与范围

本文定义 Face 应用协议 revision 3 的工具详情契约。它面向由个人 operator 直接控制 Mind、Face 和 Hand 的场景，使用户能检查 Agent 实际提交的参数、执行进度和可靠终态，而不是只看到工具名和摘要。

该能力不把 Half-Pi 变成多租户秘密隔离系统。`transparent` 会把经过展示策略处理的参数和结果发送给 Face，也允许这些展示投影进入用户自己的本地历史与诊断记录。

> 风险：未被 schema 或中央高置信规则标记的字段默认原样展示。用户自己放入命令、文件内容、URL 或其他普通字符串中的 token、密码和私钥可能被展示并记录。常见秘密扫描只产生 `scan_warnings`，不会静默改写用户数据。

安全审批仍绑定冻结调用的原始 `args_digest`。展示投影不能替代 digest，也不能改变 ToolRuntime、Authorizer、Hand 最终守门或审计结果。

## 连接协商

`face.subscribe` 增加连接级详情模式：

```json
{
  "request_id": "subscribe-1",
  "conversation_ids": ["conversation-1"],
  "event_types": ["chat.tool_called", "chat.tool_completed"],
  "transient_types": ["chat.tool.progress", "run.progress"],
  "detail_mode": "transparent"
}
```

模式只有两个值：

| 模式 | 参数和终态 | progress | task log |
|---|---|---|---|
| `transparent` | 返回版本化展示投影 | 可订阅有界增量 | 返回 admission 允许的日志正文 |
| `summary` | 只返回工具、状态、长度、digest、稳定错误类别和告警 | 不投递工具/run 原始增量 | 只返回长度、offset、digest 和截断状态 |

profile 规则固定如下：

- `operator` 省略 `detail_mode` 时默认为 `transparent`。
- `observer` 省略时默认为 `summary`。
- `observer` 显式请求 `transparent` 返回 `forbidden`；连接字段不能提升凭据权限。
- 客户端可通过 `face.capabilities.get` 协商 `tool_visibility.v1`，以启用工具 progress UI；revision 3 的可靠工具事件和详情模式本身不是 v2 兼容旁路。

详情模式在每次工具、foreground run 或后台 task admission 时冻结。之后即使发起 Face 断开、另一个 Face 改用不同模式订阅，或者 task 跨重连继续执行，也不能把 summary admission 升级成透明历史。透明 admission 可以对 summary 订阅者降级投影。

## 参数投影

`executor.PropertySchema.Display` 声明用户展示策略：

| 状态 | wire 行为 |
|---|---|
| `show` | `value` 携带原值 |
| `mask` | `value` 固定为 `[masked]` |
| `hide` | 保留字段名和状态，不携带值 |
| `preview` | 携带 UTF-8 安全前缀、原始字节数和 `truncated` |

未声明策略的字段默认为 `show`。中央高置信字段名规则优先于工具声明，可覆盖显式 `show` 或 `preview`；显式 `hide` 仍保持更严格的隐藏。例如 `password`、`token`、`api_key`、`application_key`、`private_key` 和 `secret` 使用 `mask`。中央规则递归处理 object 和 array，嵌套字段使用稳定路径，如 `config.token` 和 `items[0].password`。

schema 的显式策略与中央规则负责变换；内容扫描只告警。这样用户能区分“系统按契约遮罩”与“扫描器怀疑这里可能有秘密”。

`chat.tool_called.data` 的透明视图示例：

```json
{
  "request_id": "chat-1",
  "tool": "exec_command",
  "args_digest": "sha256:...",
  "projection_version": "tool-display.v1",
  "args": {
    "projection_version": "tool-display.v1",
    "fields": {
      "command": {"state": "show", "value": "git status", "bytes": 12},
      "api_token": {"state": "mask", "value": "[masked]", "bytes": 34}
    },
    "bytes": 72,
    "truncated": false,
    "warnings": ["sensitive_field:api_token"]
  },
  "scan_warnings": ["sensitive_field:api_token"]
}
```

summary 事件保留 `request_id`、`tool`、`args_digest` 和告警，不包含 `args` 或 `projection_version`。

参数投影的可见上限为 256 KiB，复用 `MaxFaceChatContentBytes`。超过上限时保留字段结构，把可展示字段降为 UTF-8 安全 preview，并设置 `truncated=true`；`bytes` 表示截断前长度。

## Progress 与可靠终态

`face.chat.tool.progress` 是显式订阅的瞬时消息，对应 transient type `chat.tool.progress`：

```json
{
  "conversation_id": "conversation-1",
  "request_id": "chat-1",
  "tool": "exec_command",
  "seq": 3,
  "kind": "stdout",
  "data": "building...\n",
  "gap": false
}
```

每块最多 4 KiB 并保持合法 UTF-8。队列拥塞可以丢块；客户端以 `seq` 和 `gap` 标记缺口。summary 连接不接收 `chat.tool.progress` 或 `run.progress`。

`chat.tool_completed.data.result` 是可靠终态：

```json
{
  "stdout": "ok\n",
  "stderr": "",
  "stdout_bytes": 3,
  "stderr_bytes": 0,
  "output_bytes": 3,
  "digest": "sha256:...",
  "truncated": false,
  "warnings": []
}
```

终态可见正文总上限为 1 MiB，按 stdout 后 stderr 的顺序保留 UTF-8 安全前缀。长度和 digest 始终针对截断前的完整受限工具结果，因此瞬时 progress 丢失后仍能判断终态完整性。summary 终态不包含 `result`，只保留状态、长度/digest 元数据和告警。

## 审批、run 与 task

透明 admission 产生审批时，`approval.requested` 和 pending snapshot 携带同一个冻结参数投影、`args_digest` 与投影版本。summary 订阅只看到 digest。`approval_audits` 和 `security_decisions` 继续只保存绑定摘要与结构化裁决字段，不保存原始参数或展示投影。

`use_hand` 与普通本地工具走同一 Chat tool 事件，所以远程工具参数、前台结果和启动后台 task 的返回值使用同一投影契约。远程 run/task 另有以下约束：

- foreground `run.progress` 只在 run admission 为 transparent 且订阅连接也是 transparent 时投递。
- `remote_tasks.detail_mode` 持久化 task admission 模式。
- `face.task.log` 在两个模式都返回 offset、字节数、digest、EOF 和 truncated；只有 task admission 与当前连接都为 transparent 时返回日志正文。
- run/task 状态摘要本身不含原始参数，始终可按原 scope 查询。

## 持久化与恢复

新调用在 `tool_display_projections` 中分两步保存：

1. admission 写入 conversation、request、ordinal、tool、detail mode、`args_digest` 和版本化参数投影。
2. terminal 只完成一次，写入 success、版本化结果投影、告警与完成时间。

`face.snapshot.tool_history` 返回这些记录。透明记录可以按当前连接降级成 summary；summary admission 永远没有可回填的原始视图。升级前的历史消息没有展示记录，保持旧摘要，不从 message/tool 文本推测或重建参数。

透明 admission 的 lifecycle/EventBus、REPL 控制台和文件日志可以写入同一份受展示策略约束的完整投影；summary admission 只写摘要元数据。审批审计和安全审计不走这条日志投影路径，仍只保存 digest 与结构化裁决字段。

## 客户端行为

内置 TUI 在 operator 身份下请求 transparent，显示工具卡片、结构化参数、进行中的输出和可靠终态；长内容由 viewport 折叠/滚动，不用瞬时块替代终态。observer TUI 使用 summary。

Headless Face 原样输出经过协议校验的 JSONL。自动化客户端必须依赖字段状态、投影版本、长度、digest 和 `truncated`，不得从 `message` 文本推断工具事实。

客户端记录透明事件前应明确选择日志位置和保留期。多租户或共享终端部署应使用 observer/summary 凭据，而不是依赖扫描器充当秘密过滤器。

## 验收基线

- revision 3 strict payload、详情模式枚举和 observer 拒绝透明。
- schema/中央规则、未知字段告警、mask/hide/preview 与嵌套路径。
- stdout/stderr、Unicode 边界、1 MiB 截断、原始长度和 digest。
- summary 连接无法通过 event、progress、snapshot 或 task log 取得透明内容。
- admission 模式在断线恢复、foreground run 和 durable task 中保持不变。
- approval/security 审计表不因透明 UI 变成原始参数仓库。
