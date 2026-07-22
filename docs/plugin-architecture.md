# 插件架构与运行时设计

> 状态：提案（2026-07-22），尚未实现。
>
> 本文定义 Half-Pi 插件的信任分层、运行时选型、capability、scope、manifest、Goja 宿主 API、process/WASM 边界、失败语义和实施顺序。插件依赖的统一生命周期、安全审查、审计和 ToolRuntime 前置工作，以 [`lifecycle-hooks-and-security-audit.md`](lifecycle-hooks-and-security-audit.md) 为权威来源。

相关文档：

- [`lifecycle-hooks-and-security-audit.md`](lifecycle-hooks-and-security-audit.md)：插件开放前必须完成的生命周期、安全、审计和隔离基础；
- [`face-protocol.md`](face-protocol.md)：Face identity、scope、conversation ownership 和审批投影；
- [`remote-execution-closed-loop.md`](remote-execution-closed-loop.md)：插件工具涉及远程执行时必须复用的 RemoteRun 与 Hand 最终守门；
- [`mind-management-cli.md`](mind-management-cli.md)：未来插件安装、启用、禁用和授权应复用的本地管理与审计边界。

## 1. 摘要

Half-Pi 不应把插件实现绑定到某一种语言，也不应把“能嵌入脚本”误认为“已经具备插件安全架构”。插件先消费稳定的 Lifecycle wire contract，再由不同 runtime adapter 承载执行。

本文建议：

1. 首版使用 `goja + JavaScript`，服务用户自己编写或明确安装并信任的轻量插件；
2. Goja 不是安全沙箱，不承载来源未知的公共市场插件；
3. 需要原生 SDK 或任意语言时使用受信子进程，主要获得故障隔离而非完整权限隔离；
4. 需要运行来源不完全可信的纯计算插件时，在协议稳定后优先评估 `wazero`；
5. 核心 Guard、AIReviewerGuard、Approval Broker、Auditor 和 Hand 本地策略保持内置 Go 实现；
6. 所有运行时共享相同的 capability、scope、数据视图、Hook 结果和失败语义；
7. 插件开放顺序固定为 redacted Observer、restrictive Guard、Transformer，最后才是 action 和 tool registration。

当前代码库还没有插件 runtime、manifest loader 或 Goja/WASM 依赖。本文只定义目标架构，不把提案描述为已实现能力。

## 2. 前置条件与文档边界

插件实现开始前，[`lifecycle-hooks-and-security-audit.md`](lifecycle-hooks-and-security-audit.md) 中的以下基础必须完成：

- 生命周期阶段、Meta、顺序和终态语义稳定；
- ToolRuntime 成为唯一生产工具执行入口，生产路径不能使用 `SkipChecks`；
- Frozen Action、canonical digest、Approval 和实际执行参数完全绑定；
- Guard、Transformer、Observer、Auditor 的失败语义已经分离；
- raw、redacted、digest 三种数据视图由宿主生成；
- SessionGroup、conversation、principal 和 node scope 可执行；
- transactional outbox、可靠审计和重入限制可用。

本文负责插件运行时、manifest、capability 申请、宿主 API 和运行时资源治理，不重新定义核心生命周期与安全模式。若两份文档出现冲突，核心安全不变量以生命周期文档为准，插件运行形式和开发接口以本文为准。

## 3. 插件接口与契约

### 3.1 两层接口

[`lifecycle-hooks-and-security-audit.md`](lifecycle-hooks-and-security-audit.md) 先完成进程内 internal Hook API 和 fake consumer 验收。插件层只在这些接口稳定后定义外部协议，不能一边重构核心生命周期，一边把临时 Go 类型公开给脚本。

外部插件不建议直接使用 Go `plugin`：

- Windows 不支持；
- Go 版本和依赖 ABI 约束严格；
- 崩溃隔离差；
- 难以实施资源和 capability 边界。

外部插件可以有多种运行时，但不能为每种运行时设计一套不同的 Hook 语义。`goja`、受控子进程和 WASM 都应消费同一版本化 Lifecycle wire event，并返回同一版本化结果。运行时只决定隔离、资源管理和开发体验，不决定插件能看到什么数据或能否覆盖系统 Guard。

### 3.2 Capability

建议初始 capability：

```text
lifecycle.observe.redacted
message.read
message.transform
model.request.read
model.request.transform
model.response.read
model.response.transform
tool.metadata.read
tool.args.read
tool.guard
tool.result.read
tool.result.transform
tool.register
action.enqueue
plugin.storage
```

