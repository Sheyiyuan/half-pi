# Half-Pi 文档导航

`docs/` 根目录只保存当前协议、接入指南和仍在推进的设计提案。已经完成的实施计划、已落地规格、被后续设计替代的方案及仅用于决策追溯的文档统一放入 [`archive/`](archive/README.md)。

面向学习者的项目介绍、代码地图、端到端调用链和设计取舍见 [Half Pi Wiki](https://sheyiyuan.github.io/half-pi/)；站点源码位于 [`../wiki/`](../wiki/)。

## 当前文档

| 文档 | 定位 |
|---|---|
| [`face-protocol.md`](face-protocol.md) | Face 正式协议、身份、鉴权、快照、审批和事件投影 |
| [`ai-face-protocol.md`](ai-face-protocol.md) | AI、Headless Agent 和自动化客户端接入指南 |
| [`plugin-architecture.md`](plugin-architecture.md) | 尚未实现的插件契约、Goja 宿主、process/WASM 运行时和实施顺序提案 |
| [`gateway-handshake-v3.md`](gateway-handshake-v3.md) | 已实现的 v3 握手：transcript 完整性、X25519 前向保密、label 枚举关闭，及仍未解决的元数据、静态身份与限流项 |
| [`mcp-server.md`](mcp-server.md) | 尚未实现的 MCP server 提案：关闭 Agent 后向外部 Agent 暴露工具执行的信任模型与接入形态 |

## 维护约定

- 当前 wire contract、用户可见行为或运维入口发生变化时，更新根目录中的对应文档。
- 阶段性实施计划完成后移入 `archive/`，并在归档索引中写明替代文档或当前权威来源。
- 归档文档保留历史时间点的限制与未来时态，不再作为当前能力声明。
- 项目进度和待办以 [`../AGENTS.md`](../AGENTS.md) 为准。
