---
title: 12. Hand 远程执行与设备侧最终守门
description: 一次跨越两个安全域的调用要被检查两遍——以及为什么「同名 Hand 重连」是个必须堵的攻击面。
sidebar:
  order: 12
  label: 12 · Hand 最终守门
---

<p class="stage-marker">阶段 12 · 跨越两个独立安全域</p>

<div class="bridge">
<p><strong>上一章的结论：</strong>握手能证明消息来自哪个已认证 peer，内容加密且防重放。</p>
<p><strong>本章要动的那一块：</strong>「来源可信」不等于「这次调用该执行」。<a href="../10-face-mind-hand/">第 10 章</a>说过 Hand 保留最终否决权，本章把这个承诺变成具体机制。</p>
</div>

## 两个安全域各回答什么

同一次 `read_file`，两边问的是不同的问题：

<div class="lenses" style="--cols:2">
<section>
<h3>Mind 侧问</h3>
<p>当前用户在这个 conversation 里，是否获准对 study-pc 发起这次带这些参数的调用？</p>
<p class="verdict">意图授权</p>
</section>
<section class="is-chosen">
<h3>Hand 侧问</h3>
<p>这台机器此刻是否愿意执行这个工具？工作目录允许吗？在 deny 列表里吗？本地策略同意吗？</p>
<p class="verdict">设备许可</p>
</section>
</div>

两个问题的答案互不推导。用户完全有权限发起请求，而那台机器完全可以配置成不接受这类操作。

## 走读一次完整的双重检查

```mermaid
flowchart LR
    M1["Mind ToolRuntime<br/>PrepareExternal"] --> R["RemoteRun + 外部摘要"]
    R --> RPC["加密 RPC"]
    RPC --> H1["Hand 校验绑定<br/>来源 / deadline / 审批证明"]
    H1 --> H2["Hand 自己的 ToolRuntime<br/>本地 Authorizer"]
    H2 --> OS["文件 / 进程"]
    OS --> RES["RPC result"] --> AUTH["Mind Authority 仲裁唯一终态"]
```

**Mind 侧：** `use_hand` 选定目标，创建 RemoteRun，走完 [第 6 章](../06-tool-runtime/) 的授权链。然后通过 `PrepareExternal` 冻结一份**外部执行契约**，生成一次性摘要，绑定：run ID、Hand ID、工具名、参数、是否后台、契约版本。

**Hand 侧：** 收到 RPC 后先校验绑定是否匹配、deadline 有没有过、审批证明是否有效。通过之后，**再进一次自己的 ToolRuntime**——用 Hand 自己的 Catalog、自己的 Authorizer、自己的安全策略。

注意这不是「又检查一遍相同的规则」。Hand 用的是完全独立的一套配置：它自己的 allow/deny 列表、自己的工作目录限制、自己的输出上限。Mind 的批准是**输入**，不是结论。

<div class="keypoint">
<p>Mind 的审批证明能让 Hand 相信「这次请求是经过授权的用户意图」，但不能让 Hand 跳过自己的裁决。<b>两侧的拒绝都是终局的。</b></p>
</div>

## 同名重连：一个必须堵的攻击面

这是本章最值得细看的地方，因为它是分布式系统里典型的「看起来没问题」。

场景：Mind 向 `study-pc` 发了一个 run，正在等结果。此时 study-pc 断线了。几秒后，一个新连接注册进来，label 同样是 `study-pc`。

它能不能回答那个正在等待的 run？

| 只按 Hand ID 匹配 | 后果 |
|---|---|
| 新连接声称自己是 study-pc | 它可以给上一个连接接纳的 run 返回任意结果 |
| 而 Mind 会把这个结果当权威终态 | 模型据此继续推理，用户看到一个伪造的执行结果 |

这条路径不需要攻破加密——**新连接可能是完全合法认证的**（比如凭据被盗用，或者就是同一台机器重启后的新进程，但它并不知道旧 run 的上下文）。

Half-Pi 的做法是在 Hand ID 之外再绑定**连接 generation**：结果的来源必须同时匹配「哪个 Hand」和「接纳这个 run 时的那一次连接」。替代连接无法继承旧连接接纳的调用。

那个 run 会怎样？按 [第 8 章](../08-persistence/) 的恢复规则标记为 `lost`——如实说明结果未知，不猜测、不重跑。

## 工具目录不一致时怎么办

