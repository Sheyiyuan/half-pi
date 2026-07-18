# 下一阶段开发计划

## 状态

活动计划，制定于 2026-07-17。本文基于设计文档与当前代码、测试的逐项核对，作为下一阶段开发入口；协议细节仍以 [`face-protocol.md`](face-protocol.md) 和 [`remote-execution-closed-loop.md`](remote-execution-closed-loop.md) 为准。

## 现状结论

### 已落地

- Mind → Hand 的 Approval、accepted/rejected、显式取消、唯一终态和来源校验。
- 服务级 `remoteexec.Authority`、SQLite run 审计与启动恢复。
- 每个已加载 conversation 独立 Core/RemoteBridge，手动入口与 LLM 入口复用同一执行链路。
- Hand 工具 schema、allow/deny、本地最终检查、输出截断和 Unix 进程组取消。
- R0 回归证据、R1 Windows 多架构交叉编译入口、R2 有界进度流和 R3 持久化后台任务闭环。

历史 MVP 设计和设计债清单已归档到 `docs/archived/`。闭环设计与实施计划继续保留，因为仍有未完成或未验收能力。

### 尚未落地

- 正式 Face 消息、独立 token、identity、scope 和 conversation 授权。
- 可供服务模式复用的 Conversation Actor 管理器和 Face Gateway。
- Face 快照、订阅、有序出站队列、背压和结构化事件投影。
- Chat `request_id`、幂等、busy、取消和唯一终态响应。
- 异步审批对象及 Face 审批审计。
- Headless Agent Face、Scripted LLM 和真实进程级 E2E。
- Windows Job Object 取消逻辑的原生 Windows 测试验收。

## 开发原则

- 先建立服务端安全边界和确定性测试入口，再做 Web/TUI 外观。
- Face 与 Hand 只复用传输层，不复用 token 类型或授权路径。
- Face Gateway 不直接发送 Hand RPC；run 查询和取消必须复用 `remoteexec.Authority`。
- SQLite 和运行时 registry 是权威状态，EventBus 只用于观察，不从展示文本反推业务状态。
- 每个阶段先冻结协议和验收测试，再接入下一层行为。
- 已完成的进度流和后台任务不改变 Face runtime 延期边界。

## 已完成：Face Wire Protocol

2026-07-17 已完成 `gateway-core/protocol` 下的 `face.*` typed payload、scope、错误码、结果/审批/事件枚举、快照 DTO、结构化事件 DTO 和严格结构验证，并通过 gateway-core race 测试。未来单用户 Alpha 采用“有效 Face identity 可访问全部 conversation，写操作由 scope 控制”的访问模型。

Face 独立 token、Mind peer dispatcher、Gateway、Conversation Actor、Chat runtime、异步审批、Headless/TUI/Web 客户端和进程级 E2E 均推迟到后续阶段。本阶段只保留正式协议和 [`ai-face-protocol.md`](ai-face-protocol.md) 接入文档，不落地 Face runtime。

## 已冻结：Face 核心收口方案

Face wire protocol 的最终收口将补齐持久化后台 task 协议，并把身份边界推进到独立 Face credentials、Hand/Face 四步挑战握手和注册后业务 payload 强制加密。冻结 schema、消息结构、迁移、五阶段提交和验收矩阵见 [`face-core-closure-plan.md`](face-core-closure-plan.md)。该范围不包含 Face Gateway command routing、Conversation Actor、Chat runtime 或客户端。

## 后续阶段：Face Alpha Runtime

### P0：协议与身份边界

**目标**

冻结 Face wire contract，并修复当前所有 peer 共用 Hand token 校验路径的问题。

**范围**

- 在 `gateway-core/protocol` 增加 `face.*` typed payload、错误码、scope 和验证函数。
- Face payload 只使用 `conversation_id`，不得暴露含糊的业务 `session_id`。
- 增加独立 `face_tokens`、Face identity、scope、撤销和校验 API。
- 将 Hub 回调组合提升到 Mind 服务层，按已认证 peer type 分发到 Hand Authority 或 Face Gateway。
- Face 断连不得触发 Hand run 的 `lost`；Face/Hand token 不可交叉使用。

**验收**

