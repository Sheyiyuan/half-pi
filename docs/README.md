# Half-Pi 文档导航

`docs/` 根目录只保存当前协议、接入指南和运维说明。已经完成的实施计划、被后续设计替代的方案及仅用于决策追溯的文档统一放入 [`archived/`](archived/README.md)。

## 当前文档

| 文档 | 定位 |
|---|---|
| [`face-protocol.md`](face-protocol.md) | Face 正式协议、身份、鉴权、快照、审批和事件投影 |
| [`face-streaming-protocol.md`](face-streaming-protocol.md) | Face 应用协议 revision 2 的流式、背压、恢复和终态语义 |
| [`ai-face-protocol.md`](ai-face-protocol.md) | AI、Headless Agent 和自动化客户端接入指南 |
| [`remote-execution-closed-loop.md`](remote-execution-closed-loop.md) | Mind 到 Hand 的远程执行、进度流和持久化任务架构 |
| [`mind-management-cli.md`](mind-management-cli.md) | Mind 本地管理 CLI、在线 IPC、离线管理和平台边界 |
| [`lifecycle-hooks-and-security-audit.md`](lifecycle-hooks-and-security-audit.md) | 已实现的统一生命周期 Hook、隔离 Reviewer、安全审计和插件前置契约 |
| [`plugin-architecture.md`](plugin-architecture.md) | 尚未实现的插件契约、Goja 宿主、process/WASM 运行时和实施顺序提案 |

## 维护约定

- 当前 wire contract、用户可见行为或运维入口发生变化时，更新根目录中的对应文档。
- 阶段性实施计划完成后移入 `archived/`，并在归档索引中写明替代文档或当前权威来源。
- 归档文档保留历史时间点的限制与未来时态，不再作为当前能力声明。
- 项目进度和待办以 [`../AGENTS.md`](../AGENTS.md) 为准。
