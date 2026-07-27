# Skill 系统改进

> 状态：已实现（2026-07-26）。
>
> 本文定义 Skill 系统的 4 项定向改进，不改变现有架构骨架。

## 1. 决策：不重写

当前 Skill 系统的核心设计是正确的：

- 全量 `name + description` 注入 system prompt，由**模型做语义匹配**判断相关性
- 两层披露：摘要（system prompt）→ 全文（`view_skill` 工具）
- 前缀稳定，可被 prompt cache 覆盖

这条路线与 Anthropic Agent Skills 的核心机制一致。在 half-pi 的目标规模（5–50 个技能）下，全量常驻索引的 token 成本低于任何按需匹配方案——因为索引位于稳定前缀，缓存生效时有效成本约为原始 token 的 10%。

此前曾评估过「倒排索引 + 关键词匹配替代模型判断」的方案，结论是不采纳。原因：

1. **关键词匹配在语义任务上不如模型。** description 匹配是模型最擅长的任务，用 BM25-lite 替代是降级。
2. **token 账是负收益。** 按需方案的 skill suffix 在 messages 尾部，结构上不可缓存；在 30 个技能的典型规模下，有效 token 成本反而更高。
3. **状态管理成本远超收益。** active set、disclosure plan、preflight/commit 分离、compact 联动——整套机制约 400 行，替换的是当前 12 行 transformer。
4. **compact 集成会引入功能退化。** 压缩点清空 active set 意味着长任务静默丢弃正在遵循的技能指导。

因此，本文档定义的是**增量改进**，不动现有架构。

## 2. 现状

### 2.1 加载

```text
~/.half-pi/skills/
  ├── go-patterns.skill.md
  └── deploy-flow.skill.md

启动时: skill.LoadFromDir(dir) → *skill.Store
                                    ├── local.SetSkillStore()  → view_skill 工具全局变量
                                    └── Manager.Config.Skills  → 每个 Actor.core.SetSkills()
```

限制：
- `LoadFromDir` 只扫描顶层 `*.skill.md`，跳过子目录（`skill.go:80` `entry.IsDir() → continue`）
- `view_skill` 通过包级全局变量 `skillStore` 获取 Store（`tool_view_skill.go:15`）
- 没有 `list_skills` 工具，模型只能从 system prompt 全量索引获知可用技能
- 没有确定性触发机制（如「该 group 下始终激活」）

### 2.2 System Prompt 注入

`coreModelContextTransformer`（`chat.go:404–421`）每次模型请求调用 `skills.IndexForGroup(groupID)`，输出全部可见 skill 摘要：

```text
可用技能：
  go-patterns          — Go coding patterns
  deploy-flow          — Standard deploy flow

查看技能详情：view_skill("<name>")
```

12 行代码，功能正确。

### 2.3 环境感知

`modelEnvironmentToken`（`context.go:18`）已包含 `SkillRevision` + `SkillDigest`，参与 request environment digest 和 admission 校验。skill reload 或内容变化时，环境 digest 变化会导致 preflight 校验失败（fail closed），不会静默使用过期环境。这个机制保持不动。

## 3. 改进项

### 3.1 移除全局 `skillStore`，走 context 注入

当前 `tool_view_skill.go:15` 的包级变量：

```go
var skillStore *skill.Store
var skillStoreMu sync.RWMutex
```

改为 `LocalExecutor` 持有并注入 context：

```go
// LocalExecutor 增加字段
type LocalExecutor struct {
    bridge     *RemoteBridge
    skillStore *skill.Store
}

// PrepareToolContext 注入
func (e *LocalExecutor) PrepareToolContext(ctx context.Context) context.Context {
    ctx = WithSkillStore(ctx, e.skillStore)
    if e.bridge != nil {
        ctx = WithRemoteBridge(ctx, e.bridge)
    }
    return ctx
}
```

- 新增 context helper：`WithSkillStore(ctx, store)` / `SkillStoreFromContext(ctx)`
- `view_skill` 从 context 取值，fallback 为明确错误
- 删除 `SetSkillStore`、全局变量和锁
- `conversation.go:78` 的 `local.SetSkillStore(skills)` 改为传入 `New(...)` 参数
- `RemoteBridge` 的 context 注入已有成例（`executor.go:32`），直接复用模式

约束：
- 不把 `*skill.Store` 放进 `executor.Invocation`（`executor` 在 `half-pi-core`，不能依赖 Mind internal 包）
- `view_skill` 和后续 `list_skills` 使用 `GetForGroup(name, groupID)`，找不到或无权限时统一返回 `skill not found: <name>`

预估：~30 行改动，零行为变化。

### 3.2 递归扫描

`LoadFromDir` 改为 `filepath.WalkDir` 递归扫描：

```text
~/.half-pi/skills/
├── programming/
│   ├── go-patterns.skill.md
│   └── python-testing.skill.md
├── workflow/
│   └── deploy-flow.skill.md
└── general.skill.md
```