- 所有 Face payload JSON 往返和非法字段/枚举测试通过。
- Hand token 注册 Face、Face token 注册 Hand 均被拒绝。
- scope 缺失返回稳定的 `forbidden`，日志和事件不包含 token。
- 同 ID 的 Face 不得替换 Hand 连接。
- `gateway-core` 和 `half-pi-mind` 测试在 `-race -count=1` 下通过。

### P1：Conversation Actor 与只读 Gateway

**目标**

让默认服务模式拥有可复用的 conversation runtime，并提供可恢复的只读 Face。

**范围**

- 从 `cmd/half-pi-mind/repl.go` 提取 Conversation Actor 管理器和 Core/RemoteBridge 工厂。
- 持久化并恢复 conversation mode、active Hand 和 history。
- 实现 conversation list/create/rename/snapshot、Hand list/get、run get 和 subscribe。
- 快照合并 SQLite 历史与 registry 活跃状态；历史 run 查询必须有 store fallback。
- 每个 Face 连接使用单发送循环、有界队列、单调 `event_seq` 和明确慢客户端策略。
- 为 conversation、Hand 和 run 变化提供结构化 domain event，不解析 EventBus 展示文本。

**验收**

- 两个 Face 可读取同一 conversation，重连后快照恢复消息、mode、active Hand 和 run 状态。
- conversation 过滤和 scope 校验不会泄漏其他会话。
- 慢 Face 被单独断开，不阻塞 Chat、Authority 或其他 Face。
- Registry 已裁剪或 Mind 重启后的 run 仍能从 SQLite 查询。
- 多 Face、断连清理和事件顺序测试通过 race detector。

### P2：Chat 生命周期

**目标**

建立 `face.chat` 从 accepted 到唯一终态的完整、可取消、幂等生命周期。

**范围**

- 增加 `(identity, request_id)` 请求登记、payload 摘要、状态和结果保留。
- 同 conversation 一次只接受一个 active Chat，第二个请求立即返回 `busy`。
- 将 request context 传递到 `Core.Chat`、工具和 remote run，建立 request/run 关联。
- 实现 `face.chat.cancel`；等待远程 run 时必须继续触发 `rpc_cancel`。
- 提供不依赖真实模型的 Scripted LLM/fixture provider。
- 发出结构化 chat/tool lifecycle event，不依赖 debug 开关，不包含原始敏感参数。

**验收**

- accepted Chat 恰好产生一个 `face.result`。
- 相同 request ID 和相同 payload 不重复调用模型；不同 payload 返回 `request_conflict`。
- 同 conversation 返回 busy，不同 conversation 可并发且 history 不串扰。
- Chat 取消能终止本地调用并取消正在等待的 remote run。
- Scripted LLM 可确定性完成多轮工具调用测试。

### P3：异步审批与 run 同步

**目标**

把阻塞 stdin 审批升级为 conversation 级异步对象，同时保留 REPL 适配。

**范围**

- Approval 对象绑定 approval、conversation、request、可选 run、tool、args digest 和 expiry。
- 实现 `approval.requested`、`face.approval.resolve`、`face.run.cancel` 和完整 `remote_run.changed`。
- 首个合法裁决生效；校验 `face:approve`、过期、重复裁决和 conversation 归属。
- 审计 Face identity、decision、时间和摘要，不保存原始参数。
- REPL 通过同一 approval broker 裁决，不保留第二条安全路径。

**验收**

- 无审批 scope、过期审批和重复裁决均被结构化拒绝且工具不执行。
- 两个 Face 并发裁决只有一个成功。
- 修改审批后参数会被 Hand 以 digest mismatch 拒绝。
- session 级 allow/deny 不跨 conversation。
- run cancel 只经 Authority，result/cancel 竞争仍只有一个终态。

### P4：Headless Face 与进程级 E2E

**目标**

用正式协议完成第一个可用 Face，并以真实进程验证跨设备闭环。

**范围**

- 将 `half-pi-face` 实现为 JSONL Headless Agent Face，stdout 仅协议，日志写 stderr。
- 使用临时数据目录、动态端口、Scripted LLM 和结构化 ready 启动真实 Mind、Hand、Face。
- 覆盖跨 Face 恢复、远程执行、审批、取消、断线恢复和幂等场景。
- 将 `half-pi-face` 纳入 `make test`。
- 在协议稳定后选择 Web 或 TUI 作为第二个 Face；UI 不先于 Headless E2E。