Mind 和 Hand 各自持有自己的 Catalog（[第 4 章](../04-tools/) 提过这个后果）。如果 Mind 要求执行一个 Hand 上没有的工具呢？

**明确拒绝。** 不尝试下载实现、不降级到相似工具。「远程安装新能力」是一个权限高得多的操作，不能作为一次普通工具调用的隐式副作用发生。

## 取消一个命令，比想象中麻烦

`exec_command` 超时或被取消时，杀掉启动的那个 shell 进程是不够的——shell 可能已经派生了子进程，杀父进程会留下一堆孤儿继续跑，继续产生副作用。

必须终止**整棵进程树**，而这件事没有跨平台的统一做法：

| 平台 | 机制 |
|---|---|
| Unix | 把命令放进独立进程组，取消时杀整个进程组 |
| Windows | 用 Job Object 关联所有子进程，终止 Job |

按项目约定，这类差异用 build tag 加平台文件（`_unix.go` / `_windows.go`）在**编译期**决定，不用运行时 `if runtime.GOOS` 判断。理由是让不适用的系统 API 在编译期就被排除，而不是留在二进制里等着被误调用。

## 错误信息也要收敛

Hand 执行失败时，原始错误经常带着绝对路径（`/home/alice/secret-project/...`）、内部结构、甚至配置片段。这些直接回传给 Mind 再投影给 Face，就成了信息泄露——**Face 侧不应该知道那台机器的目录结构。**

所以结果在跨越边界时会收敛细节：保留足以让模型判断下一步的分类（不存在、无权限、超时），去掉不必要的内部信息。

<details class="checkpoint"><summary>检查点：Hand 上没有 read_file，Mind 能不能让它临时下载一个实现？</summary>

当前不能。工具目录不匹配就明确拒绝。「让远程节点获取并运行新代码」是权限最高的操作之一，如果它能作为普通工具调用的隐式后果发生，那么 Hand 的 allow/deny 列表就形同虚设——任何被拒绝的能力都可以先下载再执行。

</details>

<details class="checkpoint"><summary>检查点：同名 Hand 断线重连后，能不能给旧连接接纳的 run 回结果？</summary>

不能仅凭同名就接纳。Half-Pi 额外绑定连接 generation，确保替代连接无法继承旧连接的调用权利。旧 run 按恢复规则标记 `lost`。注意这个防护针对的不只是攻击者——一台重启后的机器重新连上来，它确实是「同一个 Hand」，但它对旧 run 一无所知，让它回结果同样是错的。

</details>

<details class="checkpoint"><summary>检查点：Mind 已经做过完整的安全裁决，Hand 再跑一遍 ToolRuntime 是不是重复劳动？</summary>

不是重复，因为两边的配置和策略是独立的。Hand 检查的是自己的 allow/deny、工作目录、本地安全模式——这些 Mind 根本不知道。如果省掉这一层，Hand 就退化成一个无条件执行远程指令的代理，装它等于把机器的控制权完全交给 Mind。

</details>

## 本章的代价

双重守门意味着同一次调用有两个可能的拒绝来源，用户需要能分清是哪一侧拒的、为什么。换来的是设备自主权：**Hand 可以装在你不完全控制的机器上。**

到这里，前台的短操作（读一个文件）已经闭环。但如果那个操作要跑十分钟呢？用户想看进度、想中途取消，网络还可能断。下一章处理长任务——那里的核心难点是「取消」和「结果」同时到达时该信谁。

## 实现证据地图

| 证据 | 证明什么 |
|---|---|
| [`hand/hand_exec.go`](https://github.com/Sheyiyuan/half-pi/blob/main/modules/half-pi-hand/internal/hand/hand_exec.go) | Hand 校验 RPC 绑定后进入本地 ToolRuntime |
| [`remoteexec/authority_test.go`](https://github.com/Sheyiyuan/half-pi/blob/main/modules/half-pi-mind/internal/remoteexec/authority_test.go) | 来源校验、connection generation 与终态路由 |
| [`docs/archive/remote-execution-closed-loop.md`](https://github.com/Sheyiyuan/half-pi/blob/main/docs/archive/remote-execution-closed-loop.md) | 双重守门与 RemoteRun 的决策背景 |

<nav class="tutorial-progress"><a href="../11-gateway-security/">← 上一章</a><span>12 / 21</span><a href="../13-remote-jobs/">下一章 →</a></nav>