规则：
- 初版只加载 `*.skill.md`
- 跳过隐藏目录（`.` 前缀）和常见缓存目录（`node_modules`、`__pycache__` 等）
- 同名 skill 冲突：按路径字符串排序后第一个生效，记录 parse warning
- parse 错误跳过，但保留 warning 供后续 diagnostics 暴露
- `Skill.FilePath` 字段（`skill.go:30`）开始承载目录上下文，而非始终为顶层文件名

预估：~30 行改动，主要在 `reload()` 内部。

### 3.3 新增 `list_skills`

```text
list_skills()
```

返回当前 SessionGroup 可见 skill 的 `name + description + tags`，不返回正文。

要求：
- 从 context 读取 `*skill.Store`（复用 §3.1 的 context helper）
- 从 `executor.LifecycleMetaFromContext(ctx)` 读取 `GroupID`
- 调用 `SummariesForGroup(groupID)`（新增方法：同 `IndexForGroup` 逻辑但返回结构化摘要）
- 输出按 name 稳定排序
- 总字节上限保护（建议 4096 字节，覆盖约 40 个 skill 摘要）
- context 未注入 store 时返回明确错误

新增方法 `SummariesForGroup(groupID string) []SkillSummary`：

```go
type SkillSummary struct {
    Name        string   `json:"name"`
    Description string   `json:"description"`
    Tags        []string `json:"tags"`
}
```

预估：~50 行（工具注册 + Store 方法 + context helper）。

### 3.4 新增 `always` 触发

frontmatter 增加可选字段：

```yaml
---
name: go-patterns
description: Go coding patterns
always: true
---
```

语义：该技能在可见 group 下**无条件激活**，不依赖模型从 description 中判断是否需要。

实现：
- `Meta` 增加 `Always bool` 字段，parser 解析 `always: true`
- `IndexForGroup` 将 `always: true` 的技能排在列表最前，标注 `[始终激活]`
- `view_skill` / `list_skills` 行为不变
- `Snapshot` digest 覆盖 `Always` 字段，变化时触发环境 digest 变更

输出示例：

```text
可用技能：
  go-patterns          — Go coding patterns [始终激活]
  deploy-flow          — Standard deploy flow

查看技能详情：view_skill("<name>")
```

这是 Cursor Rules `alwaysApply` 的等价物。项目规范类技能（编码约定、部署流程、安全策略）必须无条件生效，关键词碰运气或模型自由裁量在此场景下不可接受。

预估：~20 行（Meta 字段 + parser + IndexForGroup 排序/标注 + digest 覆盖）。

## 4. 不变更的部分

- `.skill.md` 文件格式继续有效
- `groups` SessionGroup 可见性语义不变
- `LoadFromDir` / `Reload` / `Snapshot` 接口签名不变
- `IndexForGroup` 继续全量注入 system prompt；生产路径保持当前行为
- `view_skill(name)` 对模型的功能语义不变
- `coreModelContextTransformer` 的 12 行逻辑保持不动
- `modelEnvironmentToken` 保持现有结构，仅 `SkillDigest` 因 §3.4 的 `Always` 字段自然覆盖
- compact / UsageAnchor / request fingerprint 不变
- 不增加用户配置项；新行为通过代码常量或 frontmatter 控制

## 5. `SKILL.md` 兼容

不在本次范围。原因：
- 现有 parser 要求 frontmatter 必含 `name`，而外部 `SKILL.md` 不一定有
- 不同生态的 `SKILL.md` 对名称、目录包、附带资源有不同约定
- 直接兼容会引入重名、相对资源、递归包边界等额外规则

递归扫描（§3.2）落地后，`FilePath` 自然承载目录上下文。后续兼容 `SKILL.md` 时，以「skill 目录包」语义独立设计，不混入本次改动。

## 6. 实施结果

四项改动均已落地：

| 改动 | 落点 |
|------|------|
| 递归扫描 | `skill.go` `scanSkillFiles()` / `skipSkillDir()` / `reload()`；新增 `Store.Warnings()` |
| context 注入 | `local/skill_store.go`（`WithSkillStore` / `skillStoreFromContext`）、`LocalExecutor.SetSkills`、`PrepareToolContext`；删除包级 `skillStore` 和 `SetSkillStore` |
| `list_skills` | `local/tool_list_skills.go`；`Store.SummariesForGroup()` + `SkillSummary` |
| `always` | `Meta.Always`、`parse.go` `parseBool()`、`visibleToGroup()` 排序、`IndexForGroup()` 标注、Snapshot digest 覆盖 |
| 诊断与 reload（§9） | `repl/command_skill.go` 的 `/skill list\|reload\|warnings`、`Core.Skills()` getter、启动期 `publishSkillWarnings()` |

调用点变化：
- `cmd/half-pi-mind/conversation.go` 移除 `local.SetSkillStore(skills)`
- `conversation/manager.go` `newActor()` 改为 `exec.SetSkills(m.config.Skills)`

实测行为（3 个技能 + 2 个应跳过目录 + 1 个损坏文件）：