原则：

- `observe.redacted` 默认不包含原文；
- read 和 transform 分开授权；
- tool metadata 和 args 分开授权；
- `tool.guard` 只能收紧，不能覆盖系统 deny；
- `tool.register` 注册的工具自动进入 ToolRuntime；
- `action.enqueue` 只能创建新 command，不能在 Hook 内重入执行；
- `plugin.storage` 只提供按 plugin ID 和 SessionGroup 隔离、有大小上限的 KV，不提供数据库句柄；
- capability 按 SessionGroup 和 principal policy 进一步过滤。

### 3.3 Scope

插件注册必须绑定范围：

```text
global
  -> SessionGroup allowlist
      -> conversation allowlist
          -> principal / source filters
              -> Mind or selected Hand nodes
```

SessionGroup 是最低必要隔离边界。当前 Skill 仍为全局 Store，因此插件系统开放前应先完成 Skill -> SessionGroup 集成，避免 lifecycle 已按工作区过滤而 Skill 继续全局暴露。

### 3.4 插件 manifest 示例

```toml
[plugin]
name = "secret-redactor"
version = "1.0.0"
api_version = 1
runtime = "goja"
entry = "index.js"

capabilities = [
  "lifecycle.observe.redacted",
  "tool.result.transform",
]

[[plugin.hooks]]
phase = "tool.result_before_commit"
order = 100

[plugin.scope]
session_groups = ["group-id"]
```

`timeout`、是否为强制安全插件和失败策略属于宿主 policy，不应由插件作者自行声明。否则恶意插件可以把本应 fail closed 的安全 Hook 声明为 fail open。manifest 只是插件的能力申请和静态元数据，不代表当前已经确定磁盘格式。

### 3.5 资源和健康管理

插件运行时至少需要：

- 每 Hook timeout；
- 并发上限；
- 输入输出大小上限；
- panic/process crash 隔离；
- 连续失败熔断；
- observer 有界队列；
- 健康状态和最近错误查询；
- EventID 去重；
- 禁止未授权网络、文件和工具访问；
- 卸载时等待或取消 in-flight Hook 的明确语义。

插件日志不能进入用户 conversation history，除非插件通过正式 action 提交用户可见消息。

## 4. 运行时选型结论

`goja + JavaScript` 适合作为 Half-Pi 的**第一种开发者插件运行时**，但不应成为唯一运行时，也不能被描述为不可信代码的安全沙箱。

推荐把插件分成以下层级：

| 层级 | 运行形式 | 适用对象 | 隔离能力 | 建议定位 |
|---|---|---|---|---|
| T0 | 编译进 Mind/Hand 的 Go 组件 | 核心 Guard、AI Reviewer adapter、Auditor、协议投影 | 与宿主同进程，完全可信 | 内置组件，不作为第三方插件发布 |
| T1 | `goja` 执行 JavaScript | 用户自己编写或明确安装并信任的轻量插件 | 语言 API 隔离，没有进程隔离 | 首版插件运行时 |
| T2 | 独立子进程 + 版本化 RPC | 需要原生依赖、网络集成或长任务的受信插件 | 崩溃和部分资源隔离；同用户权限下不是完整安全沙箱 | 扩展集成运行时 |
| T3 | WASM，优先评估 `wazero` | 来源不完全可信、主要做纯计算和 Hook 处理的插件 | 默认无文件/网络能力，可限制线性内存和导入 | 稳定协议后的强隔离运行时 |

这里不存在一个在所有维度都“更好”的单一方案：

- 只追求开发体验、跨平台和快速启动时，`goja` 最合适；
- 需要复用任意语言或原生 SDK 时，子进程最合适；
- 需要把第三方代码当作不可信输入时，WASM 比 `goja` 更合适；
- 核心安全策略、审批状态机和事务 Auditor 不应使用任何动态插件运行时。

因此推荐的总体方案不是“在 `goja` 和 WASM 之间二选一”，而是固定一个与运行时无关的插件协议，首版实现 `goja`，以后按真实需求增加 process/WASM adapter。

## 5. 为什么 Goja 适合作为首版

建议依赖 `github.com/dop251/goja`。它与 Half-Pi 当前架构有几个直接匹配点：

