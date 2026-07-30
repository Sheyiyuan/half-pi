---
title: 术语表
description: Half-Pi 教程与架构手册中的核心中文、英文术语和项目名词。
sidebar:
  label: 术语表
---

术语按英文名或项目符号排序。首次阅读不必记忆，可从教程中的链接回查。

## A–C

**AAD（Additional Authenticated Data，附加认证数据）** 参与 AEAD 完整性校验但不加密的消息头。Half-Pi 用它把 Envelope 头与密文绑定。

**Actor（参与者模型中的状态所有者）** 串行处理属于同一对象的写操作。Half-Pi 每个 conversation 有独立 Actor。

**Admission（准入）** 副作用执行前，对身份、参数、策略、审批和必需审计作出的接纳决定。

**Agent** 能在目标、观察与动作之间循环的系统。语言模型只是其中负责生成候选下一步的一部分。

**Approval（审批）** 用户对一份绑定 request、run、tool 与参数摘要的具体敏感动作作出的裁决。

**CAS（Compare-and-Swap，比较并交换）** 只有当前版本仍符合预期才提交变更的并发控制方式。Compact 用它避免旧摘要覆盖新上下文。

**Catalog（工具目录）** 工具名称、Schema、行为与版本的可查询集合。

**Chat** 一次被 Mind 接纳并最终达到 succeeded、failed、cancelled 或 timed_out 的 conversation 操作，不等同于 WebSocket 连接。

**Context Summary（上下文摘要）** 覆盖旧消息前缀的版本化模型视图投影；不删除或改写原始消息。

**Conversation** 用户可持续恢复的一段对话；拥有独立 Actor、session 状态和消息历史。

## D–H

**Digest（摘要）** 对规范数据计算的 SHA-256 等固定长度值，用于证明审批与实际参数对应，不用于恢复原文。

**Detail mode（详情模式）** Face 订阅协商的 `transparent` 或 `summary` 视图。模式在工具、run 或 task admission 时冻结，不能把既有摘要历史升级为透明原文。

**Display projection（展示投影）** 面向用户的版本化参数或结果视图，经过 schema/中央规则的 show、mask、hide、preview 处理；它不替代冻结调用摘要，也不进入安全审计表。

**Durable task（持久化后台任务）** 在 Hand 网络断开后仍可继续，并由 Hand 本地数据库与日志保存状态的任务。

**Envelope（信封）** Gateway 的统一消息外壳，包含消息、连接 session、来源、目标、序号和加密 payload。

**EventBus（事件总线）** 进程内尽力观察通道，不承担安全 Guard 或权威业务状态。

**Face** 用户或自动化客户端的交互进程。当前实现包括 Headless JSONL 与全屏 TUI。

**Fail closed（失败关闭）** 依赖或检查失败时拒绝继续，而不是扩大权限。例如 admission audit 失败时不执行工具。

**Fail open（失败开放）** 非关键观察失败时业务继续。例如普通 Observer 队列满不会改变工具结果。

**FrozenInvocation（冻结调用）** 完成变换与 Schema 校验后，不再允许替换工具或参数的调用事实，带有参数摘要。

**Guard（守卫）** Lifecycle 中只能保持或收紧裁决的同步 Hook。

**Hand** 位于目标设备的执行进程，拥有 node-local ToolRuntime 与最终拒绝权。

**Headless Face** 通过 stdin / stdout JSONL 使用正式 Face 协议的非图形客户端。

**Hub** Mind 内管理已认证 Face / Hand peer 连接与消息分发的 Gateway 组件。

## L–R

**Lifecycle（生命周期）** Message、Model、Assistant、Tool、Security、Approval 与 Chat 共用的 Meta、Phase、Outcome 和 Hook 契约。

**LLM（Large Language Model，语言模型）** 根据输入生成文字或结构化工具调用的模型，不是完整 Agent。

**Mind** Half-Pi 的智能与协调状态中心，持有模型、conversation、审批、远程 run、Gateway 和 Store。

**Observer（观察者）** 接收隔离事件视图的异步、有界 Hook，不能改变业务事实。

**Outbox（事务发件箱）** 与业务 / 审计事实同事务写入、随后由 dispatcher 可靠投递的记录。

**Principal（主体）** 完成握手后绑定到连接的稳定身份；不是可复用的显示 label。

**Progress（进度）** 有界、可能丢失的运行观察，不代表 terminal status。

**Provider（模型提供商适配器）** 把统一模型请求翻译为具体服务协议并还原统一响应。

**RemoteRun（远程运行）** Mind 对一次发往 Hand 的前台 RPC 建立的状态与审计对象。

**Replay（请求重放）** 用相同 principal、request ID 和 payload 取回已有接纳或终态；不是再次执行动作。

**Reviewer（安全复核模型）** 与主 Agent 隔离、无工具，只能返回 allow 或 require_user 的独立模型请求。

## S–W

**Scope（权限范围）** Face principal 可执行的命令集合；每条 command 都重新校验。

**SessionGroup（会话组）** 工作区、Soul 与 Skill 可见性的隔离边界，可包含多个 session。

**Snapshot（快照）** Mind 为 Face 合并的 conversation、消息、审批、run 与 task 当前权威视图。

**Summary（摘要视图）** 工具详情的最小展示形式，只含工具、状态、长度、digest、稳定错误类别和告警，不含原始参数或结果正文。

**Terminal state（终态）** 状态机不再接受普通进展的结果，如 succeeded、failed、cancelled、timed_out 或 lost。

**Tool（工具）** 向模型描述、由系统实现的结构化动作。注册并不等于获准执行。

**Tool history projection（工具历史投影）** Store 为已接纳工具调用保存的版本化展示记录，供 `snapshot.tool_history` 恢复；透明记录可降级为 summary，旧历史不回填透明详情。

**ToolRuntime** Half-Pi 生产工具调用唯一入口，负责变换、Schema、冻结、Guard、授权、审计、执行与结果交付。

**Transformer（变换器）** Lifecycle 中在规定阶段改变调用或交付内容的 Hook；不能绕过强制确认或 hard deny。

**Transparent（透明视图）** 工具详情展示模式，返回经过展示策略处理的参数、progress 和可靠终态，可能包含用户自己传入的秘密；不等于安全审计原文。

**TUI（Terminal User Interface，终端用户界面）** Half-Pi Face 的 Bubble Tea 全屏工作台。

**Wire contract（线上契约）** 跨进程消息的类型、字段、顺序与验证规则。

**Writer** EventBus 的展示输出端，例如 ConsoleWriter 与 JSONL FileWriter。
