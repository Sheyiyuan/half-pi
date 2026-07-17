# Mind → Hand 远程执行闭环落地计划

## 文档状态

实施中。Phase 0 至 Phase 4（T0-T9）已于 2026-07-17 完成并通过全仓 race 测试及总 review；Phase 5 为可选增强。T10 已使用 Windows Job Object 完成实现并通过 Windows 386/amd64/arm/arm64 交叉编译，仍需在原生 Windows 环境运行进程树取消测试后验收。

Phase 4 review 已确认：每个已加载 session 保留独立 actor；远端专属或跨平台工具必须经 Mind 一次性确认，并继续由 Hand 执行本地最终守门；审计写入失败时 run 在内存中 fail-closed，取消请求仍会发往 Hand，避免无主执行。

本文承接以下文档：

- `docs/archived/remote-execution.md`：已归档的 MVP 设计。
- `docs/remote-execution-closed-loop.md`：闭环架构设计。
- `docs/archived/mind-hand-mvp-followups.md`：已归档的 MVP 后续设计债清单。
- `docs/next-development-plan.md`：当前开发顺序和剩余验收项。

本文只描述工程落地范围、子任务、阶段依赖和验收标准。协议及架构语义发生变化时，应先更新闭环设计，再同步本计划。

## 1. 落地目标

将当前同步单结果链路：

```text
use_hand → RPC → Hand 执行 → RPCResult → use_hand 返回
```

升级为以下可验证闭环：

```text
创建 run
  → Mind 审批
  → Hand 接收并完成本地守门
  → 执行或拒绝
  → 可显式取消
  → 唯一终态
  → 持久化审计
```

完成后，每次远程执行必须满足：

- 有唯一 `run_id`，贯穿审批、传输、执行、取消和审计。
- Mind 能区分发送、接收、执行、拒绝、取消、超时、断线和最终结果。
- Hand 永远保留工具存在性、allow/deny、平台检查和输出限制等本机边界。
- Mind 超时或用户取消时会向 Hand 发送显式取消请求。
- 状态竞争只能产生一个终态。
- REPL、服务模式和后续 Face 共用同一远程执行权威。
- 每次执行均可追溯，敏感参数不以原文进入审计表。

## 2. 首版范围与固定决策

### 2.1 纳入范围

- 远程执行协议契约。
- 结构化审批信息及参数摘要。
- Hand 本地守门、任务登记和显式取消。
- Mind `RemoteRunRegistry` 和状态机。
- 服务级 Hub 消息路由。
- 断线收敛、审计持久化和启动恢复。
- 并发与 race 测试。
- 手动远程执行调试入口和结构化结果。

### 2.2 暂不纳入

- Hand 断线重连后的任务恢复。
- 自动重试或跨连接重放 RPC。
- 后台任务持久恢复。
- 所有工具的增量 stdout/stderr。
- 完整 OS 沙箱、容器隔离或资源配额系统。
- 分布式强一致状态复制。

### 2.3 固定语义

- **投递语义：** 首版采用 at-most-once。Mind 不自动重发可能产生副作用的 RPC。
- **断线语义：** Hand 断开后，该 Hand 的所有非终态 run 收敛为 `lost`。
- **重复请求：** Hand 收到重复 `run_id` 时不得再次执行，返回结构化 `duplicate_run`。
- **超时语义：** 协议优先传绝对截止时间；若保留 `timeout_ms`，必须明确它只作为兼容字段且不能延长 Mind 截止时间。
- **终态裁决：** registry 接受第一个合法终态，后到的结果或取消响应仅记录为迟到事件，不覆盖终态。
- **取消语义：** 发送取消不等于已经取消；只有任务确实停止或 Hand 明确确认后才能进入 `cancelled`。
- **审批语义：** Approval 只证明 Mind 完成了指定审批，不授予绕过 Hand 本地安全边界的能力。
- **审计语义：** Mind 是 run 生命周期和审计记录的权威；EventBus 只负责实时通知。