- 纯 Go 实现，不要求 CGO，符合 Mind 的 Linux、Windows、macOS 多平台目标；
- 启动快，适合短小的同步 Guard、Transformer 和 Observer；
- JavaScript 学习和分发门槛低，插件可以是 manifest 加少量 `.js` 文件；
- 不自带 Node.js 的 `fs`、`net`、`child_process` 等能力，宿主可以只暴露显式 capability API；
- JavaScript 值可以通过稳定 JSON schema 与 Lifecycle event 对接，不需要暴露 Go ABI；
- 相比独立进程，事件传递和小型 Transformer 的固定开销更低；
- 相比 WASM，开发、调试和热重载体验更直接。

但 Goja 的限制同样需要写进产品安全边界：

1. **它不是 OS 沙箱。** JavaScript 与 Mind 运行在同一进程；Goja 或宿主桥接代码的 panic、内存压力和实现缺陷会影响 Mind。
2. **它没有硬内存隔离。** 可以限制输入、输出和插件持久化状态大小，但很难像独立进程或 WASM 线性内存一样给单个插件设置可靠的硬上限。
3. **超时中断不是完整资源治理。** `Runtime.Interrupt` 可以中断持续执行的 JavaScript，但不能安全抢占一个正在阻塞的 Go host callback。
4. **Runtime 不能并发使用。** 每个 `goja.Runtime` 必须由单一 worker goroutine 串行拥有，不能被多个 Chat 并发调用。
5. **宿主暴露什么，插件就拥有什么。** 一旦把 `Store`、`executor.Runner`、文件句柄、HTTP client 或任意 Go 对象直接传给脚本，capability 边界就失效。
6. **没有 Node.js 生态兼容承诺。** 首版不应承诺 npm、CommonJS、ES module、timer 或 Node 内建模块兼容，否则插件宿主会迅速演变成另一个不完整的 Node runtime。

因此安装 Goja 插件时，UI 和 CLI 应明确显示“进程内可信插件”。不能仅因为脚本看不到默认文件 API，就向用户声称它可以安全执行任意第三方代码。

## 6. Goja 宿主模型

每个已启用插件使用一个独立 `PluginWorker`：

```text
Lifecycle Router
  -> capability/scope filter
  -> immutable wire payload
  -> bounded plugin mailbox
  -> PluginWorker goroutine
       -> owns exactly one goja.Runtime
       -> invokes registered JS function serially
       -> validates structured result
  -> Lifecycle Router merges outcome
```

约束如下：

- 一个 Runtime 只属于一个插件，插件之间不共享 global object；
- 一个 Runtime 只由一个 goroutine 调用；
- 并发 Chat 对同一同步 Guard/Transformer 排队执行，队列必须有界；
- Observer 使用独立有界队列，队列满时记录 dropped metric，不反压 Chat；
- Guard/Transformer 队列满按宿主 policy 处理，强制安全 Hook 默认 fail closed；
- 插件初始化、Hook 调用、卸载分别设置 timeout；
- JavaScript timeout 时调用 interrupt，当前 Runtime 随后销毁并按策略重建，不能假设中断后的全局状态仍然可靠；
- 所有 Go -> JS 和 JS -> Go 边界都必须 `recover`，panic 转成结构化 plugin failure；
- 插件被禁用或重载时，先停止接收新事件，再取消或等待有界的 in-flight 调用；
- 不允许 Hook 同步调用 ChatRuntime、ToolRuntime 或自己，避免死锁和递归执行；
- 插件若要触发动作，只能使用 `action.enqueue` 提交 child command，由宿主在当前 Hook 返回后走完整 admission 流程。

Goja 首版采用同步函数模型。Observer 的异步性由宿主队列提供，不依赖 JavaScript event loop。首版不开放 `setTimeout`、任意 Promise 调度或自定义 goroutine bridge。

Goja 依赖只应出现在 `half-pi-mind/internal/plugin/goja`，不能加入保持零外部依赖的 `half-pi-core`。`half-pi-core` 只定义与运行时无关的 lifecycle 和 plugin wire 类型。

首版 JavaScript 插件也只运行在 Mind。不要把插件源码通过 RPC 自动下发给 Hand，也不要让 Mind 插件注册 Hand 本地原生能力。插件工具若要执行远端操作，仍通过既有 RemoteRun 和 Hand Authorizer。未来若确有 Hand 本地插件需求，应单独设计 node-local 安装、签名、allowlist、版本和审计，不能继承 Mind 的“已安装”状态。

## 7. JavaScript API 形状

首版可以避免模块加载器，入口脚本通过宿主注入的 `halfpi.register()` 注册：

