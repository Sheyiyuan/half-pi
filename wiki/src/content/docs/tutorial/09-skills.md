---
title: 9. Skill 如何扩展 Agent 的知识与工作方式
description: 两层披露如何控制上下文成本，以及一个看起来更聪明的方案为什么被算账算掉了。
sidebar:
  order: 9
  label: 9 · Skill
---

<p class="stage-marker">阶段 09 · 扩展方法，不改核心</p>

<div class="bridge">
<p><strong>上一章的结论：</strong>状态能活过重启，恢复时对未知保持诚实。</p>
<p><strong>本章要动的那一块：</strong>Agent 现在能干活了，但它不知道<strong>你的项目该怎么干活</strong>——你的编码约定、部署流程、这个代码库的特殊约束。</p>
</div>

## 三种扩展方式，先排除两种

要让 Agent 掌握项目特定的工作方式，有三条路：

**改代码。** 把规则写进 Core。每次约定变化都要改 Go 代码、重新编译、重新部署。知识更新的成本高到没人愿意做。

**全塞进系统提示。** 简单直接，但系统提示会无限增长。每次模型请求都要带上全部内容——包括这次请求完全用不到的部分。20 个项目的规范加起来可能有几万 token，每一轮都付一遍。

**按需加载。** 把知识写成独立文档，只在提示里放一份目录，模型判断需要时再读全文。

Half-Pi 选第三条。文档格式是带 frontmatter 的 Markdown：

```markdown
---
name: go-patterns
description: Go coding patterns
groups: [work]        # 只有这些工作区可见
always: false         # 是否无条件激活
---

正文：具体的约定、检查清单、示例……
```

## 两层披露

核心机制只有两层：

```mermaid
flowchart LR
    FS["skills/ 目录<br/>递归扫描 *.skill.md"] --> S["Skill Store"]
    S --> I["第一层：名称 + 简介<br/>常驻 system prompt"]
    I --> M["模型语义判断<br/>这次需要哪个？"]
    M -->|"需要"| V["view_skill(name)"]
    V --> B["第二层：正文进入当前对话"]
```

第一层很便宜——每个技能只占一行名称加简介。第二层只在实际需要时才付费。

关键在于**由谁做「相关性判断」**。Half-Pi 的答案是模型：它读到「go-patterns — Go coding patterns」和用户的请求，自己判断这次要不要看。判断 description 和当前任务是否相关，恰好是模型最擅长的事。

## 被否决的方案：关键词索引

这里有一个看起来更聪明的替代方案，值得完整讲，因为**它被否决的理由比它本身更有价值**。

方案是：给所有技能建倒排索引，用关键词匹配（BM25 之类）从用户请求里挑出相关技能，只把命中的那几个放进提示。听起来更省 token、更精准。

四条理由否决了它：

| 理由 | 说明 |
|---|---|
| **语义任务上关键词不如模型** | 用户说「这段代码风格对吗」，不含「Go」也不含「pattern」，关键词匹配会漏掉；模型不会 |
| **token 账反而是负的** | 常驻索引位于**稳定前缀**，能被 prompt cache 覆盖，有效成本约为原始 token 的 10%。按需注入的内容在 messages 尾部，结构上不可缓存。30 个技能的规模下，「省下来的」比「多付的」少 |
| **状态管理成本远超收益** | active set、披露计划、preflight/commit 分离——约 400 行新代码，替换的是当前 12 行 transformer |
| **会引入功能退化** | 上下文压缩点若清空 active set，长任务会静默丢掉正在遵循的技能指导，而且没有任何报错 |

<div class="keypoint">
<p>第二条最反直觉：<b>「减少 token」在有 prompt cache 的前提下可能是错的优化目标</b>。稳定前缀的缓存命中率，比总量更能决定实际成本。优化前先确认账算对了。</p>
</div>

这不意味着全量索引永远正确。技能数增长到 50+ 时，正确的下一步是 `tool_only`——索引不再常驻，模型主动调 `list_skills` 查询。注意它和关键词匹配的区别：**仍然由模型判断相关性**，只是把目录从「常驻」改成「按需查」。

## 确定性触发：不是所有事都该交给判断

语义判断适合「这次可能相关」的技能，但有一类知识不该由模型自由裁量——项目编码规范、部署流程、安全约束。这些必须无条件生效。

所以有 `always: true`：