## 3. 总体阶段

| 阶段 | 名称 | 主要子任务 | 阶段出口 |
|---|---|---|---|
| Phase 0 | 契约冻结 | T0、T1 | 协议、摘要和状态机通过测试与评审 |
| Phase 1 | 安全接收闭环 | T2、T3 | Hand 可验证审批并明确 accepted/rejected，拒绝调用绝不执行 |
| Phase 2 | 执行取消闭环 | T4、T5 | run 可追踪、可取消、可收敛到唯一终态 |
| Phase 3 | 服务化与审计 | T6、T7 | 服务模式拥有统一路由，执行记录可查询、可恢复 |
| Phase 4 | 多入口可用性 | T8、T9 | 会话隔离通过 race，支持不依赖 LLM 的手动验证 |
| Phase 5 | 增强能力 | T10、T11 | Windows 取消闭环；可选进度流通过独立验收 |

Phase 0 至 Phase 3 是远程执行闭环的发布必需范围。Phase 4 是 Face 接入前的必需范围。Phase 5 可独立发布，不阻塞 Linux/Unix 首版闭环，但必须在能力声明中标明平台限制。

## 4. 子任务清单

### T0：协议契约

**目标**

在 `gateway-core/protocol` 中建立稳定、可版本化的远程执行协议。

**范围**

- 定义 `run_id` 及 RPC 字段。
- 定义 `rpc_accepted`、`rpc_rejected`、`rpc_cancel`、`rpc_cancel_result`。
- 定义结构化拒绝码和取消结果码。
- 为 `RPCResult` 增加 `run_id`、`truncated` 等必要字段。
- 定义 malformed payload、未知消息和迟到消息的处理规则。

**依赖**

无。

**交付物**

- 协议类型和常量。
- 协议类型与字段定义。
- JSON 编解码与兼容测试。
- 更新后的闭环协议说明。

**验收标准**

- 所有闭环消息可无损 JSON 编解码。
- `run_id` 在一次生命周期内不可为空且保持不变。
- 拒绝原因不再依赖自由文本判断。
- malformed payload 不会更新 run 状态，也不会导致 Hub 消息循环退出。
- 协议包测试在 `-race -count=1` 下通过。

### T1：审批摘要与状态机契约

**目标**

冻结 Approval 作用域、参数摘要算法和 run 合法状态迁移。

**范围**

- 定义 `Approval` 结构。
- 摘要输入至少绑定 `run_id`、`hand_id`、tool 和 args。
- 规范化 JSON 后使用 SHA-256 生成摘要。
- 明确数字、空值、数组、对象键顺序和 Unicode 的规范化规则。
- 定义 Approval 有效期与过期处理。
- 建立集中式状态转换表和终态集合。

**依赖**

T0。

**交付物**

- 共享摘要函数及测试向量。
- 状态定义和合法转换函数。
- Approval 校验规则。

**验收标准**

- 相同语义、不同 map 键顺序的参数产生相同摘要。
- tool、Hand、run 或任一参数变化都会导致摘要变化。
- 过期、缺失或摘要不匹配的 Approval 可被明确识别。
- 终态不能迁移到另一个终态。
- `cancel_requested` 不得覆盖已经到达的 `succeeded` 或 `failed`。
- 表驱动测试覆盖全部合法与非法状态迁移。

### T2：移除 `SkipChecks` 并恢复 Hand 最终守门

**目标**

删除“跳过安全检查”的协议语义，让 Hand 始终执行本机边界检查。

**范围**

- 用 Approval 替换 `RPC.SkipChecks`。
- 删除或停止使用 Hand `trustedRunner` 的全量跳过路径。
- Hand 无条件检查工具存在性和 allow/deny。
- 明确哪些本地 `Tool.Check` 必须执行。
- 将用户确认与本机环境检查拆分，避免重复交互但不跳过机器安全边界。
- 输出限制始终在 Hand 生效。

