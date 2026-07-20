# Face 全屏 TUI 设计

> 状态：T1-T4 已实现。Linux 构建、race、reducer、布局和输入测试已通过；Windows ConPTY 与 macOS PTY 原生发布矩阵仍需在对应平台验收。现有 Headless JSONL 客户端和 Face wire protocol 保持不变。

## 1. 背景

实现前的 `face.mode = "tui"` 实际运行的是行式 REPL。它证明了正式 Face 协议可以完成 conversation、Chat、审批、run 和 task 工作流，但不适合作为最终人类界面：

- 启动后停留在 `no conversation`，用户必须理解并手动输入 conversation ID。
- `accepted`、`chat.started`、`conversation.changed` 和 request ID 等协议细节直接进入主输出。
- 每个流式 chunk 被渲染成独立日志行，提示符在异步消息之间重复出现。
- Markdown 换行显示为字面量 `\n`，chunk 边界破坏自然阅读。
- 会话、聊天、工具活动、审批和任务共享一个不可回流的文本区域。
- 鼠标、响应式布局、多行编辑、输入历史和命令补全均不存在。

新 TUI 必须被视为独立产品层。协议消息只更新 UI 状态，不能直接打印到终端。

当前实现位于 `modules/half-pi-face/internal/tui/`。`app.go` 是唯一可见状态 reducer，`network.go` 只产生带 connection generation 的 Bubble Tea 消息，`requests.go` 复用并预校验正式 DTO，`view.go` 负责固定矩形与终态 Markdown 渲染。`--mode tui` 对非 TTY stdin/stdout 明确失败；`--mode headless` 保持单连接严格 JSONL。

## 2. 目标

1. 启动后直接进入可输入的本地“新对话”草稿。
2. 第一次发送消息时自动创建 conversation，用户不接触 UUID。
3. Chat delta 原地追加到同一条 assistant 消息，chunk 边界不可见。
4. 根据终端字符宽高提供稳定的三栏、双栏和单栏布局。
5. 键盘完成所有功能，并为常用操作提供鼠标点击和滚轮支持。
6. 输入框支持多行编辑、历史回溯、草稿恢复和上下文命令补全。
7. conversation 历史、活动 Chat、审批、foreground run 和 durable task 均可恢复。
8. 保留 Headless Face 的严格 JSONL 行为，不引入 TUI 专用服务端旁路。

## 3. 非目标

- 不在 TUI 内实现多用户 RBAC 或修改 Face scope。
- 不把 TUI 本地状态提升为 conversation、run 或 task 的权威来源。
- 不为界面新增明文协议或跳过 v2 加密握手。
- 不保证终端原生多点触控、手势或软键盘行为。
- 不在首个版本实现插件化主题、图片协议或富媒体消息。
- 不在首个版本实现 conversation 删除；当前正式协议没有对应 command。

## 4. 核心决策

| 主题 | 决策 |
|---|---|
| UI 框架 | Bubble Tea，使用单一 `Update` 状态循环处理键盘、鼠标、网络和 resize |
| 基础组件 | Bubbles 的 textarea、viewport、list 和 spinner |
| 样式 | Lip Gloss；颜色是增强信息，不作为唯一状态编码 |
| Markdown | 流式期间使用安全纯文本；终态后用 Glamour 按当前宽度渲染 |
| 启动状态 | 默认进入本地新对话草稿，不自动写入空 conversation |
| 首次发送 | 自动执行 create -> subscribe -> snapshot -> chat 状态机 |
| 流式刷新 | delta 立即进入内存，界面最多约 30 FPS 合并重绘 |
| 输入历史 | conversation 用户消息 + 当前进程输入，不新增本地明文历史文件 |
| 命令系统 | typed command registry，统一解析、补全、执行和帮助元数据 |
| 鼠标模式 | cell motion；支持点击、滚轮和按下拖动，不依赖 hover |
| 触控 | 接受终端模拟器映射出的点击/滚轮事件，不承诺原生手势 |
| 兼容模式 | `--mode headless` 不变；`--mode tui` 切换为真正全屏实现 |

