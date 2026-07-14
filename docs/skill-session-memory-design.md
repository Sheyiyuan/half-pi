# Session / Memory 组织设计

> 状态：草稿（2026-07-14 更新）
> 
> 本篇是会话与记忆的全貌设计。Skill 子系统的深入设计见 [skill-design.md](skill-design.md)。

## 问题

当前系统缺少一套灵活的组织结构来管理：

- **会话（Session）**：对话的层级关系与生命周期
- **记忆（Memory）**：跨会话的长期知识如何按组沉淀、按需注入

一个典型场景：

```
用户在「编程」组里和 Half-Pi 聊了两周 Go 项目，它记住了代码风格、项目结构。

切换到「办公」组——它立刻进入办公语境，记得会议模板、邮件措辞。
编程组的记忆不会污染办公组的对话。

用户在编程组里需要查阅办公组的某段记忆：
「上次那个客户的合同条款偏好是什么？」
Half-Pi 可以跨组查询——但这是用户主动请求，不是默认行为。
```

## Memory 是什么

Memory 不是「上一次会话的摘要」——那是上下文延续，靠对话历史自然承接。

Memory 是**按 SessionGroup 累积的长期知识**：

| 组 | 会记住什么 |
|------|------|
| 编程 | Go 代码风格、项目架构、常用 API 参数、部署流程 |
| 办公 | 会议模板、邮件措辞习惯、合同条款偏好、客户背景 |
| 聊天 | 爱好的乐队、下周旅行计划、常点的咖啡 |

三个组的记忆互不干扰。切换到新组时，LLM 只注入对应组的记忆，进入全新的知识上下文。

## 核心概念

### SessionGroup（会话组）

介于全局和单个会话之间的统一配置区间。一个组下可以有多个会话，
它们共享同一套配置。

SessionGroup 的 identity 由工作目录路径自然决定。
`/home/syy/projects/alpha` 和 `/home/syy/projects/beta` 是两个不同的组。

### 配置层级

```
全局默认 < SessionGroup < 单个 Session
```

| 配置项 | 默认值 | 组可覆盖 | 会话可覆盖 |
|--------|------|------|------|
| **Soul** | 全局人格 | ✅ | ✅ |
| **Skillset** | 全局技能集 | ✅ | ✅ |
| **Memory** | 独属于组，会话间共享 | ✅（组级粒度） | — |

### 记忆隔离

| 维度 | 默认行为 | 用户可操作 |
|------|------|------|
| 不同组之间 | 完全隔离 | 读取（按需） / 写入（手动） |
| 同组内会话 | 共享同一份记忆 | 不可隔离 |

跨组访问记忆是用户主动行为——LLM 不能自行跨组读取或写入。
用户在对话中要求查阅其他组的记忆时，LLM 通过工具按需获取。

## 数据模型

```
SessionGroup（会话组）
  ├── id              ← 工作目录路径 hash
  ├── name             ← 用户自定义，如 "Project Alpha"
  ├── work_dir         ← 文件系统路径
  ├── soul             ← 可选，覆盖全局人格
  └── skillset_ids     ← 该组启用的技能集

Session（会话）
  ├── id
  ├── group_id         ← 所属组
  ├── created_at
  ├── soul             ← 可选，覆盖组级人格
  └── enabled_skills   ← 可选的会话级覆盖
```

## 跨组访问

两个工具：

```
read_memory(group_name, key)    → 读取指定组的长期记忆
write_memory(group_name, key, value) → 写入指定组的长期记忆
```

LLM 默认只能操作当前组的记忆。用户说「查一下办公组里关于客户的记忆」时，
LLM 调用 `read_memory("办公", "客户背景")`。

是否需要用户确认？取决于当前安全模式——Normal 模式应弹出确认。

## 存储

统一用 SQLite（与 `store/` 的会话持久化同一数据库）：

| 表 | 内容 |
|------|------|
| `session_groups` | group_id, name, work_dir, soul |
| `sessions` | session_id, group_id, created_at |
| `memories` | group_id, key, value, updated_at |

技能定义文件（`.skill.md`）继续放文件系统，绑定的技能集关系存 SQLite。

## 待讨论

- 会话组的 skill 绑定：是组里一个 `skillsets` 列表，还是通过独立的 binding 表？
- 跨组写入：是否应该始终要求确认（无论什么安全模式）？
- Memory 的 key-value 是否需要结构化类型支持（仅 string 还是支持 JSON object）？
