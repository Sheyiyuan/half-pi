# 记忆系统设计

> 状态：设计中，未实现  
> 依赖：[技术选型](./技术选型-记忆存储.md)

half-pi 的记忆系统需要解决两个核心问题：**分层筛选**（哪些记忆该同步、哪些该注入）和**自我迭代**（Agent 自己整理记忆）。

---

## 1. 分层存储架构

```
~/.half-pi/memory/
├── local/                    ← 设备绑定，不进 git
│   ├── env-node-path.md      # 本机 Node.js 路径
│   ├── env-python-venv.md    # 本机 Python 虚拟环境位置
│   ├── pref-terminal.md      # 终端偏好（Kitty 配置）
│   └── cache-model-list.md   # 本机可用模型列表
│
├── cloud/                    ← 跨设备共享，git 同步
│   ├── user-pref-editor.md   # 偏好 Vim 键位
│   ├── user-role-cpp.md      # 共青团员，央国企求职
│   ├── env-project-rules.md  # 项目通用约定（分支命名等）
│   ├── lesson-patch-tool.md  # 经验教训：patch 工具的坑
│   ├── lesson-write-tool.md  # 经验教训：write 工具的引号问题
│   └── episodic-2026-05-15.md # 事件记忆：那天讨论了 Agent 架构
│
└── index.sqlite              ← 本地索引（自动重建，不进 git）
    ├── entries 表            # 记忆元数据
    ├── embeddings 表         # 向量（将来用于语义检索）
    └── stats 表              # 调用统计
```

**分层原则：**

| 层级 | 范围 | 同步 | 内容举例 |
|------|------|------|---------|
| `local/` | 本设备 | 否 | 环境路径、终端配置、设备特定偏好 |
| `cloud/` | 所有设备 | 是（git） | 用户身份、通用偏好、经验教训、事件 |

记忆写入时，Agent 根据内容自动判断 `scope`——也可以手动指定。判断逻辑很简单：包含路径、设备名、环境变量 → local；其他 → cloud。

---

## 2. 记忆条目格式

每条记忆是一个 Markdown 文件，YAML frontmatter 携带元数据：

```markdown
---
id: 2026-05-21-user-pref-editor
scope: cloud
type: user_pref
priority: high
tags: [editor, keybinding]
created: 2026-05-21
updated: 2026-05-21
call_count: 14
last_called: 2026-05-21T14:30:00Z
weight: 0.85
---

社老师是 Vim 用户。在帮他配置编辑器或写快捷键文档时
默认使用 Vim 风格。具体偏好：

- 使用 Vim 键位（hjkl 移动、模态编辑）
- 在终端中也偏好 vi-mode（set -o vi）
- 不要 Emacs 风格的键位
```

**元数据字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 唯一标识，格式 `YYYY-MM-DD-{slug}` |
| `scope` | `local\|cloud` | 设备绑定 or 云端共享 |
| `type` | enum | `user_pref \| env_fact \| lesson \| episodic` |
| `priority` | enum | `critical \| high \| medium \| low \| archive` |
| `tags` | string[] | 自由标签，用于过滤和分组 |
| `created` | date | 创建时间 |
| `updated` | date | 最后修改时间 |
| `call_count` | number | 被检索注入 system prompt 的总次数 |
| `last_called` | datetime | 最后一次被注入的时间 |
| `weight` | float | 读取权重（0-1），自动计算 |

---

## 3. 权重计算

权重决定了一条记忆在 system prompt 构建时被注入的优先级。

```
weight = clamp(base_weight × decay + freq_bonus, 0, 1)
```

### 基础权重（由 priority 决定）

| priority | base_weight | 含义 |
|----------|-------------|------|
| `critical` | 1.0 | 必须记住（用户明确说「记住这个」） |
| `high` | 0.8 | 重要偏好或关键教训 |
| `medium` | 0.5 | 一般信息，默认等级 |
| `low` | 0.2 | 可能有用的参考信息 |
| `archive` | 0.0 | 已归档，不注入但保留原文 |

### 时间衰减

| last_called 距今 | decay 因子 |
|-----------------|-----------|
| < 7 天 | 1.0 |
| 7-30 天 | 0.9 |
| 30-90 天 | 0.7 |
| > 90 天 | 0.5 |

### 频率加成

| call_count | freq_bonus |
|-----------|------------|
| 0-5 | 0 |
| 5-20 | +0.05 |
| 20-50 | +0.10 |
| 50+ | +0.15 |

### 计算示例