**依赖**

T0、T1。

**交付物**

- Mind Approval 生成逻辑。
- Hand Approval 校验和本地策略逻辑。
- 删除 `SkipChecks` 后的执行路径。

**验收标准**

- 未知工具返回 `unknown_tool`，且工具未执行。
- `deny_tools` 命中返回 `deny_tools`，且工具未执行。
- allow list 未命中返回 `allow_tools_miss`，且工具未执行。
- 缺失、过期或摘要错误的 Approval 均拒绝需要审批的工具。
- Mind 已审批时，Hand 的平台检查和输出上限仍然生效。
- 不再存在由远程协议触发的 `Runner.SkipChecks=true` 全量绕过路径。

### T3：Hand 接收、拒绝与重复 run 防护

**目标**

让 Mind 能准确知道 Hand 是否接受执行，并阻止重复副作用。

**范围**

- RPC 到达后先完成本地守门。
- 失败立即返回 `rpc_rejected`。
- 成功登记任务后返回 `rpc_accepted`。
- 定义 accepted 与 running 的关系；首版可规定 accepted 即开始执行。
- 增加已见 `run_id` 防重集合或任务/结果索引。
- Hand 回复发送失败必须被记录，不能静默吞错。

**依赖**

T2。

**交付物**

- Hand 接收前检查流水线。
- accepted/rejected 响应。
- duplicate run 防护。

**验收标准**

- `rpc_accepted` 只能在任务成功登记后发送。
- rejected run 不进入任务表、不调用 Tool、也不发送成功结果。
- 相同 `run_id` 到达两次时工具最多执行一次。
- accepted 必须先于该 run 的最终 result 发送。
- 并发提交相同 `run_id` 时仍然最多执行一次。
- Hand 集成测试和 race 测试通过。

### T4：Hand 任务表与显式取消

**目标**

Hand 能定位并停止指定 run，返回确定的取消结果。

**范围**

- 增加 `run_id -> Task` 的并发安全任务表。
- Task 保存 tool、开始时间、cancel func、done 和状态。
- 处理 `rpc_cancel`。
- 定义 `cancelled`、`already_done`、`unknown_run`、`failed`。
- 对 cancel 与自然完成的竞争进行一次性裁决。
- 设置取消等待上限，避免消息处理无限阻塞。

**依赖**

T3。

**交付物**

- Hand task registry。
- 取消处理器及取消响应。
- Unix 进程组取消回归测试。

**验收标准**

- 运行中的 `exec_command` 收到 cancel 后，其 Unix 进程组退出。
- 未知 run 返回 `unknown_run`。
- 已完成 run 返回 `already_done`，且不会改变既有结果。
- cancel 与 result 并发时只产生一个执行终态。
- 取消一个 run 不影响其他并发 run。
- `go test -race -count=1` 覆盖并发任务登记、完成和取消。

### T5：Mind `RemoteRunRegistry`

**目标**

由 Mind 统一管理每次远程执行的内存生命周期。

**范围**

- 创建 `RemoteRun` 和 registry。
- run 在 RPC 发送前登记。
- 按 `run_id` 路由 accepted、rejected、result 和 cancel result。
- 校验消息来源 Hand。
- 原子执行状态转换并唤醒等待者。
- `use_hand` 等待 run 终态，而不是直接等待裸 `RPCResult`。
- 本地 context 取消或截止时间到达时发送 `rpc_cancel`。
- Hand 断开后将相关非终态 run 标记为 `lost`。

**依赖**

T1、T3、T4。

**交付物**

- `RemoteRunRegistry`。
- `use_hand` 闭环执行路径。
- Hub 生命周期消息适配接口。

**验收标准**