### 4.1 为什么选择 Bubble Tea

TUI 同时接收连接状态、可靠响应、可丢 delta、窗口 resize、输入、鼠标和定时刷新。Bubble Tea 的消息/命令模型可以让所有可见状态只在一个 reducer 中变化，避免网络 goroutine 直接操作组件。

`tview` 更适合静态 widget 表单，但复杂流恢复、异步审批和响应式重排仍需额外状态层；在本项目中不会减少整体复杂度。直接基于 ANSI 或 `tcell` 实现则会重复处理焦点、textarea、宽字符和滚动行为。

## 5. 产品模型

### 5.1 新对话是本地草稿

启动后中央聊天区显示一个未持久化的 `DraftConversation`，输入框立即聚焦。此时可以异步加载 conversation 列表和 capabilities，但不能阻塞输入。

第一次发送消息时：

```text
local draft + user submits text
  -> optimistically append pending user message
  -> face.conversation.create(name="")
  -> accepted/result
  -> install returned conversation_id
  -> face.subscribe(conversation_id, transient streams)
  -> accepted
  -> face.conversation.snapshot
  -> face.snapshot
  -> face.chat(original text)
```

约束：

- create 期间禁用第二次发送，但用户可以继续编辑下一条草稿。
- create 或 subscribe 失败时保留原文本和本地新对话，不产生重复 Chat。
- conversation 创建成功、Chat 发送失败时保留已创建会话，并提供重试。
- 用户消息在收到 `face.accepted(chat)` 前显示为 `sending`，之后显示为普通消息。
- 服务端自动以第一条用户消息命名；客户端在列表刷新前可用截断后的首条消息作为临时标题。

### 5.2 启动行为

默认始终打开新对话草稿，而不是自动进入最后一个 conversation。历史会话在左栏和 conversation picker 中可见。

后续可增加非默认配置：

```toml
[face]
startup = "new" # new | last
```

该配置不属于首个版本的阻塞项。

## 6. 信息架构

界面包含五个逻辑区域：

1. Header：连接、当前 conversation、mode 和 active Hand 摘要。
2. Conversations：新对话入口、搜索和最近会话。
3. Chat viewport：用户、assistant、工具活动和错误。
4. Activity：审批、foreground run、durable task 和 Hand 状态。
5. Composer：多行输入、补全菜单、发送和当前请求状态。

协议 ACK、request ID、snapshot version 和内部事件默认不属于以上区域，只能在 Debug inspector 查看。

## 7. 响应式布局

布局使用终端 cell 尺寸，不按像素或 viewport 比例缩放字体。每次收到 `tea.WindowSizeMsg` 都重新计算稳定矩形，并将相同矩形用于渲染和鼠标 hit-testing。

### 7.1 Breakpoints

| 模式 | 条件 | 布局 |
|---|---|---|
| Wide | 宽 `>=132` 且高 `>=30` | conversation 28 列 + chat 弹性列 + activity 34 列 |
| Standard | 宽 `88-131` | conversation 26 列 + chat 弹性列；activity 为覆盖抽屉 |
| Compact | 宽 `<88` | 单栏 chat；conversation 和 activity 使用全屏覆盖层 |
| Short | 高 `<20` | 单行 header、压缩 composer、隐藏非关键 footer 信息 |

所有模式必须保留：

- chat viewport 最小宽度 40 列。
- composer 至少 3 行，最多占可用高度的三分之一。
- header、composer 和 footer 高度先确定，chat 使用剩余高度。
- 面板隐藏或恢复时保留选择、滚动位置和未发送草稿。
- 尺寸不足以安全渲染时显示单一“终端尺寸不足”视图，不允许重叠。

### 7.2 Wide

