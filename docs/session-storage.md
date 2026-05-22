# Session 持久化设计

> 状态：P1+P2 已实现（基础存储 + 恢复机制），P3-P5 待开发
> 更新：2026-05-22

## 设计哲学

Session 是对话的完整流水，记忆是从流水里提炼的知识点。
Session 持久化的目标是让对话可以在设备间、时间间延续——
你今天关掉终端，明天打开还能接着聊。

Session 不是永久保存的。它模拟人的遗忘过程：
**热区 → 温区 → 冷区 → 遗忘**，每个阶段用户可以控制阈值。

> 命名说明：用「温度」类比不同阶段的存取成本——
> 「热」正在用、完整可取，「温」存着但不常用、取出来有损但可用，「冷」只留摘要、仅供回顾。
> 目录名称与此一致（hot/ / warm/ / cold/）。

---

## 目录结构

```
~/.half-pi/sessions/
├── hot/                 ← 热区：完整 messages，一个 session 一个文件
│   ├── 20260522-abc123.json
│   └── 20260521-def456.json
├── warm/                ← 温区：压缩版（去冗余、合并轮次）
│   ├── 20260520-ghi789.json
│   └── 20260519-jkl012.json
└── index.json           ← 冷区：只存摘要于作为索引的 index.json，原文件已删除
```

### 各阶段说明

#### hot/ (热区 — 完整版)

存的是 pi-agent-core 的原始 messages 数组。可以直接反序列化恢复。
每个 session 一个 JSON 文件，文件名格式 `{YYYYMMDD}-{shortId}.json`。

#### warm/ (温区 — 压缩版)

压缩规则：
- 合并连续同角色的 assistant 轮次
- 去掉 tool_call / tool_result 的中间细节，只保留摘要（如 "调用了 bash 执行了 git log"）
- 保留 system prompt 和关键 user / assistant 轮次
- 保留所有 soul 的发言（不丢失对话内容）

压缩由 Agent 在降级时执行一次，结果写入 warm/ 目录。
文件格式与 hot/ 相同，仍然是 messages 数组，只是条目变少。

从 warm 晋升回 hot 是**有损的**——已丢弃的 tool_call 细节不会恢复。

#### cold/ (冷区 — 摘要)

不再保留 messages 文件。摘要直接写在 index.json 的 session 记录里：

```json
{
  "20260520-ghi789": {
    "phase": "cold",
    "path": null,
    "title": "记忆系统设计讨论",
    "created": "2026-05-20T10:30:00Z",
    "last_active": "2026-05-22T09:00:00Z",
    "msg_count": 47,
    "warm_msg_count": 12,
    "degraded_at": "2026-05-22T09:05:00Z",
    "summary": "讨论了 session 持久化的分级策略，确定了 hot→warm→cold 的遗忘曲线",
    "parent": "20260518-xyz789"
  }
}
```

`parent` 字段记录该 session 的来源（fork 自哪个 session）。新建 session 没有此字段。
`phase` 为 `cold` 时，`path` 为 `null`（文件已被删除）。

摘要由 Agent 在从 warm 降级到 cold 时生成。
摘要本身不可恢复为完整对话——它只用于检索和回顾。
cold 不可晋升（没有 messages 可恢复）。

index.json 同时充当完整的 session 目录，`half-pi session list` 直接读它即可，无需遍历文件系统。

#### index.json (导航索引)

```json
{
  "version": 1,
  "sessions": {
    "20260522-abc123": {
      "phase": "hot",
      "path": "hot/20260522-abc123.json",
      "title": "Session 持久化实现",
      "created": "2026-05-22T14:00:00Z",
      "last_active": "2026-05-22T15:30:00Z",
      "msg_count": 23,
      "degraded_at": null
    },
    "20260521-def456": {
      "phase": "warm",
      "path": "warm/20260521-def456.json",
      "title": "Session 持久化实现",
      "created": "2026-05-21T10:30:00Z",
      "last_active": "2026-05-22T09:00:00Z",
      "msg_count": 47,
      "warm_msg_count": 12,
      "degraded_at": "2026-05-29T09:05:00Z"
    },
    "20260520-ghi789": {
      "phase": "cold",
      "path": null,
      "title": "记忆系统设计讨论",
      "created": "2026-05-20T10:30:00Z",
      "last_active": "2026-05-22T09:00:00Z",
      "msg_count": 47,
      "degraded_at": "2026-05-22T09:05:00Z"
    }
  }
}
```

---

## 生命周期（遗忘曲线）

```
  hot  ──→  warm  ──→  cold  ──→  deleted
  (热区)    (温区)    (冷区)     (遗忘)
```

分区迁移只看使用间隔，由单个阈值触发：

| 阶段转换 | 默认触发条件 | 说明 |
|----------|-------------|------|
| hot → warm | 超过 7 天未访问 | 太久没聊 |
| warm → cold | 超过 30 天未访问 | 一个月没碰过 |
| cold → deleted | 超过 90 天未创建 | 三个月前的对话 |

### 热区内压缩

hot 区中的 session 如果消息数超过 `hot_max_msgs`，会在原地压缩（去冗余、合并轮次），**不改变 session 所属分区**。

压缩仅用于控制 hot 区文件大小，让活跃对话保持可接受的体积。压缩后的 session 仍然留在 hot/ 目录，下次用户续聊时完全正常。

用户可通过配置覆盖默认值：

```jsonc
// ~/.half-pi/config.jsonc
{
  "session": {
    "hot_days": 7,         // 多久未访问降入温区
    "hot_max_msgs": 200,   // 热区内压缩阈值（不改变分区）
    "warm_days": 30,       // 多久未访问降入冷区
    "cold_days": 90        // 多久后删除
  }
}
```