- 正常执行按 `created → approved → sent → accepted → succeeded/failed` 收敛。
- rejected run 直接进入 `rejected`，不进入 running。
- 错误 Hand 发来的消息不能更新 run。
- context 取消或截止时间到达时 Mind 必须发送 `rpc_cancel`。
- Hand 断连使其全部非终态 run 进入 `lost`。
- result、cancel result、timeout 和 disconnect 竞争时只接受一个合法终态。
- 并发多个 `use_hand` 不发生结果串投，race 测试通过。

### T6：远程执行服务化

**目标**

将远程执行权威从 REPL 初始化中移出，使服务模式、REPL 和后续 Face 共享同一实现。

**范围**

- 把 Hub 消息路由和 disconnect 处理放入 Mind 服务级生命周期。
- 消除包级全局 `remoteBridge`，改为显式依赖注入或服务实例。
- REPL 只负责交互，不拥有 pending run 或 Hub 回调。
- 明确服务启动和关闭时 registry 的生命周期。

**依赖**

T5。

**交付物**

- 服务级 remote execution component。
- REPL 接入适配。
- 面向 Face 的稳定接口。

**验收标准**

- 非 REPL 服务模式可以路由完整消息生命周期。
- REPL 模式继续完成现有 `use_hand` 行为。
- 同一进程内只有一个远程执行权威和一套 Hub 生命周期回调。
- 服务关闭时非终态 run 有确定收敛或审计行为。
- 服务模式与 REPL 模式集成测试均通过。

### T7：审计持久化与启动恢复

**目标**

为每次远程执行建立可追溯、可查询的权威记录。

**范围**

- 增加 `remote_runs` 和 `remote_run_events` 迁移。
- 保存 run 元数据、审批摘要、状态、拒绝码和时间点。
- 保存状态迁移事件。
- 原始敏感 args 不入库。
- Mind 启动时处理遗留非终态 run。
- 提供按 run、session、Hand 查询的最小 store API。

**依赖**

T5、T6。

**交付物**

- SQLite schema 和迁移。
- store CRUD/查询 API。
- registry 持久化适配。
- 启动恢复逻辑。

**验收标准**

- run 创建、审批、发送、接收、取消和终态均有记录。
- 数据库不保存原始 args，仅保存版本化摘要或显式脱敏摘要。
- Hand 拒绝原因以结构化 code 保存。
- Mind 重启后，遗留非终态 run 转换为 `lost` 并追加恢复事件。
- 重复执行迁移不会破坏已有数据。
- store 单测和 migration 测试在 `-race` 下通过。

### T8：会话并发边界

**目标**

确保 Face/TUI/IM 多入口不会造成会话历史、activeHand 或审批状态串扰。

**范围**

- 选定 SessionActor 或等效的 session 级串行化模型。
- 将 Chat、history、Mode、activeHand 和审批缓存纳入 session 边界。
- `RemoteRunRegistry` 保持独立，只按 run 管理执行生命周期。
- 通过 `session_id` 将 run 事件投递到正确会话。

**依赖**

T5、T6。

**交付物**

- 会话状态并发模型。
- 多入口调度实现。
- session/run 关联测试。

**验收标准**

- 并发 Chat 不发生 history 串扰。
- 并发 `select_hand` 不影响其他 session。
- 审批缓存只对所属 session 生效。
- Hub result 与 session 切换并发时，run 仍归属原 session。
- `go test -race -count=1` 覆盖以上场景且无数据竞争。

### T9：手动验证与结构化结果

**目标**

不依赖 LLM 即可验证远程执行闭环，并向上层提供稳定结果结构。

**范围**

- 增加 `/hand select <id>`。
- 增加 `/hand exec <tool> <json>`。
- `get_hand_info` 返回可用工具 schema，而不只返回名称。
- `use_hand` 返回 run、Hand、tool、状态、耗时和截断信息。
- system prompt 中明确 Hand 发现、选择和调用策略。

**依赖**

T6、T8；工具 schema 可独立提前实施。

