# Quest & Timeline 系统设计

> 状态：设计中，待讨论
> 更新：2026-05-22

## 核心思路

角色扮演的深度不来自角色本身的性格设定（那是 identity.md 的事），而来自**角色知道自己身处什么叙事中**。

一张"剧本"告诉导演现在演到哪儿了——主线是什么、有哪些支线、刚才发生了什么。角色就能说出有上下文的话，而不是每轮对话都像从零开始。

---

## 1. 和现有系统的关系

```
System Prompt 构建顺序：
  core.SOUL.md       ← 底层承诺
  identity.md        ← 角色是谁
  quest & timeline   ← 🆕 现在在做什么（注入在这层）
  memory             ← 长期知识
  tools + guidelines
```

与 session 的区别：

| | Session（对话流水） | Quest & Timeline |
|---|---|---|
| 粒度 | 每一轮 LLM 交互 | 提炼后的叙事节点 |
| 维护者 | 程序自动（每轮保存） | Agent 主动（quest_update） |
| 数量 | 几十到几百条 | 5-20 条事件 |
| 用途 | 续聊回溯 | 叙事上下文 |
| 是否注入 prompt | 否（太大） | 是（轻量） |

---

## 2. 数据结构

```jsonc
// ~/.half-pi/sessions/{id}/quest.json
{
  "main": {
    "id": "main",
    "title": "优化 half-pi 角色系统",
    "status": "in_progress",      // pending | in_progress | done | blocked
    "progress": "讨论了 per-soul 记忆方案，决定先做 P0",
    "created": "2026-05-22T15:00:00Z",
    "updated": "2026-05-22T16:30:00Z"
  },
  "side": [
    {
      "id": "sq-1",
      "title": "讨论事件时间线方案",
      "status": "in_progress",
      "summary": "社老师提出用时间线+主支线优化角色扮演，正在写设计文档",
      "parent": "main",            // 可选：关联的主线
      "created": "2026-05-22T16:00:00Z"
    }
  ],
  "timeline": [
    { "time": "15:00", "type": "start",  "text": "开始优化角色扮演", "soul": null },
    { "time": "15:15", "type": "decision", "text": "决定先做 per-soul 记忆", "soul": "hanpai" },
    { "time": "16:00", "type": "idea",   "text": "提出事件时间线方案", "soul": "user" },
    { "time": "16:30", "type": "action", "text": "开始写 quest-timeline 设计文档", "soul": "zero" }
  ]
}
```

**字段说明：**

### 主线 / 支线
- `id`: 唯一标识。主线固定 `"main"`，支线用 `sq-{n}`
- `status`: `pending`（待开始）→ `in_progress`（进行中）→ `done`（完成）→ `blocked`（受阻）
- `progress`: 一句话描述当前进度，每次 `quest_update` 时更新
- `parent`: 支线关联的主线（可选）

### 事件类型
| type | 含义 | 举例 |
|------|------|------|
| `start` | 开始做某事 | "开始搭建 xx 服务" |
| `decision` | 做出决定 | "决定用 SQLite 而非 Postgres" |
| `action` | 完成一个操作 | "部署成功，API 在线" |
| `idea` | 提出新想法 | "可以加一个时间线系统" |
| `blocker` | 遇到阻碍 | "API key 过期，暂停工作" |
| `mood` | 情绪波动 | "今晚心情不好，先睡了" |
| `done` | 任务完成 | "P1 测试全部通过" |

---

## 3. 注入格式

构建 system prompt 时注入在 identity 之后、memory 之前：

```
## Quest Log

### 主线
[in_progress] 优化 half-pi 角色系统
  进度：讨论了 per-soul 记忆方案，决定先做 P0
  开始于：2026-05-22 15:00

### 支线
[in_progress] sq-1 讨论事件时间线方案
  进度：正在写设计文档
  关联主线：优化 half-pi 角色系统

### 时间线（最近 10 条）
  15:00  [开始]   开始优化角色扮演
  15:15  [hanpai] 决定先做 per-soul 记忆
  16:00  [user]   提出事件时间线方案
  16:30  [zero]   开始写 quest-timeline 设计文档
```

注入限制：
- 时间线最多展示最近 10 条（防 prompt 膨胀）
- 支线最多展示 5 条
- 如果没有任何任务，整段省略

---

## 4. 谁来维护？