```text
┌ Conversations ┬──────────────── Chat ────────────────┬── Activity ──┐
│ New chat       │ User                                 │ Approvals     │
│ Recent         │                                     │ Runs          │
│ Project A      │ Assistant response                  │ Tasks         │
│ Project B      │ [tool activity]                     │ Hands         │
├────────────────┴─────────────────────────────────────┴───────────────┤
│ Composer                                                        Send │
├──────────────────────────────────────────────────────────────────────┤
│ Connected · normal · Hand: laptop · Generating                       │
└──────────────────────────────────────────────────────────────────────┘
```

### 7.3 Standard

```text
┌ Conversations ┬──────────────────── Chat ────────────────────────────┐
│ New chat       │                                                     │
│ Recent         │                                                     │
├────────────────┴─────────────────────────────────────────────────────┤
│ Composer                                                        Send │
└──────────────────────────────────────────────────────────────────────┘
```

Activity 从右侧覆盖 chat，但关闭后不得改变 chat 的滚动锚点。

### 7.4 Compact

```text
┌ Conversation title · Connected ──────────────────────────────────────┐
│ Chat viewport                                                        │
│                                                                      │
├──────────────────────────────────────────────────────────────────────┤
│ Composer                                                        Send │
└──────────────────────────────────────────────────────────────────────┘
```

conversation picker 和 activity 使用独占内容区的覆盖视图，`Esc` 返回 chat。

## 8. 焦点与导航

焦点枚举固定为：

```text
conversations
chat
activity
composer
overlay
modal
```

优先级：modal > overlay > completion menu > focused pane > global keymap。高优先级组件消费事件后，不再向下传播。

建议默认键位：

| 按键 | 行为 |
|---|---|
| `Enter` | composer 中发送；列表和菜单中确认 |
| `Alt+Enter` | composer 插入换行 |
| `Tab` / `Shift+Tab` | 在当前布局可见区域间切换焦点 |
| `Ctrl+N` | 打开新的本地对话草稿 |
| `Ctrl+P` | conversation picker |
| `Ctrl+K` | command palette |
| `Ctrl+R` | 搜索输入历史 |
| `PageUp` / `PageDown` | 滚动 chat viewport |
| `Esc` | 关闭 completion、overlay 或 modal |
| `Ctrl+C` | 有活动 Chat 时先请求取消；无活动操作时退出确认 |

传统终端不能稳定区分 `Shift+Enter` 与 `Enter`，因此不把 `Shift+Enter` 作为唯一换行方式。可同时兼容能够区分该按键的终端。

## 9. 鼠标与触控

### 9.1 鼠标能力

- 单击 conversation、activity tab、工具行和审批操作。
- 单击 composer 聚焦，并在组件支持时定位光标。
- 滚轮只滚动鼠标所在矩形；没有命中时默认滚动 chat。
- 单击 Send、Cancel、Retry 等明确命令。
- 按下拖动用于组件选择或 scrollbar，不把 hover 作为必要交互。
- 用户按住 `Shift` 时允许终端模拟器执行原生文本选择。

所有鼠标操作必须有等价键盘路径。布局引擎输出的矩形是鼠标路由的唯一依据，禁止在组件中复制坐标计算。

### 9.2 触控边界

终端应用只接收终端模拟器编码后的鼠标事件，无法可靠识别手指、多点触控、长按和滑动手势。验收范围是：

- 终端将触屏点击映射为 mouse press/release 时，可点击控件。
- 终端将触控板或触屏滚动映射为 wheel 时，可滚动目标面板。
- 不承诺 pinch、双指手势或移动端软键盘布局。

目标验收终端包括 Windows Terminal、WezTerm、Kitty、iTerm2 和 Termux。需要可靠原生触控时应使用未来的 Web Face。

## 10. Composer

Composer 是稳定尺寸的多行编辑器，支持 UTF-8、中文输入法提交、粘贴、光标移动、选择和水平/垂直滚动。

### 10.1 高度

