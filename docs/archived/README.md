# 已归档设计文档

本目录保存已经完成、被后续设计取代或仅用于追溯决策的文档。它们不是当前实施入口；现行协议和待实现设计以 `docs/` 根目录中的活动文档为准。

## 当前文档

| 文档 | 用途 |
|---|---|
| [`../face-protocol.md`](../face-protocol.md) | Face 正式协议与 Alpha runtime 设计 |
| [`../ai-face-protocol.md`](../ai-face-protocol.md) | AI/Headless Face 接入指南 |
| [`../remote-execution-closed-loop.md`](../remote-execution-closed-loop.md) | Mind → Hand 远程执行闭环架构 |
| [`../mind-management-cli.md`](../mind-management-cli.md) | 待实现的 Mind 本地管理 CLI、IPC 与离线管理设计 |

## 归档目录

| 文档 | 归档原因 |
|---|---|
| [`architecture.md`](architecture.md) | 早期完整系统架构，后续由各专项协议和实现演进 |
| [`mind-service-mode.md`](mind-service-mode.md) | Mind 服务模式设计已经落地 |
| [`provider-adapter.md`](provider-adapter.md) | Provider/Model 适配器设计已经落地 |
| [`skill-design.md`](skill-design.md) | Skill 文件系统与加载机制设计已经落地 |
| [`skill-session-memory-design.md`](skill-session-memory-design.md) | Skill、会话和记忆的早期总体设计；部分工作区集成仍由当前路线图跟踪 |
| [`remote-execution.md`](remote-execution.md) | Mind → Hand MVP 设计，已被闭环架构取代 |
| [`mind-hand-mvp-followups.md`](mind-hand-mvp-followups.md) | MVP 设计债清单已经收口 |
| [`remote-execution-implementation-plan.md`](remote-execution-implementation-plan.md) | 远程执行 Phase 0-5 实施与验收已经完成 |
| [`face-core-closure-plan.md`](face-core-closure-plan.md) | Face wire、身份与加密收口实施已经完成 |
| [`next-development-plan.md`](next-development-plan.md) | Face Alpha P0-P4 与远程执行 R0-R3 收尾已经完成 |

归档文档中的阶段性限制和未来时态应按其历史时间点理解；当前能力声明以活动文档和仓库代码为准。