```javascript
halfpi.register({
  apiVersion: 1,
  hooks: {
    "tool.before_execute": function (event) {
      if (event.tool === "write_file" && event.riskLabels.includes("outside_workspace")) {
        return {
          decision: "require_user",
          reasonCode: "outside_workspace"
        };
      }
      return { decision: "abstain" };
    },

    "tool.finished": function (event) {
      halfpi.log("info", "tool finished: " + event.tool);
      return null;
    }
  }
});
```

这只是 API 形状示例。实际协议需要满足：

- 传入对象由版本化 wire schema 生成，不是 Go struct 或 Go pointer 的动态代理；
- 每次调用获得新的数据副本，JavaScript 修改输入对象不能修改宿主状态；
- freeze 之后的 event 不接受参数替换结果；
- Guard 插件只返回 `abstain`、`require_user` 或 `deny`，不提供能够覆盖系统结果的 `allow`；
- Transformer 只返回 `unchanged` 或符合目标 phase schema 的 replacement；
- Observer 的返回值被忽略；
- 返回值必须经过 schema、UTF-8、深度、字段数和字节数校验；
- 插件提供的 reason code 使用 `plugin.<plugin_id>.<code>` 命名空间，不能伪造 core reason；
- EventID、TraceID、PrincipalID、GroupID、NodeID 和 source 均由宿主签发，插件不能覆盖；
- 插件日志经过长度和控制字符过滤，并附带 plugin ID；
- raw 字段在对象创建前就按 capability 删除，不能先传给 JavaScript 再要求脚本不要读取。

首版建议只暴露极小的宿主 API：

```text
halfpi.register(definition)
halfpi.log(level, message)
halfpi.action.enqueue(command)    # 需要 action.enqueue
halfpi.kv.get(key)                # 需要 plugin.storage
halfpi.kv.set(key, value)         # 需要 plugin.storage
```

不应暴露：

- 任意文件读写；
- 任意网络请求；
- 启动进程或 shell；
- SQLite connection、Store transaction；
- `executor.Runner`、ToolRuntime 或 Approval Broker；
- EventBus publisher；
- `context.Context`、channel、mutex 或其他 Go runtime 对象；
- 从脚本动态加载任意宿主路径的 `require()`。

如果未来确实需要文件或网络能力，应新增窄 capability，例如工作区内只读文件或限定 host 的 HTTP client，并由 Go 包装器重新做 scope、大小、timeout 和审计。不能直接补一个通用 `os` 或 `fetch`。

## 8. 不同通道的失败语义

运行时不能自行决定失败是否阻止主流程。宿主根据 Hook 类型和管理员安装策略决定：

| 插件用途 | timeout、panic、schema error 时的行为 |
|---|---|
| 普通 Observer | fail open，丢弃本次通知并计数；连续失败后熔断 |
| 可选的展示型 Transformer | fail open，使用变换前数据并记录错误 |
| 强制脱敏 Transformer | fail closed，不交付未审查数据 |
| 强制 Guard | fail closed；通常升级为 `require_user`，明确系统禁止项可 `deny` |
| 插件注册的 Tool | 返回规范化 tool failure，不自动重试有副作用调用 |

manifest 可以声明它希望注册的 Hook 类型，但是否把某插件设为“强制”必须由本地管理员 policy 确认并写入管理审计。插件升级后，如果 capability、entry digest 或 Hook 集合变化，强制状态应重新确认。

## 9. 子进程运行时的边界

子进程运行时建议使用 stdin/stdout 上的长度前缀 JSON RPC，stderr 专用于插件日志。协议至少包括：

- protocol hello 和 API version 协商；
- manifest digest、plugin instance ID 和宿主 nonce；
- hook invoke/cancel/result；
- EventID 去重；
- health/ping/shutdown；
- 最大 frame 大小和每次调用 deadline；
- 不接受插件自行提供的 trace identity 或 capability。

子进程的优点是崩溃不会直接让 Mind panic，可以单独终止、重启并统计资源。它也能让插件使用 Python、Rust、Node.js 或厂商 SDK。

但“进程隔离”不等于“权限隔离”。一个以当前用户身份运行的原生插件仍可能直接读取该用户有权访问的文件、打开网络连接或启动其他进程。若要把子进程用于不可信插件，还需要平台级沙箱：

- Linux 可评估 namespace、seccomp、cgroup 或容器；
- Windows 使用 Job Object 管理进程树和资源，但文件/网络权限还需要额外隔离；
- macOS 需要单独评估发布环境可用的 sandbox 机制。