- 初始 3 行。
- 随内容换行增长。
- Standard/Wide 最大 10 行。
- Compact 最大为可用高度三分之一。
- 达到最大高度后内部滚动，不推动 chat 布局。

### 10.2 输入历史

历史来源：

1. 当前 conversation 已持久化的 `role=user` 消息。
2. 当前进程已发送但尚未出现在 snapshot/messages 的用户输入。
3. `/` command 使用独立的进程内历史。

不新增 `~/.half-pi/face/history` 明文文件，避免在 Mind 权威历史之外复制敏感输入。

状态模型：

```text
draft        当前用户尚未发送的文本
entries      可回溯历史，按时间升序
cursor       -1 表示 draft，否则指向 entries
saved_draft  首次离开 draft 时保存，用于向下返回
```

按键规则：

- completion 打开时，`Up/Down` 只移动候选选择。
- 多行文本中存在上一/下一视觉行时，`Up/Down` 移动光标。
- 光标位于首行顶部时，`Up` 进入更早历史。
- 浏览历史时，`Down` 向更新记录移动；越过最新记录恢复 `saved_draft`。
- 编辑历史项会创建新草稿，绝不修改已持久化消息。
- 切换 conversation 时保存各自草稿，并切换到该 conversation 的历史集合。
- `Ctrl+R` 打开模糊历史搜索；选中后载入 composer，不立即发送。

### 10.3 粘贴

- 支持 bracketed paste。
- 粘贴内容不得触发命令自动执行。
- 超过 `MaxChatContentBytes` 时阻止发送并显示剩余/超限字节数。
- 控制字符按输入安全规则过滤，但保留换行和制表符的编辑语义。

## 11. Command Registry 与补全

命令不再由一个字符串 `switch` 同时承担解析、帮助和执行。使用统一注册表：

```go
type CommandSpec struct {
    Name        string
    Aliases     []string
    Description string
    Args        []ArgumentSpec
    Execute     CommandHandler
}

type ArgumentSpec struct {
    Name      string
    Required  bool
    Variadic  bool
    Complete  ArgumentCompleter
}
```

同一份 metadata 驱动：

- `/` completion menu。
- `Ctrl+K` command palette。
- 参数校验和错误定位。
- 动态参数补全。
- 可测试的 command dispatch。

### 11.1 补全交互

- composer 第一个非空字符为 `/` 时激活。
- 菜单显示命令名和简短描述，按前缀与模糊分数排序。
- `Tab` 接受当前候选但不自动执行。
- `Up/Down` 选择候选，`Esc` 关闭，继续输入实时过滤。
- 参数之间支持空格和引号；解析错误在 composer 附近显示，不进入 chat。
- 动态数据尚未加载时显示 loading 状态，不阻塞普通文本输入。

### 11.2 动态补全

| Command | 补全来源 |
|---|---|
| `/open` | conversation name + ID |
| `/hand` | 当前在线 Hand |
| `/run`, `/run-cancel` | 当前 conversation 的 run |
| `/task`, `/task-log`, `/task-cancel` | 当前 conversation 的 task |
| `/approve` | pending approval + decision enum |
| `/messages` | `before_seq` 和 limit 参数提示 |
| `/rename` | 当前 conversation name 作为可编辑默认值 |

首个版本保留现有命令语义，同时新增 UI 原生入口。用户不需要记忆命令才能完成核心工作流。

## 12. Chat 渲染

### 12.1 消息模型

```text
ChatItem
├── UserMessage
├── AssistantResponse(response_index)
├── ToolActivity
├── ApprovalActivity
└── ErrorNotice
```

协议消息由 reducer 投影到以上模型：

- `face.accepted` 更新 pending command，不生成聊天行。
- `chat.started` 创建生成状态，不显示 request ID。
- `face.chat.delta` 追加到对应 `AssistantResponse`。
- tool called/completed 更新同一个 `ToolActivity`。
- `conversation.changed` 只触发状态失效或列表刷新。
- `face.chat.stream.end` 结束 spinner，并检查 `last_seq` 缺口。
- `face.result` 校正发起端最终正文和错误状态。

