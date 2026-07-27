---
title: 内容覆盖矩阵
description: 已实现子系统在问题驱动教程与当前架构手册中的双重覆盖。
sidebar:
  order: 8
---

此矩阵用于防止「代码已实现但教程没有解释」，或「教程提到能力却没有手册入口」。每个条目至少指向一章教程和一篇手册。

| 已实现子系统 | 教程 | 手册 |
|---|---|---|
| Agent Core / 工具循环 | [2 · Agent 循环](../../tutorial/02-agent-loop/) | [half-pi-mind](../half-pi-mind/) |
| LLM adapters | [3 · 模型适配](../../tutorial/03-model-providers/) | [half-pi-mind](../half-pi-mind/) |
| Catalog / 通用工具 | [4 · 工具](../../tutorial/04-tools/) | [half-pi-core](../half-pi-core/) |
| ToolRuntime / 参数冻结 | [5](../../tutorial/05-registration-is-not-safety/)、[6](../../tutorial/06-tool-runtime/) | [half-pi-core](../half-pi-core/) |
| 安全策略 / Reviewer | [6 · 审批与 ToolRuntime](../../tutorial/06-tool-runtime/) | [half-pi-core](../half-pi-core/)、[half-pi-mind](../half-pi-mind/) |
| EventBus / Lifecycle / Outbox | [7](../../tutorial/07-observability/)、[19](../../tutorial/19-lifecycle-audit/) | [half-pi-core](../half-pi-core/)、[状态机](../state-machines/) |
| Store / session / message | [8 · 持久化](../../tutorial/08-persistence/) | [half-pi-mind](../half-pi-mind/) |
| Skill Store / group 隔离 | [9 · Skill](../../tutorial/09-skills/) | [half-pi-mind](../half-pi-mind/) |
| 三端与五 module | [10 · 三端拆分](../../tutorial/10-face-mind-hand/) | [系统总览](../)、[五个 module](../modules/) |
| Gateway protocol / wss / Hub | [11 · Gateway 安全](../../tutorial/11-gateway-security/) | [gateway-core](../gateway-core/) |
| Hand node runtime | [12 · Hand 守门](../../tutorial/12-hand-guard/) | [half-pi-hand](../half-pi-hand/) |
| RemoteRun / Authority | [12](../../tutorial/12-hand-guard/)、[13](../../tutorial/13-remote-jobs/) | [half-pi-mind](../half-pi-mind/)、[状态机](../state-machines/) |
| Progress / durable task | [13 · 远程任务](../../tutorial/13-remote-jobs/) | [half-pi-hand](../half-pi-hand/) |
| Mind service / Conversation Actor | [14 · Mind 与 Actor](../../tutorial/14-mind-service-actors/) | [half-pi-mind](../half-pi-mind/) |
| Face Gateway / snapshot / replay | [15 · Face 协议](../../tutorial/15-face-protocol/) | [half-pi-mind](../half-pi-mind/) |
| Approval Broker / 异步审批 | [16 · 异步审批](../../tutorial/16-async-approval/) | [half-pi-mind](../half-pi-mind/)、[状态机](../state-machines/) |
| Headless / TUI | [17 · Face 客户端](../../tutorial/17-face-clients/) | [half-pi-face](../half-pi-face/) |
| Management / IPC / 平台安全 | [18 · 运维与跨平台](../../tutorial/18-operations/) | [half-pi-mind](../half-pi-mind/) |
| Compact / Context Summary | [20 · 上下文压缩](../../tutorial/20-context-compaction/) | [half-pi-mind](../half-pi-mind/)、[状态机](../state-machines/) |
| 真实进程 E2E | [21 · 毕业章](../../tutorial/21-graduation/) | [五个 module](../modules/) |