**交付物**

- REPL 调试命令。
- 结构化远程执行结果。
- 更新后的工具说明和 system prompt。

**验收标准**

- 操作者无需 LLM 即可完成 list、select、exec、cancel 和结果查询。
- 输出明确展示 `run_id`、`hand_id`、tool、status、duration 和 truncated。
- 非法 JSON、离线 Hand 和未知工具均给出可行动错误。
- 手动命令与 LLM `use_hand` 走同一 registry 和审批链路，不存在旁路。

### T10：Windows 进程树取消

**目标**

使 Windows 的命令取消语义与 Unix 对齐。

**范围**

- 使用 Windows 专用实现终止命令进程树。
- 保持 build tag 与 `_windows.go` 文件约定。
- 更新能力说明和平台测试。

**依赖**

T4。

**交付物**

- Windows Job Object 进程树取消实现（挂起启动、加入 job、恢复执行、取消时终止整个 job）。
- Windows 集成测试（父进程、子孙进程、无关进程、预取消和正常后台进程语义）。

**当前状态**

- 代码、vet 和 Windows 386/amd64/arm/arm64 测试二进制交叉编译已通过。
- 原生 Windows 集成测试尚未执行，因此 T10 尚未完成最终验收，也不宣称跨平台完整取消。

**验收标准**

- cancel 后父进程及其子进程均退出。
- 不影响其他无关进程。
- Windows 测试通过后才能宣称跨平台完整取消。
- 未完成前，用户可见能力说明必须明确 Windows 限制。

### T11：可选进度流

**目标**

在闭环稳定后为长任务提供有序、可去重的增量反馈。

**范围**

- 增加 `rpc_progress` 和单 run `seq`。
- 首期仅支持 `exec_command`。
- Mind 去重、排序并转发进度事件。
- 定义累计输出上限和慢消费者策略。
- 进度事件不改变终态裁决。

**依赖**

T5、T6、T7。

**交付物**

- 进度消息协议和 Hand 流式适配。
- registry 进度事件处理。
- EventBus/Face 转发接口。

**验收标准**

- 长命令执行期间能收到多条有序进度。
- 重复 seq 被去重，缺失 seq 可观测但不阻塞最终结果。
- 慢消费者不会阻塞 Hand 的最终结果发送。
- 进度总量受限，不能绕过最终输出大小限制。
- result、cancel 和 progress 并发时仍只产生一个终态。

## 5. 分阶段实施与出口标准

### Phase 0：契约冻结

**包含任务**

- T0：协议契约。
- T1：审批摘要与状态机契约。

**阶段目标**

先消除协议歧义，避免 Mind、Hand 和存储并行实现出不同语义。

**阶段验收**

- 消息、错误码、取消结果和终态集合已冻结。
- Approval 摘要有共享实现和固定测试向量。
- 状态转换表覆盖所有合法与非法迁移。
- `gateway-core` 全量测试通过：

```bash
cd modules/gateway-core && go test -race -count=1 ./...
```

**退出门禁**

协议字段和状态语义仍有未决项时，不进入 Phase 1。

### Phase 1：安全接收闭环

**包含任务**

- T2：移除 `SkipChecks` 并恢复 Hand 最终守门。
- T3：Hand 接收、拒绝与重复 run 防护。

**阶段目标**

保证 Hand 在真实执行前给出明确决策，且 Mind 审批不会绕过本机边界。

**阶段验收**

- `SkipChecks` 不再出现在远程执行路径。
- 未知工具、权限拒绝、审批缺失、摘要不匹配和本地检查失败均结构化拒绝。
- 所有拒绝场景都能证明工具未执行。
- accepted 总在任务登记后、最终结果前发送。
- 并发重复 `run_id` 最多执行一次。
- `half-pi-hand` 全量测试通过：

```bash
cd modules/half-pi-hand && go test -race -count=1 ./...
```