### 12.2 流式正文

- delta 按 `response_index` 进入稳定字符串缓冲区。
- `seq` 或 UTF-8 byte `offset` 不连续时进入恢复，暂存后续 delta。
- 恢复过程默认静默，只在失败且无法从 messages 校正时显示错误。
- chunk 到达不创建新组件，不打印 `assistant[n]` 标签。
- 真实 `\n` 作为段落换行；ANSI、C0/C1 控制字符被过滤。
- 流式期间只做安全文本换行，避免每个 delta 触发 Markdown 全量重排。
- stream end/result 后进行完整 Markdown 渲染并维持 viewport 锚点。

### 12.3 工具循环

每个 `response_index` 是独立 assistant 内容段。工具调用插入在产生它的 response 与下一 response 之间：

```text
Assistant: 我先检查在线设备。
Tool: list_hands  succeeded
Tool: use_hand    running
Assistant: 已完成检查，结果如下……
```

工具默认显示一行摘要；展开后显示安全的 run 状态和 stdout/stderr。原始参数仍不从普通 Face 协议获取或显示。

### 12.4 自动滚动

- 用户位于底部时，delta 到达后跟随最新内容。
- 用户向上滚动后锁定当前锚点，显示非侵入式“有新内容”标记。
- 用户返回底部或激活标记后恢复自动跟随。
- Markdown 终态重排不得把正在阅读的旧内容强制拉到底部。

## 13. Conversation

### 13.1 左栏

- 顶部固定 New chat。
- 最近更新时间倒序展示 conversation。
- 名称为空或等于 ID 时显示 `Untitled`，ID 只在详情中展示。
- 当前 conversation 有活动 Chat、pending approval 或失败状态时显示非颜色唯一标记。
- 支持键盘和鼠标选择、滚轮、搜索。

### 13.2 切换

切换 conversation 时：

1. 保存当前 composer draft、scroll anchor 和 selection。
2. 安装目标 subscription。
3. 获取 snapshot。
4. 对 pending Chat 执行 stream recovery。
5. 按需通过 `face.conversation.messages` 向前分页。

切换不取消原 conversation 的 Chat。多个 conversation 的 transient state 可以保留有界摘要，但只渲染当前 conversation。

### 13.3 历史分页

- 首次打开使用 snapshot 中的消息。
- chat viewport 到达顶部阈值时请求更早一页。
- 合并依据稳定 `seq`，禁止按文本去重。
- 加载更早消息后保持原视觉锚点。
- 同一 conversation 同时只允许一个历史分页请求。

## 14. Activity

Activity 包含四个 tab：Approvals、Runs、Tasks、Hands。

### 14.1 Approval

pending approval 以 modal 提升到最前层，显示：

- tool。
- reason。
- args digest。
- expires at。
- conversation 和关联 run 摘要。

操作包括 allow once、deny once、allow session、deny session。默认焦点放在 deny once；提交后进入 resolving，禁止重复裁决。

### 14.2 Foreground run

- `face.run.progress` 追加到对应 run 的有界 stdout/stderr 缓冲区。
- stdout/stderr 使用不同标签，但颜色不是唯一差异。
- 检测 `seq`/`gap` 后显示“输出可能不完整”。
- run 终态由 `remote_run.changed` 或 `run.get` 决定，不能由输出推断。

### 14.3 Durable task

durable task 不消费 foreground progress。详情通过 `face.task.log` 的 offset/limit 拉取，并显示 stale、truncated 和 terminal 状态。

## 15. 连接与恢复

当前 `tui.Run` 只接收一条已建立的连接。真正 TUI 要实现自动重连，连接所有权应调整为 `Connector` 或由应用顶层持有：

```go
type Connector interface {
    Connect(context.Context) (client.Connection, error)
}
```

