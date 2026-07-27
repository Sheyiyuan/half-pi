---
title: 4. 为什么 Agent 需要工具
description: 拆解一个工具定义的每个字段，对比开放 shell 与结构化工具的取舍，并区分模型看到的和系统持有的。
sidebar:
  order: 4
  label: 4 · 工具能力
---

<p class="stage-marker">阶段 04 · 给推理接上受控动作</p>

<div class="bridge">
<p><strong>前三章的结论：</strong>循环能把结果送回模型，供应商差异被挡在适配层外。</p>
<p><strong>本章要动的那一块：</strong>之前一直含糊地说「执行工具」。现在正面回答——这个接口长什么样，为什么不是直接给模型一个 shell。</p>
</div>

## 先看被否决的方案：直接开放 shell

最省事的做法是给模型一个「执行任意命令」的能力，让它自己拼命令行。这确实能干活，但有四个问题：

**一，参数无法校验。** 命令是一个字符串。系统在执行前无法知道模型想读哪个文件、想不想删东西，只能看到 `sh -c "..."`。

**二，安全策略无处落脚。** 想拦住「读取 SSH 私钥」，就得去解析 shell 语法——管道、变量展开、引号嵌套、`$(...)`。这条路上没有能站得住的检查点。

**三，结果不分层。** 命令的退出码、stdout、stderr 混成一团，模型难以稳定判断成败。

**四，模型的负担更重。** 它得同时记住命令名、参数顺序、各平台差异。工具描述反而能告诉它「这里需要一个绝对路径」。

<div class="lenses" style="--cols:2">
<section>
<h3>开放 shell</h3>
<p>能力上限最高，接入成本最低。但参数不可校验、安全策略无法定位、跨平台差异全暴露给模型。</p>
<p class="verdict">安全性最差</p>
</section>
<section class="is-chosen">
<h3>结构化工具</h3>
<p>每个动作有名称、参数 Schema 和明确结果。系统能在执行前检查具体参数。</p>
<p class="verdict">Half-Pi 的默认形态</p>
</section>
</div>

注意 Half-Pi 里**仍然有** `exec_command`——完全禁止命令执行会让 Agent 失去大量实用能力。区别在于它是一个有明确边界的工具：参数是结构化的，有专门的安全策略（黑名单、灰名单），执行时能设超时，取消时会杀掉整个进程树。它是「一个受管的危险工具」，不是「一个没有边界的后门」。

## 拆开一个工具定义

Half-Pi 里一个工具由这些部分组成：

```go
type Tool struct {
	Name           string
	Description    string
	Parameters     *ObjectSchema
	DefaultConfirm bool          // true 时每次调用都需用户确认
	OwnsConfirm    bool          // true 时 confirm 是工具参数，由工具自行审批
	Check          ToolCheck     // 执行前安全检查，nil 表示不检查
	PolicyCheck    ToolPolicyCheck
	Execute        func(ctx context.Context, args json.RawMessage) *ToolResult
}
```

这里最值得注意的是：**模型只能看到前三个字段。**

| 字段 | 模型看得到吗 | 作用 |
|---|---|---|
| `Name` / `Description` / `Parameters` | 看得到 | 模型据此决定用哪个工具、怎么填参数 |
| `DefaultConfirm` | 看不到 | 声明这个工具每次都要用户确认 |
| `Check` / `PolicyCheck` | 看不到 | 执行前的确定性安全检查 |
| `Execute` | 看不到 | 真正干活的实现 |

<div class="keypoint">
<p>模型看到的是<b>能力目录</b>，系统持有的是<b>能力目录加上一整套约束</b>。模型不知道、也不需要知道哪些调用会触发审批——它的建议权和系统的执行权是分开的。</p>
</div>

## 参数还带着一层「给谁看」的声明

有一个字段容易被忽略，但它体现了这套设计的细致程度。每个参数除了类型和描述，还要声明它在安全审查时如何暴露：

```go
const (
	ReviewInclude    ReviewExposure = "include"      // 传递字段值（非敏感字段的默认）
	ReviewRedact     ReviewExposure = "redact"       // 只传占位符，不传原值
	ReviewRequireUser ReviewExposure = "require_user" // 不给 Reviewer 看，强制升级到用户审批
)
```

为什么需要这个？因为 [第 6 章](../06-tool-runtime/) 会引入一个 AI Reviewer 来判断可疑操作。那个 Reviewer 也是一个模型，也会看到参数。如果某个参数本身就是敏感内容（比如要写入的文件正文），把它原样交给另一个模型审查就是在扩大暴露面。

所以工具作者必须为每个参数做一次决定：这个值可以给 Reviewer 看吗？不能的话，这次调用就直接升级到人类审批。**新增工具时这不是可选项**——项目约定要求每个参数都声明投影策略。