```text
=== loaded skills ===
  conventions    always=true  path=/tmp/skilldemo/conventions.skill.md
  deploy-flow    always=false path=/tmp/skilldemo/workflow/deploy.skill.md
  go-patterns    always=false path=/tmp/skilldemo/programming/go.skill.md
=== warnings ===
  skipped /tmp/skilldemo/broken.skill.md: skill file must start with ---
=== IndexForGroup("") ===
可用技能：
  conventions          — Project coding conventions [始终激活]
  deploy-flow          — Standard deploy flow
  go-patterns          — Go coding patterns

查看技能详情：view_skill("<name>")
```

`.hidden/` 和 `node_modules/` 下的技能未被加载。

## 7. 测试覆盖

`internal/skill`：

| 测试 | 覆盖 |
|------|------|
| `TestStoreScansSubdirectories` | 顶层 + 多层子目录加载；`.hidden/`、`node_modules/` 跳过；非 `.skill.md` 不加载；嵌套 `FilePath` 正确 |
| `TestStoreDuplicateNameIsDeterministic` | 重名按路径排序首个生效 + duplicate warning |
| `TestStoreRecordsParseWarnings` | parse 错误不影响其他 skill，并记录 warning |
| `TestAlwaysSkillsSortFirstAndAreAnnotated` | always 优先排序、非 always 不被误标注 |
| `TestAlwaysChangeAffectsSnapshotDigest` | `Always` 变化触发 digest 变化（联动环境 digest fail closed） |
| `TestSummariesForGroupEnforcesVisibility` | group 隔离在摘要路径同样生效 |

`internal/executor/local`：

| 测试 | 覆盖 |
|------|------|
| `TestSkillToolsRequireStoreInContext` | 两个工具在 context 未注入时返回明确错误，不静默成功 |
| `TestListSkillsEnforcesLifecycleGroup` | 只返回当前 group 可见摘要；不泄露正文；always 优先 |
| `TestViewSkillEnforcesLifecycleGroup` | 改用 context 注入后 group 隔离仍生效（既有测试） |

`internal/repl`：

| 测试 | 覆盖 |
|------|------|
| `TestSkillCommandIsRouted` | `/skill` 全部子命令（含未知子命令）被 REPL 路由 |
| `TestSkillReloadPicksUpNewFiles` | 运行中新增技能文件经 reload 生效并报告数量变化 |
| `TestSkillReloadChangesSnapshotDigestForEnvironmentCheck` | reload 推进 revision/digest，保证 admission fail closed |
| `TestSkillWarningsAreVisible` | 损坏文件的告警可见 |
| `TestSkillCommandsTolerateMissingStore` | store 未注入时三个子命令均明确报错，不 panic |

全部 5 个模块 `-race` 通过。

## 8. 风险

| 风险 | 缓解 |
|------|------|
| 递归扫描引入重名冲突 | 路径排序 + warning；`Store.Warnings()` 已就绪，尚未接入用户可见 diagnostics |
| context 注入遗漏调用点 | `PrepareToolContext` 是统一入口，覆盖所有工具执行路径 |
| `SKILL.md` 生态差异 | 本次不兼容，后续按 package 语义单独设计 |

## 9. 诊断与 reload

### 9.1 启动告警

`cmd/half-pi-mind/conversation.go` 的 `publishSkillWarnings()` 在 `LoadFromDir` 后把 `Store.Warnings()` 以 `LevelWarn` 发到 EventBus。服务模式写入 `~/.half-pi/logs/mind.log`，REPL 模式直接打印。技能因为 parse 失败或重名被静默丢弃时，用户能看到原因而不是只发现技能"不见了"。

### 9.2 `/skill` 命令

| 命令 | 行为 |
|------|------|
| `/skill` / `/skill list` | 列出当前 group 可见技能，`*` 标记 always |
| `/skill reload` | 重新扫描技能目录，报告可见数量变化并打印告警 |
| `/skill warnings` | 显示当前加载告警 |

实测输出：

```text
>  * conventions          Project coding conventions
   go-patterns          Go coding patterns [go, coding]
2 skills (* = always active)

> /skill reload
skills reloaded: 3 visible in this group (was 2)
skill: skipped ~/.half-pi/skills/broken.skill.md: skill file must start with ---

>  * conventions          Project coding conventions
   deploy-flow          Standard deploy flow [ops]
   go-patterns          Go coding patterns [go, coding]
3 skills (* = always active)
```

reload 语义：

- `*skill.Store` 由所有 Actor 共享（`Manager.Config.Skills`），reload 对全部会话立即生效
- reload 使 `revision` 单调递增、`Digest` 变化，进而改变 `modelEnvironmentToken`；进行中的模型请求在 `admitModelRequest()` 阶段 fail closed，不会混用新旧环境
- `Core.Skills()` 是新增的只读 getter，REPL 通过它取到共享 Store

## 10. 后续

- 技能数增长到 50+ 时，按 §1 的判断走 `tool_only`（`IndexForGroup` 不再注入 system prompt，只保留 `list_skills` / `view_skill`），而不是关键词匹配
- Face / TUI 侧没有对应的 skill 视图；目前 `/skill` 只在 Mind REPL 可用