状态：

```text
connecting -> authenticating -> synchronizing -> ready
     ^                                      |
     +-------- backoff <- disconnected -----+
```

要求：

- 断线时保留本地草稿、布局、conversation 选择和已渲染历史。
- 禁止在不确定发送结果时自动重发非幂等 command；Chat 依赖相同 request ID replay。
- 重连后重新查询 capabilities、安装 subscription、获取 snapshot 并恢复 pending streams。
- 状态栏显示连接状态；重连不在 chat 中制造重复错误行。
- 用户可以显式立即重试或退出。

## 16. 安全与隐私

- token、application key、原始工具参数和 provider 原始事件永不进入 UI 状态。
- 普通界面隐藏 request ID；Debug inspector 只显示无秘密协议摘要。
- Chat 正文和 run 输出不写入额外 TUI 日志。
- 复制操作由用户显式触发，不自动写系统剪贴板。
- 输入历史复用 Mind 权威消息，不新增本地明文副本。
- 所有远端字符串在渲染前移除 ANSI、C0/C1 控制序列，但 chat 正文保留安全换行和制表语义。
- approval 仍受 scope、归属、digest 和 expiry 校验；UI 不做越权兜底。

## 17. 包结构

建议将现有 `internal/tui` 拆分为以下核心概念，每个文件保持单一职责：

```text
internal/tui/
├── app.go                 Bubble Tea model/update/view
├── connector.go           连接与重连状态机
├── reducer.go             Face protocol -> UI state
├── layout.go              breakpoints 与矩形
├── keymap.go              全局/局部键位
├── mouse.go               hit-testing 与滚轮路由
├── composer.go            textarea、草稿和发送状态
├── history.go             输入历史状态机
├── completion.go          补全菜单
├── commands.go            command registry
├── conversations.go       列表、搜索和切换
├── chat.go                消息模型与 viewport
├── streaming.go           delta、恢复和终态校正
├── activity.go            approval/run/task/hand
├── markdown.go            终态渲染缓存
├── sanitize.go            终端文本安全
└── theme.go               语义样式与无色降级
```

网络 reader goroutine 只产生 `tea.Msg`，不能直接修改 model。发送操作通过 `tea.Cmd` 返回结果，所有 pending command 关联仍在 reducer 中维护。

## 18. 性能预算

- UI 重绘上限约 30 FPS；无状态变化时不定时重绘。
- delta 到达后只更新目标 response buffer，Markdown 仅在终态或 resize 后重建。
- chat 历史按页加载，render cache 只覆盖可见窗口和有限前后缓冲。
- 每个非当前 conversation 只保留 pending/terminal 摘要，不保留无限 transient output。
- mouse motion 不启用全量 hover 事件，降低高频 Update 压力。
- resize 只重算布局和受宽度影响的 render cache。

## 19. 实施阶段

### T1：可用聊天工作台

- 引入 Bubble Tea/Bubbles/Lip Gloss，启用 alternate screen。
- 实现 responsive layout、chat viewport 和 composer。
- 默认新对话草稿，首次发送自动创建并 Chat。
- delta 原地追加、真实换行、stream end/result 校正。
- 隐藏协议 ACK 和生命周期噪声。
- conversation picker 和现有会话打开。

完成定义：用户启动二进制后无需输入命令或 ID 即可发送第一条消息，流式回答表现为一条连续消息。

### T2：输入与导航

- 多行编辑、bracketed paste、conversation 草稿。
- 输入历史回溯和 `Ctrl+R` 搜索。
- command registry、palette 和动态补全。
- 鼠标点击、目标滚轮和响应式 overlay。
- 历史消息向前分页。

完成定义：核心工作流同时可由纯键盘和鼠标完成，resize 不丢状态。

### T3：操作工作台

- approval modal。
- tool activity、foreground run progress。
- durable task log、Hand 详情。
- 自动滚动锁定和新内容提示。
- Markdown 终态渲染和无色模式。