## 目录、Schema 与「模型看到的必须等于执行的」

工具通过注册表（Catalog）暴露。Core 从目录生成发给模型的定义；模型返回调用后，运行时再按名称找回实现。

这里藏着一个不明显的要求：**这两次查找必须命中同一个版本的定义。** 如果目录在中途被改动——比如另一个会话注册了一个同名工具——模型是按旧描述做决定的，执行的却是新实现。

所以 Half-Pi 的 Catalog 支持派生隔离视图和版本快照，而不是让所有会话、所有测试共享一个全局可变列表。测试要限制工具集时，做法是派生一个受限目录，而不是往全局表里增删。

```mermaid
flowchart LR
    CAT["Catalog<br/>名称 → 定义"] --> DEF["生成模型可见定义<br/>name + description + schema"]
    DEF --> M["模型选择并填参数"]
    M --> CALL["结构化调用"]
    CALL --> LOOK["按名称找回最终定义"]
    LOOK --> NORM["按 Schema 规范化参数"]
    NORM --> GATE["能不能执行？<br/>第 5、6 章"]
```

图里最后一个方框是本章故意留下的缺口。我们现在能找到实现、能校验参数形状了，但还没有回答任何关于「许可」的问题。

## Half-Pi 现有的工具分两层

通用能力放在共享模块 `half-pi-core/tools`，Mind 特有的放在 Mind 内部：

| 层 | 工具 | 为什么在这层 |
|---|---|---|
| 通用（core） | `read_file`、`write_file`、`edit_file`、`grep`、`grep_regex`、`list_files`、`exec_command` | Mind 和 Hand 都需要，共享实现避免两份代码漂移 |
| Mind 特有 | `view_skill`、`list_skills`、`list_hands`、`use_hand`、`select_hand`、任务查询与取消等 | 只有 Mind 有 Skill 库和 Hand 连接，Hand 上不存在这些概念 |

这个划分在 [第 12 章](../12-hand-guard/) 会有一个直接后果：Mind 和 Hand 各自持有自己的目录，两边的工具集**可能不一致**。Hand 上没有的工具，Mind 不能要求它执行。

<details class="checkpoint"><summary>检查点：把工具描述写得非常严格（「禁止读取任何密钥文件」），能代替参数校验吗？</summary>

不能。描述影响的是模型的选择倾向，Schema 和运行时检查约束的是系统的实际行为。模型的输出始终是不可信输入——它可能误解、可能被提示注入影响、也可能只是偶然生成了不符合描述的参数。约束必须落在系统这一侧。

</details>

<details class="checkpoint"><summary>检查点：所有工具注册进一个全局列表，是不是最简单？</summary>

初期最简单，但会在三个地方出问题：不同会话无法有不同工具集；测试之间会互相污染；Mind 和 Hand 的工具集无法区分。Half-Pi 保留了进程默认目录（方便 `init()` 自注册），同时支持派生受限视图，让需要隔离的场景有正规做法。

</details>

<details class="checkpoint"><summary>检查点：工具返回的错误信息，可以直接把底层异常原文给模型吗？</summary>

要看内容。错误需要能让模型判断下一步（所以要区分「文件不存在」和「权限不足」），但原始错误经常带着绝对路径、内部结构甚至配置片段。Half-Pi 对外投影时会收敛这类细节，[第 15 章](../15-face-protocol/) 会讲 Face 侧同样不接收原始内部错误。

</details>

## 本章的缺口

我们现在有了结构化、可描述、参数可校验的动作接口。模型能选工具，系统能找到实现。

但「找到了实现」和「获准执行」是两件完全不同的事。下一章专门拆穿这个最容易踩的捷径——而且会展示一个看起来很合理的补丁如何被绕过。

## 实现证据地图

| 证据 | 证明什么 |
|---|---|
| [`executor/types.go`](https://github.com/Sheyiyuan/half-pi/blob/main/modules/half-pi-core/executor/types.go) | Tool、Schema、确认标志与 Reviewer 参数投影契约 |
| [`executor/catalog_test.go`](https://github.com/Sheyiyuan/half-pi/blob/main/modules/half-pi-core/executor/catalog_test.go) | Catalog 隔离、拷贝和派生行为 |
| [`half-pi-core/tools/`](https://github.com/Sheyiyuan/half-pi/tree/main/modules/half-pi-core/tools) | 通用工具的实际实现与参数定义 |

<nav class="tutorial-progress"><a href="../03-model-providers/">← 上一章</a><span>4 / 21</span><a href="../05-registration-is-not-safety/">下一章 →</a></nav>