时间阈值设为 `null` 或 `0` 可禁用对应的阶段转换（如 `hot_days: null` 表示热区永不因时间降级；`cold_days: null` 表示冷区永不自动删除）。

---

## 唯一 ID 生成

每个 session 在创建时生成全局唯一 ID，格式：
`{YYYYMMDD}-{shortId}`

- `YYYYMMDD`：创建日期
- `shortId`：6 位随机字符（字母 + 数字），碰撞概率可忽略

示例：`20260522-a3kX7z`

ID 在整个 session 生命周期内不变（直到被彻底删除）。

---

## 恢复机制

### CLI 接口

```bash
# 恢复最新 session
half-pi chat --last

# 恢复指定 session
half-pi chat --resume 20260522-a3kX7z

# 查看可恢复的 session 列表
half-pi session list
```

### 恢复流程

1. 从 index.json 找到目标 session
2. 根据 phase 字段决定恢复方式
3. 如果是 hot，反序列化 messages 数组注入 Agent（完整恢复）
4. 如果是 warm，反序列化 messages 数组注入 Agent（有损恢复，已丢弃的 tool_call 细节不回来），然后晋升回 hot
5. 如果是 cold，只显示摘要并提示"这个会话只保留了摘要，无法恢复对话"
6. 恢复后 session 的 `last_active` 更新

### 晋升规则

如果用户通过 `--resume` 或 `--last` 恢复了一个 warm 状态的 session，
它应该晋升回 hot（因为用户又回来聊了）。

**晋升是有损的**——warm 版本已经丢弃了 tool_call/tool_result 等细节，
晋升回 hot 时不会恢复这些丢失的信息。后续对话会在有损的基础上继续。
这模拟了人脑的真实记忆：你回想起来的对话永远不会是原版，但足以继续聊下去。

规则：
- hot → hot：直接恢复，无损
- warm → hot：恢复时晋升，有损
- cold → 不可恢复，只展示摘要（用户可手动创建新 session 并引用摘要内容）
- deleted → 不可恢复

---

## 实现阶段

| 阶段 | 内容 | 输出 |
|------|------|------|
| P1 | 基础存储：session 创建、保存到 hot/、index.json 维护 | `session-store.ts` |
| P2 | 恢复机制：`--last` 和 `--resume`、晋升规则 | cli.ts 修改 |
| P3 | 压缩管线：hot 区内压缩（消息数超阈值时原地压缩） | `session-compress.ts` |
| P4 | 迁移管线：hot → warm / warm → cold（摘要生成+删文件）/ cold → deleted + cron | `session-tidy.ts` |
| P5 | session list / session inspect 等管理命令 | cli.ts 扩展 |

---

## 并发控制

多个终端窗口可能同时加载同一个 session。需要防止竞态——两个进程同时写同一份文件，后写的覆盖前写的，导致对话丢失。

### 方案：文件锁 + fork 提示

锁文件放在 `~/.half-pi/sessions/` 目录下：

```
~/.half-pi/sessions/
├── .locks/
│   └── 20260522-abc123.lock    ← 锁文件，内容为 PID
├── hot/
│   └── ...
└── index.json
```

**加锁流程：**

1. 进程 A 尝试加载 session `20260522-abc123`
2. 检查 `.locks/20260522-abc123.lock` 是否存在
3. 不存在 → 创建锁文件（写入自身 PID）→ 正常加载
4. 存在 → 读取锁文件中的 PID
   - PID 对应的进程仍在运行 → 拒绝加载，提示用户
   - PID 已不存在（进程崩溃/退出）→ 清除旧锁，重新加锁

**冲突提示：**

```
[session] 会话 20260522-abc123 正在被另一个终端使用 (PID 12345)
是否要 fork 一份新的会话？
  [y] 是，复制当前会话内容创建新 session（推荐）
  [n] 否，返回 session 列表
```

选择 `y` 时，程序自动执行 fork：
1. 读取当前 session 的 messages
2. 生成新 ID
3. 写入 hot/ 下新文件
4. 更新 index.json（新 session 记录 `parent` 字段指向原 session ID，原 session 不受影响）
5. 加载新 session

**解锁：**

- 进程正常退出时，删除自身持有的锁文件
- 进程崩溃时，下次检测到 stale lock 自动清理

**锁文件格式：**

```json
{
  "pid": 12345,
  "started_at": "2026-05-22T15:30:00Z",
  "hostname": "my-laptop"
}
```

**不阻塞**——冲突时不等待，直接提示用户做决策。这避免了锁超时和死锁问题，也符合 half-pi 的交互风格（用户始终可控）。

---

## 与记忆系统的关系

| | Session 持久化 | 记忆系统 |
|---|---|---|
| 存什么 | 完整对话流水 | 提炼后的知识片段 |
| 格式 | JSON (messages 数组) | Markdown + YAML |
| 位置 | `~/.half-pi/sessions/` | `~/.half-pi/memory/` |
| 生命周期 | 有限（主动遗忘） | 永久（除非确认删除） |
| 用途 | 续聊、回顾 | 跨 session 的长期知识 |
| 谁来写 | 程序自动（每次 prompt 后保存） | Agent 主动（`memory_create`） |
| 谁来删 | cron 自动（按遗忘曲线） | 用户或 Agent 主动 |

两者互补：session 提供对话的连续性和完整回溯能力，
记忆提供跨 session 的知识提炼和注入。