```
一条 type=lesson 的记忆：
  priority=medium → base_weight = 0.5
  last_called = 15 天前 → decay = 0.9
  call_count = 23 → freq_bonus = +0.10

  weight = 0.5 × 0.9 + 0.10 = 0.55
```

---

## 4. 注入策略

每次构建 system prompt 时：

```
1. 从 cloud/ 和 local/ 加载所有记忆
2. 过滤：weight > threshold（默认 0.3）
3. 排序：weight 降序
4. 注入：逐条追加到 prompt，累计 token 不超过预算

预算 = 模型 context window × 5%（默认）
例如 200k 窗口 → 记忆预算 10k tokens
```

注入格式：

```
## Memory

<memory id="2026-05-21-user-pref-editor" weight="0.85">
社老师是 Vim 用户。在帮他配置编辑器或写快捷键文档时
默认使用 Vim 风格。
</memory>

<memory id="2026-05-20-lesson-patch-tool" weight="0.55">
write_file 工具会将 Unicode 弯引号转换为 ASCII 双引号。
写包含中文引号的代码时改用直角引号「」或单引号。
</memory>
```

超过预算的部分截断，并在末尾注明 `... (3 more memories omitted, use memory:search to retrieve)`。

---

## 5. Agent 自迭代：记忆整理

Agent 通过工具主动管理自己的记忆。不仅是 CRUD，还有整理能力。

### 工具清单

| 工具 | 操作 | 触发方式 |
|------|------|---------|
| `memory_create` | 创建新记忆 | Agent 判断有价值 |
| `memory_list` | 列出记忆（支持过滤、排序） | 用户请求或 Agent 自查 |
| `memory_search` | 关键词/语义搜索 | 需要特定信息时 |
| `memory_update` | 修改记忆内容或元数据 | 信息过时或补充 |
| `memory_delete` | 删除记忆 | 确认不再需要 |
| `memory_merge` | 合并两条相关记忆 | 检测到高度相似 |
| `memory_archive` | 降权为 archive | 超过 90 天未调用 |
| `memory_promote` | 升权（通常是 → critical） | 用户说「记住这个」 |
| `memory_stats` | 查看记忆统计（总数、权重分布、调用频率） | 定期自查 |

### 自动整理规则（Agent 定期执行）

```
每周运行一次整理任务（通过 cron）：

1. 标记建议归档：
   - last_called 超过 90 天 且 priority ≠ critical
   → Agent 生成归档建议列表，用户确认后执行

2. 检测相似记忆：
   - 标签重叠 ≥ 2 个 且 内容 embedding 余弦相似度 > 0.85
   → Agent 建议合并，展示 diff，用户确认后执行

3. 清理孤立记忆：
   - weight < 0.15 且 created 超过 180 天
   → Agent 建议删除

4. 更新统计：
   - 重新计算所有记忆的 weight
   - 更新 index.sqlite 中的 stats
```

### 合并示例

```
记忆 A：社老师在 ~/Code/pi 项目里偏好用 feat 分支
记忆 B：社老师在 narratable 项目里也偏好 feat 分支

合并后：
社老师在所有项目里偏好 feat 分支工作流：
具体做法是开新分支 → 修改 → 提交 → 推送。
不要在 main 上直接 commit。
（原始记忆 A 和 B 标记为 archive）
```

---

## 6. 命令行接口

```bash
# 查看所有记忆
half-pi memory list

# 搜索记忆
half-pi memory search "Vim"

# 手动创建
half-pi memory create --priority high --scope cloud "社老师偏好..."

# 查看统计
half-pi memory stats

# 触发自动整理（预览模式）
half-pi memory tidy --dry-run

# 执行自动整理
half-pi memory tidy
```

---

## 7. 实现阶段

| 阶段 | 内容 | 输出 |
|------|------|------|
| **Phase 1** | 纯 Markdown 存储 + YAML frontmatter + 权重计算 | `memory-store.ts` |
| **Phase 2** | 注入管线（过滤 → 排序 → token 预算裁剪） | `memory-injector.ts` |
| **Phase 3** | CRUD 工具注册（memory_create/list/update/delete） | `core/tools/memory.ts` |
| **Phase 4** | 整理工具（merge/archive/promote/stats） + cron 自整理 | `core/memory-tidy.ts` |
| **Phase 5** | sqlite-vec 向量索引 + 语义检索 | `memory-index.ts` |

Phase 1-2 覆盖「分层存储 + 权重筛选」，是记忆系统的最小可行版本。Phase 3-4 实现 Agent 自迭代整理。Phase 5 是向量检索的升级，接口已预留。
