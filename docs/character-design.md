# 角色系统设计（v4）

> 状态：最终设计——待实现
> 更新：2026-05-21（第四次修订——LLM 作为导演操控角色，而非扮演角色）
>
> 本文记录 half-pi 角色系统的最终设计决策。

---

## 0. 设计哲学

**"情绪价值 >> 工程实用价值"**

half-pi 不是专业 CLI agent。它是陪伴你始终如一的灵魂群。这里没有冷冰冰的工具界面——这里有人和你说话、和你讨论、陪你写代码。

需要纯工具场景时，请用其他更专业化的 agent。half-pi 的价值不在于工程效率最大化，而在于让 CLI 里有一个你能记住、会想念的存在。

---

## 1. 核心架构：LLM 是导演，不是演员

这是 v4 最重要的设计决策变化。

### 1.1 旧模型的问题

v3 的设计是"LLM 扮演当前 soul"——system prompt 里写 "You are currently [zero]"，LLM 直接以零的第一人称输出文字。这导致了两个问题：

1. **模拟他人** — LLM 在扮演 zero 时，天然会写出"姐你来呗"这类替半拍说的话，因为它的训练数据里群聊就是这样的
2. **身份模糊** — system prompt 说"你是 zero"但对话历史里出现了[hanpai]的发言，LLM 在多个身份间切换时容易混淆
3. **格式漂移** — LLM 直接输出文字时，`[name]` 标签的格式无法程序保证，模型经常串格式

### 1.2 新模型

**LLM 不扮演任何 soul。LLM 是一个对话导演（director），通过工具操控 soul 发言。**

```
LLM（导演）
  ├── speak(soul="zero", text="数据流没问题")
  ├── speak(soul="hanpai", text="我同意但更担心写扩散")
  └── run_tool("bash", cmd="grep ...")
```

效果：
- LLM 从不直接输出文字。所有输出都通过 `speak(soul, text)` 工具
- soul 的归属是明确的——工具的参数决定了谁在说话
- LLM 不会再"替别人说话"，因为要替别人说话也得调 `speak` 工具
- 程序可以完美控制 `[name]` 标签的输出，不存在模型格式漂移

### 1.3 对话示例（从 LLM 视角看）

实际执行时 LLM 看到的是：

```
[system] 群组成员: zero（佐洛）, hanpai（半拍）
[system] 你是一个对话导演。通过 speak(soul, text) 让灵魂发言。
[system] 永远不要直接输出文字——所有发言必须通过 speak 工具。

[user] 这个架构怎么样？半拍也说说看法。

→ LLM 调用 speak(soul="zero", text="数据流没问题，但缓存层太薄了……")
→ LLM 调用 speak(soul="hanpai", text="我同意，但写扩散的问题也不小")
```

用户看到的是：

```
[zero] 数据流没问题，但缓存层太薄了……
[hanpai] 我同意，但写扩散的问题也不小
```

### 1.4 对比

| | v3（LLM 扮演灵魂） | v4（LLM 导演） |
|---|---|---|
| LLM 输出方式 | 直接输出文字 | 通过 `speak()` 工具 |
| 灵魂归属 | 由 system prompt 声明"你是谁" | 由 `speak` 参数明确指定 |
| 模拟他人 | 容易——直接写"姐你来呗" | 不可能——必须调 `speak` 工具 |
| 灵魂切换 | 隐式——LLM 在文本里切换人称 | 显式——导演调 `speak` 切换参数 |
| 标签控制 | 依赖 LLM 格式 | 程序完全控制 |
| 用户 @ 谁 | LLM 读到输入后自然调 speak | LLM 读到输入后自然调 speak |

---

## 2. 工具集

### 2.1 speak(soul, text)

```
speak(soul: string, text: string)
```

让指定的 soul 说一句话。这是 LLM（导演）输出文本的**唯一方式**。

- `soul` — soul 的名称（如 `zero`、`hanpai`），必须在群组成员中
- `text` — 该 soul 要说的话。不要包含 `[name]` 标签——程序自动加

调用 `speak` 时，程序切换当前活跃 soul 为目标 soul，并递增连续发言计数器。
连续 N 次（默认 3）`speak` 后工具拒绝，强制等待用户输入。

导演直接在 speak 里切换 soul：

```
speak(soul="zero", text="数据流没问题")
speak(soul="hanpai", text="我同意")
speak(soul="zero", text="那就双写")
→ 第 3 次，暂停，等用户
```

### 2.2 发言顺序示例

```
User: 这个架构怎么样？

→ speak(soul="zero", text="数据流没问题，但缓存层太薄了")
→ speak(soul="hanpai", text="我同意，写扩散也不小")
→ speak(soul="zero", text="可以双写——热点走缓存，冷数据直读")
→ (工具层检测到连续 3 次 speak，拒绝)
等待用户输入

User: 你们两个细化一下
→ (计数器重置)
→ speak(soul="zero", text="行，那我先画数据流")
→ speak(soul="hanpai", text="我来配合写缓存策略")
```

---

## 3. 目录结构

```
~/.half-pi/
├── core.SOUL.md           ← 底层承诺（极薄，所有 soul 共享）
├── souls/                 ← 灵魂定义目录
│   ├── zero/              ← 角色：佐洛
│   │   ├── identity.md    ← 我是谁（名字、性格、场景、边界）
│   │   └── memory/        ← 佐洛自己的记忆
│   ├── hanpai/            ← 角色：半拍
│   │   ├── identity.md
│   │   └── memory/
├── groups/                ← 群组配置
│   └── daily.json         ← 示例：日常群组 {zero, hanpai}
├── shared/                ← 跨角色共享记忆（可选）
├── skills/                ← 技能（所有 soul 可用，角色无关）
└── config.jsonc           ← 配置（API key、默认群组、默认调度规则）
```

### 3.1 核心层（core.SOUL.md）

定义所有 soul 共享的底层承诺。极薄，2~3 句话：

> 你是 half-pi 体系中的一个灵魂。你始终以用户的利益为先。你的记忆属于你自己。你可以通过 shared/ 感知其他灵魂留下的痕迹。

### 3.2 灵魂层（souls/<name>/）

每个 soul 是一个**完整的人**。identity.md 是灵魂的"角色卡"——导演通过读这些卡来了解每个角色该怎么演。

### 3.3 群组层（groups/<name>.json）

```json
{
  "name": "日常",
  "souls": ["zero", "hanpai"],
  "dispatch": "default",
  "default_soul": "zero"
}
```

---

## 4. 发言标签

`[name]` 标签由显示层自动输出，不由 LLM 生成。

流程：speak 工具被调用 → AgentSession 记录当前 soul → 显示层在 text_delta 前打印 `[name]` → 工具返回的 text 流式输出。

---

## 5. 工具执行

half-pi 的定位是"情绪价值优先"——工具执行如果污染了对话上下文，就本末倒置了。所以在 `speak` 之外的工具（bash/read/edit/write 等）应该由 subagent 隔离执行。这是 Phase 3 的内容。

---

## 6. 已确定的设计决策

- **LLM 是导演，不是演员**。所有 soul 发言通过 `speak` 工具。
- **发言者标签由程序自动输出**。`[zero]` `[hanpai]` 不由模型生成。
- **每个 soul 是一个完整的人**。有自己的 identity 和 memory。
- **群聊模型**。所有 soul 在同一上下文中。
- **所有 soul 彼此可见 identity**。导演需要知道角色设定。
- **N 轮上限**。连续 N 次 `speak` 后等待用户输入。
- **用户始终可以打断**。
- **"情绪价值 >> 工程实用价值"** 是顶层哲学。