这些平台机制复杂且行为不完全一致。因此首版 process runtime 应标记为“受信原生插件”，它主要提供故障隔离和语言自由，而不是承诺跨平台强安全沙箱。

## 10. WASM 运行时的边界

如果 Half-Pi 要支持来源不完全可信的第三方插件，建议在 wire contract 稳定后评估 `wazero`：

- 纯 Go，跨平台且不依赖系统级 WASM runtime；
- 默认不启用 WASI 时，模块没有文件、网络、环境变量和进程能力；
- 宿主只导入 capability 对应的窄函数；
- 可以限制线性内存、输入输出大小和执行 deadline；
- 模块崩溃或 trap 能与 Mind 的 Go 控制流隔离。

代价是：

- 插件 ABI、内存布局和字符串/JSON 传递需要专门 SDK；
- 调试、热重载和错误堆栈体验弱于 JavaScript；
- 复杂异步 I/O 和宿主回调设计成本更高；
- 不同语言编译到 WASM 后的体积、运行库和行为不一致；
- WASM 只能限制模块本身，宿主导入函数仍必须做完整 capability、scope、timeout 和审计。

WASM 适合 Guard、Transformer、解析器和小型纯计算工具。需要浏览器自动化、数据库驱动或完整厂商 SDK 的插件，更适合明确受信的子进程。

## 11. 与 AI Reviewer 和安全模式的关系

`AIReviewerGuard` 是 T0 内置安全组件，不是普通插件：

- 它的 prompt、provider、模型、schema 和失败降级由安全 policy 固定；
- 它可以在审查投影规则允许时读取 Frozen Action 的必要真实参数；
- 它只能返回 `allow` 或 `require_user`；
- 它的 `allow` 也不能覆盖 deterministic Guard、`DefaultConfirm`、显式 `confirm: true` 或 Hand deny；
- 它的调用和结果进入 `security_decisions`，不能只写普通插件日志。

外部 `tool.guard` 插件则只能收紧：`abstain`、`require_user` 或 `deny`。不能允许某个 Goja 插件冒充 Reviewer 返回 `allow`，也不能允许插件注册一个同名 reviewer 来替换内置实现。

插件可以在 freeze 前根据自己的 capability 添加风险标签或请求用户审批，但所有修改后的 Action 仍要重新经过 schema validation、freeze、digest 和 deterministic Guard。Reviewer 不默认看到插件私有状态，插件也不默认看到 Reviewer 的完整输入和模型原始输出。

## 12. 推荐实施顺序

插件部分建议按以下顺序实现：

1. 确认生命周期文档的插件就绪验收已经通过，冻结首版 wire fixture；
2. 完成 manifest、capability、scope、安装审计和 plugin health 模型；
3. 实现 Goja Observer，只开放 `lifecycle.observe.redacted` 和日志；
4. 验证 worker 串行化、timeout、interrupt、panic、队列、熔断和热重载；
5. 开放 restrictive Guard 和少量 Transformer，并根据 phase 接入 buffered streaming；
6. 增加 namespaced KV 和 `action.enqueue`；
7. 最后开放 `tool.register`，所有插件工具强制进入 ToolRuntime；
8. wire API 稳定后，根据真实插件需求决定先实现 process adapter 还是 WASM adapter。

如果首批插件主要是用户自己写的审计、格式化和工作流脚本，Goja 优先。如果首批目标是安装公共市场中的未知插件，则应把 WASM 原型前置，不能先用 Goja 承诺不可信插件隔离。

## 13. 运行时专项验收

Goja runtime 至少需要以下测试：

- 无限循环能在 deadline 后中断，且该 Runtime 不再复用；
- panic、throw、非法返回值和超大返回值不会让 Mind 崩溃；
- 同一 Runtime 永不被并发调用；
- 慢 Observer 队列满不阻塞 Chat；
- 强制 Guard timeout 不会静默放行；
- 未授权插件对象中不存在 raw message、raw args 和 result；
- 插件修改输入对象不能修改 Frozen Action；
- 直接和间接重入 Chat/ToolRuntime 均被拒绝；
- 插件升级扩大 capability 时需要重新授权；
- disable/reload 与 in-flight Hook 竞争只有一个明确终态；
- Windows、Linux 和 macOS 的 interrupt、取消和路径加载行为一致。

如果实现 process/WASM，还要分别验证：

- 子进程退出、协议乱序、超大 frame、stdout 污染和进程树取消；
- WASM memory 上限、trap、deadline、非法 host call 和未授权 WASI 能力；
- 三种运行时对同一 wire fixture 产生相同的 Guard/Transformer 语义。