完成定义：敏感远程执行、取消和后台任务无需回到 Mind REPL。

### T4：恢复与跨平台收口

- connector 和自动重连。
- pending command replay、snapshot 和 stream recovery。
- Windows/Linux/macOS 终端矩阵。
- 触控映射、IME、Unicode width 和性能验收。
- 删除旧行式 renderer，保留 Headless JSONL。

完成定义：断线和 resize 不破坏用户草稿或权威状态，正式发布不再把行式 REPL 标记为 TUI。

## 20. 测试策略

### 20.1 纯状态测试

- create -> subscribe -> snapshot -> chat 自动创建状态机。
- command accepted/result/error 关联。
- delta contiguous/gap/recovery/end/final reconcile。
- conversation 切换期间跨会话事件隔离。
- history cursor、saved draft 和 conversation 独立草稿。
- completion filtering、参数解析和动态数据更新。

### 20.2 布局测试

固定验收尺寸：

```text
160x45
120x30
80x24
50x16
```

每个尺寸检查：

- 所有矩形在 viewport 内且互不重叠。
- chat 和 composer 满足最小尺寸。
- 最长中文标题和状态不会覆盖相邻区域。
- overlay/modal 完全覆盖目标区域且 `Esc` 可退出。
- resize 前后的焦点、草稿和 scroll anchor 保持。

### 20.3 输入与鼠标测试

- 中文、宽字符、emoji、组合字符和超长单词。
- Enter/Alt+Enter、粘贴和最大输入限制。
- Up/Down 在 completion、textarea 和 history 间的优先级。
- mouse hit-testing、滚轮目标、点击 Send/Cancel/Approval。
- Shift 原生选择不触发 UI 操作。

### 20.4 PTY 与真实进程 E2E

- 使用真实 Mind/Hand/Face 二进制和 v2 加密连接。
- 发送窗口 resize 和鼠标 escape sequences。
- 自动创建首个 conversation 并收到连续流式回答。
- 双 Face、发起端断线、重连恢复和 request replay。
- foreground progress、approval、cancel 和 durable task。
- stdout 不再包含协议级逐行日志或重复 prompt。
- 全部 Go 测试使用 `-race -count=1`。

## 21. 验收矩阵

- 启动后 composer 自动聚焦，用户可直接输入并发送。
- 首次发送只创建一个 conversation，只发起一个 Chat。
- 不显示 `accepted`、request ID、snapshot version 或重复 prompt。
- 任意数量 delta 只形成一个连续 response，真实换行正确。
- 工具循环按 response/tool/response 顺序显示，不重复最终正文。
- `160x45` 到 `50x16` resize 无重叠、崩溃或状态丢失。
- conversation、activity、composer 均可用键盘和鼠标操作。
- 支持历史回溯、草稿恢复、历史搜索和 conversation 隔离。
- `/` 命令和动态参数具备可选择补全。
- chat 上滚后不会被新 delta 强制拉到底部。
- stream 缺口、断线和重连后可恢复到权威最终消息。
- 慢渲染不阻塞网络读取、Chat、run 或其他 Face。
- 无 `face:approve`/`face:runs:output` scope 时不出现越权入口或数据。
- Headless JSONL 输出和现有正式协议保持兼容。

## 22. 实现前冻结项

以下默认决策视为已确定，除非实现评审明确修改：

1. 启动默认新对话草稿，首次发送才持久化。
2. Bubble Tea 技术栈，不延续行式打印架构。
3. Wide/Standard/Compact/Short 四级响应式布局。
4. 键盘功能完整，鼠标为等价增强，触控以终端映射能力为边界。
5. 历史来自 Mind conversation，不新增本地明文输入历史。
6. typed command registry 同时驱动解析、补全和 palette。
7. 协议生命周期不进入 chat，Debug inspector 才显示关联信息。
8. T1 先交付真正可用的自动创建和连续流式聊天，再扩展操作面板。