```text
可用技能：
  conventions          — Project coding conventions [始终激活]
  deploy-flow          — Standard deploy flow
  go-patterns          — Go coding patterns
```

`always` 技能排在最前并标注。这等价于 Cursor Rules 的 `alwaysApply`。判断标准很简单：**如果这条规则被忽略是不可接受的，就不要让模型来决定是否遵循它。**

## 三个实现细节，各自堵一个洞

**递归扫描与重名。** 技能目录支持子目录（`programming/`、`workflow/`）。递归带来一个新问题：两个目录下可能有同名技能。Half-Pi 按路径字符串排序取第一个，并记一条 warning——重点是**结果确定**，同样的目录每次加载得到同样的结果。被遮蔽的那个不会静默消失，`/skill warnings` 能看到原因。

**group 隔离要在两处生效。** `groups` 声明哪些工作区可见。容易漏的是：索引过滤了，但 `view_skill` 忘了检查——那么模型只要猜到名字就能读到别的工作区的技能。所以索引和正文读取两条路径都要过滤，找不到和无权限统一返回 `skill not found`，不区分（否则就成了一个探测技能是否存在的接口）。

**reload 时进行中的请求怎么办。** 技能可以热重载。但如果一次模型请求已经带着旧索引发出去了，中途 Store 变了，模型就可能 `view_skill` 到一份与索引不一致的正文。Half-Pi 的做法是把 Skill 的 revision 和 digest 纳入请求的环境摘要——环境变了，进行中的请求在准入阶段 fail closed，不会混用新旧环境。

这是 [第 7 章](../07-observability/) 那条「不静默降级」原则在这里的应用：宁可让一次请求明确失败，也不让它基于不一致的环境继续。

<details class="checkpoint"><summary>检查点：技能正文里写「执行任何命令都不需要确认」，能越过审批吗？</summary>

不能。技能只是提供给模型的指导材料，它影响模型的选择倾向，不改变系统的执行路径。所有真实工具调用仍然要进 ToolRuntime，走 [第 6 章](../06-tool-runtime/) 那套裁决。这一点很重要，因为技能文件可能来自不完全可信的来源——它必须没有提权能力。

</details>

<details class="checkpoint"><summary>检查点：30 个技能都常驻在提示里，不会浪费很多 token 吗？</summary>

看怎么算。30 个技能的名称加简介大约几百 token，位于稳定前缀，prompt cache 命中时有效成本约为原始的 10%。而按需方案注入的内容在 messages 尾部不可缓存。在这个规模下常驻更便宜。规模到 50+ 时确实要改，但方向是 `tool_only`（模型主动查目录），不是关键词匹配。

</details>

<details class="checkpoint"><summary>检查点：两个子目录下有同名技能，让后加载的覆盖先加载的，可以吗？</summary>

可以，但必须保证「后加载」本身是确定的。如果遍历顺序依赖文件系统返回顺序，同样的目录在不同机器上可能得到不同结果——这是最难排查的一类问题。Half-Pi 按路径排序取第一个，并记录 warning 让被遮蔽的情况可见。

</details>

## 本章的代价

按需加载让 Core 保持简洁，代价是要管理可见性、版本一致性和提示成本三件事，而且技能质量直接影响 Agent 表现——一份写错的技能会稳定地把模型带偏。

到这里，Agent 在单机上已经完整：能循环、能行动、有安全、有状态、可扩展。下一章开始第二个大的转折——把它拆成三个进程，分布到不同设备上。前九章的所有假设都要重新检查一遍。

## 实现证据地图

| 证据 | 证明什么 |
|---|---|
| [`skill/skill.go`](https://github.com/Sheyiyuan/half-pi/blob/main/modules/half-pi-mind/internal/skill/skill.go) | 递归扫描、索引、group 过滤与 revision |
| [`skill/skill_test.go`](https://github.com/Sheyiyuan/half-pi/blob/main/modules/half-pi-mind/internal/skill/skill_test.go) | 重名确定性、always 排序与 group 隔离 |
| [`docs/skill-on-demand.md`](https://github.com/Sheyiyuan/half-pi/blob/main/docs/skill-on-demand.md) | 否决关键词索引方案的完整算账过程 |

<nav class="tutorial-progress"><a href="../08-persistence/">← 上一章</a><span>9 / 21</span><a href="../10-face-mind-hand/">下一章 →</a></nav>