**退出门禁**

只要存在 Mind 可触发的 Hand 安检绕过，或 rejected 后仍可能执行工具，不进入 Phase 2。

### Phase 2：执行取消闭环

**包含任务**

- T4：Hand 任务表与显式取消。
- T5：Mind `RemoteRunRegistry`。

**阶段目标**

完成单次 run 从创建到唯一终态的内存闭环。

**阶段验收**

- 正常、失败、拒绝、取消、超时和断线均有确定终态。
- Mind timeout/context cancel 必须发送 `rpc_cancel`。
- Unix 命令取消后进程组退出。
- wrong-Hand 消息不能更新 run。
- cancel、result、timeout 和 disconnect 竞争只产生一个终态。
- 至少覆盖 20 个并发 run 的集成测试，无串投和 race。
- Mind、Hand、gateway-core 相关测试全部通过。

**退出门禁**

存在无主执行、终态可覆盖、错误来源可投递或取消影响其他 run 时，不进入 Phase 3。

### Phase 3：服务化与审计

**包含任务**

- T6：远程执行服务化。
- T7：审计持久化与启动恢复。

**阶段目标**

使 Mind 服务成为远程执行权威，并让每次执行可追溯。

**阶段验收**

- 服务模式和 REPL 使用同一 remote execution component。
- 不再依赖包级全局 bridge 保存远程执行状态。
- 每次 run 的关键状态变更均进入审计表。
- 原始敏感参数不入库。
- 重启后遗留非终态 run 进入 `lost` 并有恢复事件。
- 数据库迁移可重复执行且兼容已有数据。
- 全仓测试通过：

```bash
make test
```

**退出门禁**

服务模式不能独立路由 run，或任一次远程执行无法从数据库追溯时，不发布远程执行闭环。

### Phase 4：多入口可用性

**包含任务**

- T8：会话并发边界。
- T9：手动验证与结构化结果。

**阶段目标**

为 Face/TUI/IM 接入提供隔离的会话模型和稳定调试入口。

**阶段验收**

- 多 session 并发 Chat、select Hand 和审批无串扰。
- result 与 session 切换竞争时仍归属正确会话。
- 不依赖 LLM 可完成 Hand 发现、选择、执行、取消和结果查询。
- 手动命令与 LLM 调用不存在安全旁路。
- 全仓 `make test` 及关键多入口 race 测试通过。

**退出门禁**

存在 session 状态串扰或手动入口绕过审批时，不接入 Face 多入口。

### Phase 5：增强能力

**包含任务**

- T10：Windows 进程树取消。
- T11：可选进度流。

**阶段目标**

补齐跨平台取消，并在不破坏终态模型的前提下改善长任务体验。

**阶段验收**

- Windows 父子进程树可被指定 run 的 cancel 终止。
- progress 有序、可去重、有限流，不阻塞 result。
- progress 不改变唯一终态规则。
- 对应平台和全仓测试通过。

**退出门禁**

Windows 进程树测试未通过时，不宣称跨平台完整取消；progress 影响最终结果投递时不得启用。

## 6. 验收矩阵

| 能力 | Phase 0 | Phase 1 | Phase 2 | Phase 3 | Phase 4 | Phase 5 |
|---|---:|---:|---:|---:|---:|---:|
| 协议字段和消息语义 | 必须 | 回归 | 回归 | 回归 | 回归 | 回归 |
| Approval 摘要与有效期 | 必须 | 必须 | 回归 | 审计 | 隔离 | 回归 |
| Hand 最终守门 | 定义 | 必须 | 回归 | 回归 | 无旁路 | 回归 |
| accepted/rejected | 定义 | 必须 | 必须 | 审计 | 展示 | 回归 |
| duplicate run 防护 | 定义 | 必须 | 必须 | 审计 | 回归 | 回归 |
| 显式取消 | 定义 | 准备 | 必须 | 审计 | 手动入口 | 跨平台 |
| 唯一终态 | 定义 | 准备 | 必须 | 持久化 | 会话隔离 | progress 回归 |
| 断线收敛 | 定义 | 无重试 | 必须 | 持久化 | 展示 | 回归 |
| 服务模式统一路由 | 接口 | 接口 | 可接入 | 必须 | 必须 | 回归 |
| 审计与恢复 | schema 评审 | 字段准备 | 事件来源 | 必须 | 查询展示 | progress 事件 |
| 多会话 race | 模型评审 | 局部 | registry | 服务级 | 必须 | 回归 |