## 14. 其他候选方案

以下方案可以解决部分问题，但不建议替代上述分层方案：

| 方案 | 优点 | 主要问题 | Half-Pi 建议 |
|---|---|---|---|
| Go 标准库 `plugin` | Go 类型调用直接、性能高 | Windows 不支持，Go/依赖 ABI 强绑定，无法良好隔离崩溃 | 不采用 |
| Yaegi 等 Go 解释器 | 插件作者可继续写 Go | 仍在进程内，宿主 API 暴露风险高，语言/依赖兼容面大 | 不作为公共插件 ABI |
| Lua / `gopher-lua` | 小巧、嵌入成熟 | 同样没有进程隔离，对本项目目标用户的开发体验没有明显胜过 JavaScript | 无现有 Lua 生态需求时不采用 |
| Starlark | 语义受控、适合确定性配置和规则 | 语言和库生态较小，不适合复杂集成、工具 SDK 和通用插件 | 可作为未来策略脚本候选，不替代通用插件运行时 |
| CEL/Rego/自定义 DSL | 容易限制为纯表达式，适合可审计安全规则 | 不是通用插件，难以承载 Observer、工具和工作流 | 简单 Guard 优先考虑，核心 deny 规则仍用确定性 Go/policy data |
| QuickJS/V8 绑定 | JavaScript 兼容性或性能更强 | CGO/原生库、交叉编译、打包和漏洞维护成本更高 | 当前跨平台目标下不优先 |
| 仅使用 WASM | 隔离边界清晰、语言可选 | ABI/SDK、调试和异步集成成本较高 | 若产品一开始就是公共插件市场，可改为首选 |

特别需要区分“策略”和“插件”：

- 硬拒绝、scope、ownership、参数边界和审批不变量应继续使用 Go 与数据驱动的确定性 policy；
- 用户可配置的简单条件可以使用声明式 rule/CEL 类表达式；
- 需要状态、格式化、观察、工作流或工具扩展时才使用 Goja 插件；
- 来源不明且需要执行代码时，使用 WASM 或不安装。

如果未来 Half-Pi 的插件主要变成“几条审批规则”，Starlark/CEL 会比 JavaScript 更合适。如果目标是面向开发者编写完整扩展、注册工具并消费生命周期，Goja 的综合体验更好。

## 15. 建议模块结构

```text
modules/half-pi-core/
├── lifecycle/              # Meta、Phase、wire event 和 Hook 结果
└── executor/               # FrozenInvocation、Authorizer、ToolRuntime

modules/half-pi-mind/internal/
├── lifecycle/              # registry、scope、ordering、redacted view
├── plugin/
│   ├── manifest/           # manifest 解析、schema version 和 digest
│   ├── capability/         # 能力授权与 SessionGroup scope
│   ├── host/               # worker、队列、health、熔断和重载
│   ├── goja/               # 首版可信进程内 JavaScript runtime
│   ├── process/            # 后续受控子进程 adapter
│   └── wasm/               # 后续 wazero adapter
└── management/             # 安装、启用、禁用、授权和管理审计入口
```

Goja、process 或 wazero 依赖不能进入保持零外部依赖的 `half-pi-core`。Core 只提供稳定类型和接口，具体运行时留在 Mind internal。首版不在 Hand 安装或同步插件。

## 16. 完成标准

插件架构完成不能只以“JavaScript 能执行”为准，还必须满足：

- 插件只能订阅其 manifest 申请且管理员实际授予的 phase 和 capability；
- scope 在 payload 构造前执行，插件无法读取其他 SessionGroup 或 conversation 数据；
- 普通插件默认只获得 redacted view；
- 外部 Guard 只能收紧系统决定，不能覆盖 core deny、AI Reviewer、用户强制审批或 Hand deny；
- 插件工具和异步 action 重新进入正式 ToolRuntime 与 admission 路径；
- Goja timeout、panic、busy loop、禁用和重载具有确定行为；
- Observer 故障不阻塞 Chat，强制安全 Hook 故障不会静默放行；
- manifest 或 capability 扩大时需要重新授权并写管理审计；
- 运行时 API 有 schema version，不暴露 Mind internal Go 类型；
- 如果允许未知来源插件，必须提供与威胁模型匹配的 WASM 或平台沙箱，不能依赖 Goja 的 API 缩减冒充隔离；
- 五模块 race 测试和相关真实进程 E2E 保持通过。