**Agent 通过工具维护。** 和 memory 一样——CRUD 工具注册，Agent 自主判断何时更新。

### 工具设计

```
quest_update(target, patch)
  target: "main" | "sq-1"  ← 要更新的任务
  patch: {
    status?: "in_progress" | "done" | "blocked"   ← 变更状态
    progress?: string       ← 更新进度描述
    title?: string          ← 修改标题（首次必须提供）
  }

quest_event(text, type, soul?)
  text: "部署成功，API 在线"
  type: "action"
  soul: "zero"              ← 可选，谁做的

quest_summary()
  返回当前 quest 的完整摘要（给 Agent 自查用）
```

### 使用场景

1. 用户说"开始做 xx" → Agent 调 `quest_update("main", {status:"in_progress", title:"..."})`
2. 用户说"这个完成了" → Agent 调 `quest_update("main", {status:"done"})` + `quest_event("完成", "done")`
3. 做一半有新想法 → Agent 调 `quest_update("sq-1", {status:"in_progress", title:"..."}, parent:"main")`
4. 遇到 bug → Agent 调 `quest_event("连接超时", "blocker", "zero")`

---

## 5. 角色怎么利用时间线？

导演（LLM）在构建角色发言时，会把 quest log 作为叙事上下文。它的 system prompt 里加上：

```
<quest_guidance>
When directing souls to speak, consider the quest log:
- If a quest just started, souls should express enthusiasm or ask questions
- If a quest was completed, souls should acknowledge it (celebrate, reflect)
- If a quest is blocked, souls can offer help or express frustration
- If a new idea appeared (timeline type=idea), souls can pick it up and discuss
- Souls can reference recent timeline events naturally ("刚才你说...", "既然决定了...")
</quest_guidance>
```

**效果：**

```
时间线显示：16:00 [user] 提出事件时间线方案

→ hanpai: "时间线这个想法不错。不过你说的是 per-soul 还是全局的？"
→ zero: "应该是按 session 的。毕竟每个对话窗口的叙事上下文不同。"
```

vs 没有时间线：

```
→ hanpai: "所以，要做什么？"  ← 不知道在讨论什么
→ zero: "你有什么想法吗？"    ← 空洞
```

---

## 6. 和 per-soul 记忆的关系

| | Quest & Timeline | Per-soul Memory |
|---|---|---|
| 生命周期 | 当前 session | 长期（跨 session） |
| 存什么 | 进行中的任务 + 近期事件 | 提炼后的知识片段 |
| 角色感知 | 所有角色看到同一个 | 每个角色有自己的视角 |
| 转换 | 主线完成时 → 可为 per-soul 记忆 | — |

**转换规则：** 当主线标记为 `done` 时，Agent 可以决定是否从时间线中提炼记忆。例如：

```
主线"修复登录 bug"完成 →
  → zero 的记忆：修复了登录 bug（2026-05-22），原因是 cookie 过期
  → hanpai 的记忆：社老师修登录 bug 时忘了清缓存，我提醒了两次
```

两个角色对同一件事有不同的记忆——这正是 per-soul 记忆的价值。

---

## 7. 实现阶段

| 阶段 | 内容 | 输出 |
|------|------|------|
| P1 | quest.json 存储 + system prompt 注入 | quest-store.ts, system-prompt 修改 |
| P2 | Agent 维护工具（quest_update, quest_event, quest_summary） | tool-impls 扩展 |
| P3 | 角色利用指令（quest_guidance in prompt） | system prompt 指令层 |
| P4 | quest → per-soul 记忆转换 | 事件 → 记忆提炼 |

P1 是最小可行——手写 quest.json，验证导演能否利用时间线提升角色发言质量。之后再让 Agent 自己维护。

---

## 8. 待讨论的问题

1. **事件粒度**：工具调用算不算事件？如果一个 bash 命令部署了服务，该自动记录还是等 Agent 主动记录？——建议：Agent 主动记录，避免噪音。

2. **session 结束时 quest 怎么处理？** 支线全部丢弃？还是保存到 session 的 index.json？——建议：保存 quest 快照到 session 索引，下次续聊可恢复。

3. **时间线要不要在 CLI 里展示？** 比如 `half-pi quest show` ——建议：P1 不加，P2 加。

4. **多条主线？** 一次只能有一个主线还是可以有多个并行主线？——建议：一个主线 + N 个支线。如果真有并行任务，用支线表示即可。
