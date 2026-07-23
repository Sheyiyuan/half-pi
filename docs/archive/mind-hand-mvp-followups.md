# Mind + Hand MVP 后续重点 TODO（已归档）

> 文档状态：已归档的历史设计债清单。审批闭环、显式取消、审计持久化和会话并发边界已由 [`remote-execution-implementation-plan.md`](remote-execution-implementation-plan.md) 的 Phase 0 至 Phase 4 完成；进度流、后台任务和 Windows 原生验收记录见 [`next-development-plan.md`](next-development-plan.md)。

## 背景

当前 Mind + Hand 已完成 MVP：Mind 能启动 Hub，Hand 能注册，LLM 能通过 `list_hands` / `get_hand_info` / `select_hand` / `use_hand` 完成远程工具调用，RPC 支持 `timeout_ms`，Hand 侧具备基础权限过滤、输出截断和监控事件上报。

当前协议足够支撑一版 demo，但还不是长期稳定协议。本文件记录 demo 后优先收敛的设计债。

远程执行闭环的集中方案见 [`remote-execution-closed-loop.md`](remote-execution-closed-loop.md)。

## 1. 审批语义与 Hand 最终守门

现状：
- Mind 负责用户确认、模式判断和本地工具安全检查。
- Hand 负责真实执行环境、工具 allow/deny、远端专属工具本地检查。
- `SkipChecks` 仍是偏实现细节的字段名，容易被误解为 Hand 完全信任 Mind。

TODO：
- 将 `SkipChecks` 语义重命名为更明确的审批信息，例如 `Approval` / `ApprovedByMind`。
- Hand 永远保留最终执行守门权：工具存在性、allow/deny、平台本地 `Check`、输出上限都必须在 Hand 侧生效。
- 定义审批来源和审计字段：审批模式、审批人、审批时间、审批原因、是否一次性批准。
- 为远端专属工具定义确认策略：Hand 无交互能力时如何接受 Mind 的用户批准，哪些决策仍必须拒绝。

验收标准：
- 协议字段名不再表达“跳过安全”，而是表达“Mind 已完成哪些审批”。
- Hand 根据自身策略决定是否接受 Mind 审批。

## 2. 完整取消协议

现状：
- `use_hand` 将 `timeout_ms` 发给 Hand。
- Hand 执行工具时使用该 timeout 派生 context。
- Unix `exec_command` 取消时杀整个进程组。
- Mind 超时后没有显式发送 cancel 消息；Hand 只能依赖 RPC 自带 timeout。

TODO：
- 增加 `rpc_cancel` 消息类型。
- `use_hand` 在本地 context 取消或等待超时时向 Hand 发送 cancel。
- Hand 为正在执行的 RPC 维护任务表：`rpcID -> cancel func / started_at / tool / status`。
- 定义取消结果：已取消、已完成、未知 RPC、取消失败。
- Windows 已使用 Job Object 实现进程树取消，并通过多架构交叉编译和原生 Windows 集成测试验收。

验收标准：
- Mind 主动取消后，Hand 能尽快停止对应 RPC。
- Hand 能返回明确的取消结果，而不是只依赖连接断开或执行超时。

## 3. 进度流与后台任务

现状：
- MVP 只支持一轮 RPC → 一次 `RPCResult`。
- 长任务没有进度输出，LLM/用户只能等最终结果。

TODO：
- 增加 `rpc_progress` 消息类型。
- 支持 stdout/stderr 增量上报，至少对 `exec_command` 可用。
- 支持后台任务模式：启动后返回 task ID，用户可查询状态、读取日志、取消任务。
- 定义结果保留策略：内存保留、SQLite 持久化或日志文件。

验收标准：
- 长命令能持续反馈输出。
- RPC 和后台任务生命周期有明确状态机。

## 4. Core 并发状态模型

现状：
- REPL 单入口时基本安全。
- Hub 回调和 pending call 已有局部锁。
- `history`、`Mode`、`activeHand`、`autoAllow` / `autoDeny` 尚未形成统一并发模型。

TODO：
- 选定一种模型：`Core` 全局 mutex，或按 session actor 化。
- 将 `Chat`、`SetMode`、`SetStore`、`SetActiveHand`、审批缓存读写纳入同一状态边界。
- 为 Face/TUI/IM Bot 并发入口设计调度规则。
- 增加 race 测试覆盖并发 Chat、切 session、select Hand、Hub event。

验收标准：
- `go test -race` 覆盖多入口场景。
- Face 接入后不会出现历史串扰、activeHand 串扰或审批缓存竞态。

## 5. 远程执行用户体验

现状：
- 远程执行主要通过 LLM 工具调用触发。
- REPL 只有 `/hand add/list/remove` 和 `/peers`，没有手动远程执行命令。

TODO：
- 增加手动调试命令，例如 `/hand select <id>`、`/hand exec <tool> <json>`。
- `get_hand_info` 返回工具 schema，而不只是工具名。
- 在 system prompt 中明确远程工具使用策略：先 `list_hands`，必要时 `select_hand`，再 `use_hand`。
- 对远程结果增加结构化来源信息：hand_id、tool、duration、truncated。

验收标准：
- 不依赖 LLM 也能手动验证 Mind + Hand 链路。
- LLM 更稳定地选择正确 Hand 和工具。

## 6. 安全审计

现状：
- EventBus 会输出连接、断开、工具结果和监控事件。
- 远程执行审批和 RPC 生命周期尚未形成完整审计表。

TODO：
- 持久化远程执行记录：session_id、hand_id、tool、args 摘要、审批结果、执行结果、耗时。
- 敏感参数脱敏。
- 记录 Hand 拒绝原因：deny_tools、unknown tool、Check 拒绝、timeout、cancel。

验收标准：
- 用户能追溯每一次远程执行是谁批准、在哪台 Hand 执行、结果如何。

## 建议顺序

1. 审批语义与 Hand 最终守门。
2. Core 并发状态模型。
3. 完整取消协议。
4. 手动远程执行调试命令。
5. 进度流与后台任务。
6. 安全审计持久化。
