---
title: half-pi-core
description: Catalog、ToolRuntime、安全、Lifecycle、EventBus 与通用工具的当前契约。
sidebar:
  order: 3
---

`half-pi-core` 是 Mind 与 Hand 的共同执行地基。它保持零第三方依赖，避免任一进程复制或削弱生产工具准入链。

## ToolRuntime

```mermaid
flowchart LR
    I["Invocation"] --> TR["Transformer"] --> SV["Schema validation"]
    SV --> F["FrozenInvocation + digest"]
    F --> G["Guards"] --> A["Authorizer"] --> AU["Admission audit"]
    AU --> E["一次性 Execute"] --> RT["Result transformer"] --> TA["Terminal audit"]
```

Catalog 解析工具并提供不可变快照；Transformer 只能在冻结前改变调用。未配置 Authorizer 默认拒绝。外部执行使用 `PreparedExternal`，把 run、目标、工具、参数和后台标志绑定进 contract digest。

## 安全

`security` 提供确定性黑 / 灰名单与脱敏。strict hard deny 始终拒绝；normal 对敏感命令请求确认。Mind 的 Reviewer 与 Approval 是 Authorizer 组合层，不位于通用 core 中。

## Lifecycle

Message、Model、Assistant、Tool、Security、Approval、Chat 共享 Meta / Phase / Outcome。四类 Hook 能力分离：Guard 单调收紧；Transformer 改变准入前输入或交付结果；Observer 异步有界且 fail open；Auditor 按阶段决定是否 fail closed。Lifecycle context 会沿 ToolRuntime 的 prepare、admission、execute、terminal 路径传播 conversation、request、run、detail mode 和冻结调用摘要，使 REPL、Face、EventBus 看到同一条事实链。

## 工具展示投影

`PropertySchema.Display` 是用户可见性契约，不参与安全裁决：

| 策略 | 展示行为 |
|---|---|
| `show` | 返回原值（默认策略） |
| `mask` | 返回固定 `[masked]` |
| `hide` | 保留字段名和状态，不返回值 |
| `preview` | 返回 UTF-8 安全前缀、原始字节数和截断标记 |

中央高置信字段名规则可以把 `show`/`preview` 收紧为 `mask`；显式 `hide` 不会被放宽。参数与结果先生成版本化展示投影，再由 lifecycle/EventBus、REPL 或 Face 按 `summary`/`transparent` 视图交付。内容扫描只产生告警，不把用户字符串当成可静默删除的秘密。展示投影不进入 approval/security audit 表。

## EventBus

EventBus 负责进程内观察与 Console / JSONL File Writer。`PublishSync` 可保证 REPL 输出顺序，但 EventBus 不承担 Guard、领域状态或必需审计。收到 transparent 工具事件时，ConsoleWriter 将参数、progress 或结果投影写到 stderr；FileWriter 以每行一个 JSON 事件保留同一投影，便于用户日志分析。summary 事件则只写稳定摘要元数据。两种 Writer 都是展示路径，不能代替安全审计；透明日志可能包含用户自己传入的秘密。

## 通用工具

`tools/` 当前包含文件读写、精确编辑、字面量 / 正则搜索、文件列表与跨平台命令执行。写入类工具默认确认；命令超时按 Unix 进程组或 Windows Job Object 清理进程树。

## 失败语义速查

| 组件 | 失败策略 |
|---|---|
| Guard 超时 / panic | fail closed |
| 无 Authorizer | deny |
| Observer 满 / panic | fail open，不改变事实 |
| Admission audit 失败 | 不执行副作用 |
| Terminal audit 失败 | 保留执行事实，交付状态单独表达 |

源码与行为测试从 [`executor/runtime.go`](https://github.com/Sheyiyuan/half-pi/blob/main/modules/half-pi-core/executor/runtime.go)、[`lifecycle/lifecycle_test.go`](https://github.com/Sheyiyuan/half-pi/blob/main/modules/half-pi-core/lifecycle/lifecycle_test.go) 和 [`security/security_test.go`](https://github.com/Sheyiyuan/half-pi/blob/main/modules/half-pi-core/security/security_test.go) 进入。