## 7. 测试责任划分

| 测试层级 | 负责内容 | 主要任务 |
|---|---|---|
| 协议单测 | JSON、摘要向量、状态迁移、错误码、兼容性 | T0、T1 |
| Hand 单元测试 | 守门规则、任务表、重复 run、取消裁决 | T2、T3、T4 |
| Hand 集成测试 | 实际工具执行、进程终止、并发 RPC | T3、T4、T10 |
| Mind 单元测试 | registry、来源校验、唯一终态、断线处理 | T5 |
| Mind 集成测试 | `use_hand`、服务模式、审计、恢复 | T5、T6、T7 |
| 多入口 race 测试 | Chat、activeHand、审批缓存、run 事件投递 | T8 |
| 端到端测试 | Mind + Hand 注册、执行、取消、重启和查询 | Phase 2 至 Phase 4 |

所有 Go 测试必须使用 `-race -count=1`。阶段合并前至少执行受影响模块测试；Phase 3 起必须执行 `make test`。

## 8. 发布判定

### Alpha

需要完成 Phase 0 至 Phase 2。

允许限制：

- 只在 REPL 或内部环境试用。
- 审计可暂以内存事件验证，不对外承诺持久追溯。
- Windows 不承诺进程树完整取消。

### Beta

需要完成 Phase 0 至 Phase 3。

必须满足：

- 服务模式可独立运行完整闭环。
- 审计和启动恢复通过测试。
- 无已知安全检查旁路和终态竞争问题。

### Stable / Face 接入基线

需要完成 Phase 0 至 Phase 4。

Face 自身的正式协议、独立鉴权、快照恢复、有序事件投影和 Headless Agent E2E 还必须满足 [`face-protocol.md`](face-protocol.md) 的 Alpha 完成定义；本计划只覆盖远程执行侧的接入门槛。

必须满足：

- 多 session 并发隔离通过 race 测试。
- 手动和 LLM 两种入口共用同一审批、registry 和审计链路。
- 发布说明明确平台能力和未实现的增强项。

Phase 5 的 Windows 完整取消是宣称“跨平台取消闭环”的前提；进度流不是 Stable 的必要条件。

## 9. 实施纪律

- 每个子任务单独提交，commit 只解决一个验收目标。
- 协议变更必须先合并测试，再修改 Mind 与 Hand 行为。
- 不为尚未实现的重连恢复添加兼容分支。
- 不自动重试有副作用的远程工具。
- 不以日志文本代替结构化状态和错误码。
- 不通过扩大锁范围掩盖状态机缺陷；终态裁决必须集中实现。
- 任何新增旁路入口都必须复用 Approval、registry 和审计链路。
- 阶段验收失败时先修复当前阶段，不并行扩展 progress 或后台任务。

## 10. 完成定义

远程执行闭环只有在以下条件全部满足时才视为完成：

- Phase 0 至 Phase 4 全部通过。
- `make test` 在 race 模式下通过。
- 安全拒绝场景均证明工具未执行。
- 取消、结果、超时和断线竞争只有一个终态。
- 服务模式和 REPL 共用同一远程执行权威。
- 数据库可追溯每次执行且不保存原始敏感参数。
- 多会话状态无串扰。
- 文档、协议类型、实际行为和用户可见能力说明一致。