**验收**

- 测试不调用真实模型、不使用固定 sleep、不走测试专用协议旁路。
- SQLite 消息、run 和审批审计与 Face 最终响应一致。
- Face 断开不默认取消已接受 Chat，重连快照可恢复终态。
- Headless Face 与首个人类 Face 对同一 conversation 观察一致。
- 全仓 `make test` 在 race 模式下通过。

P0 中 wire contract 已完成，其余 P0 以及 P1-P4 整体延期，不属于当前实施范围。

## 当前主线：远程执行收尾

R0-R3 已完成。涉及 Face 事件投影的部分只保持协议兼容，不实现 Face Gateway。

### R0：补齐闭环回归证据

- 增加 Unix 子孙进程树取消测试。
- 增加“取消一个并发 run 不影响另一个”的 Hand 集成测试。
- 增加 Authority shutdown 将非终态 run 持久化为 `lost` 的测试。
- 增加服务模式/REPL 共用 Authority 和手动命令完整路径的集成测试。

### R1：Windows 原生验收

- 当前 Linux 阶段以 Windows 多架构交叉编译和可复用原生验收脚本为出口。
- 使用 `scripts/test-windows.ps1 -CompileOnly` 复现交叉编译；原生 Windows 使用 `scripts/test-windows.ps1` 执行 race 测试。
- 原生 Windows 运行 `modules/half-pi-core/tools/tool_exec_windows_test.go` 继续作为发布环境验收项。
- 验证父子孙进程退出、无关进程不受影响、预取消和正常后台进程语义。
- 验收前继续声明“实现已交叉编译，未完成跨平台取消验收”。

### R2：可选进度流

- 已实现 `rpc_progress` 和独立的单 run `seq`，支持 Unix `exec_command` 以及共享同一输出实现的 Windows `exec_cmd` / `exec_ps`。
- stdout/stderr 分流；协议统一限制 progress 单块 4 KiB、每 run 1 MiB/256 个事件。Hand 的 progress 总量另取配置输出上限与协议上限的较小值，最终输出仍只服从原配置。
- Hand 使用有界非阻塞队列，队满可丢弃且 final result 前直接停止、不排空；已开始写入的 WebSocket 帧不可抢占，result 最多等待该帧至传输写超时。Mind 单调接收并去重，观察缺口但不等待补齐。
- progress 独立限量审计和发布，不改变状态；重复、晚到、超限事件不发布，审计失败不 fail-closed。

### R3：后台任务

- 已实现独立 start/status/log/cancel 生命周期，`task_id == start_run_id`。start run 在 Hand 持久接纳后终止，后台 task 继续按独立状态机运行。
- Mind 的 `remote_tasks` 只保存会话归属、Hand、tool、参数摘要、状态、时间、日志字节数和截断信息；Hand 使用独立 SQLite 保存元数据，日志写入权限受限的独立文件，不持久化原始参数。
- 后台 RPC 强制一次性 Approval，摘要额外绑定 background、task ID 和最大运行时间；Hand 继续执行 allow/deny 和本地 `Tool.Check`。
- 任务跨 WebSocket 断线继续运行，重连后可 status/log/cancel；Hand 重启将未完成任务标记 `lost`，不会自动重跑。Mind 重启把非终态快照标记 stale，在线查询时对账。
- Hand 保留 compact tombstone 防止旧 task ID 重放，详情和日志按 retention/配额清理。
- Mind 提供 `get_hand_task`、`read_hand_task_log`、`cancel_hand_task`，REPL 提供 `/hand task start|status|log|cancel`。

## 推荐执行顺序

1. 在原生 Windows 环境运行 `scripts/test-windows.ps1`，完成外部平台验收。
2. 后续单独启动 Face Alpha Runtime P0-P4。
3. `/compact` 和 Skill 工作区集成继续按独立主线推进。

## 完成判定

当前阶段以 R0-R3 的 Linux/race 测试和 Windows 交叉编译验收通过为准；不据此宣称原生 Windows 已验收。Face Alpha 仍以未来 P0-P4 runtime 全部验收为准。任何阶段仅在对应测试和文档能力声明与实际行为一致后标记完成。
