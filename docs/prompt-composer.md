# PromptComposer 设计

> 状态：设计中
> 更新：2026-05-23

## 设计哲学

参考 SillyTavern Chat Completion 路径的资源管理思路，把 prompt 构建当成**资源管理问题**来解——预算有限、需求很多，按优先级分配。

核心思路：把所有可能进入 prompt 的内容拆成离散的段（segment），每段带优先级和 role，通过统一的 Token Budget 机制动态填充。

## 动机

当前 `buildSystemPrompt()` 一次性拼接字符串的问题：
- **无全局预算意识** — 所有内容堆在一起，模型自行截断或爆 context window
- **无优先级体系** — 预算紧张时无法做取舍
- **无 role 分离** — 灵魂身份、记忆、工具列表全部挤在同一个 system role 里
- **无分层概念** — 无法利用 recency bias

需要一个编排层来统一管理。

## 架构总览

```
PromptComposer (编排层)
 │
 ├─ 段注册表 (SegmentRegistry)
 │   收集所有 prompt 段的元数据
 │
 ├─ 生产工厂 (下层)
 │   ├─ SoulIdentityBuilder  → identity 段
 │   ├─ MemoryInjector      → memory 段
 │   ├─ SkillInjector       → skills 段
 │   └─ Summarizer          → summary 段 (新增)
 │
 ├─ compose() 流程
 │   1. collectSegments()   收集所有段
 │   2. partitionHistory()  分区: 早期→Summarizer, 近期保留
 │   3. runSummarizer()     早期对话→摘要段
 │   4. allocateBudget()    按优先级分配 token
 │   5. assembleMessages()  输出最终 messages[]
 │
 └─ 输出 → pi-agent-core 的 Agent 实例
      systemPrompt (字符串) + messages (裁剪后)
```

### 分层职责

| 层 | 职责 | 是否新增 |
|----|------|---------|
| PromptComposer | 编排：注册、预算、裁剪、组装 | 新增 |
| SoulIdentityBuilder | 生产灵魂身份段 | 现有 (system-prompt.ts) |
| MemoryInjector | 生产记忆段 | 现有 (memory-injector.ts) |
| SkillInjector | 生产技能段 | 现有 (skills.ts) |
| Summarizer | 生产对话摘要段 | 新增 |
| pi-agent-core | 执行 LLM 调用 | 不修改 |

## Prompt 段 (Segment) 定义

每个段是一个轻量的元数据结构：

```typescript
interface PromptSegment {
  id: string;              // 唯一标识符
  role: "system" | "user" | "assistant";
  content: string;         // 段的内容
  priority: "mandatory" | "normal" | "low";
  tokenCount: number;      // 预估 token 数 (当前为启发式: len/4)
  source: string;          // 来源描述 (调试用)
}
```

### 段注册表

| id | 来源 | 默认 role | 默认 priority |
|----|------|-----------|---------------|
| `soul-identity` | SoulIdentityBuilder | system | mandatory |
| `group-config` | SoulIdentityBuilder | system | mandatory |
| `tools` | SoulIdentityBuilder | system | mandatory |
| `guidelines` | SoulIdentityBuilder | system | normal |
| `skills` | SkillInjector | system | low |
| `context` | SoulIdentityBuilder | system | low |
| `memory` | MemoryInjector | system | normal |
| `summary` | Summarizer | system | normal |
| `dialogue` | agent.state.messages | user/assistant | per message |

## compose() 详细流程

### 1. collectSegments()

从各工厂拉取内容，注册到段注册表。每次 compose() 调用时重新收集，确保内容是最新的。

```typescript
function collectSegments(): PromptSegment[] {
  return [
    { id: "soul-identity", role: "system", content: buildSoulIdentity(), priority: "mandatory" },
    { id: "group-config",  role: "system", content: buildGroupConfig(),  priority: "mandatory" },
    { id: "tools",         role: "system", content: buildToolsList(),    priority: "mandatory" },
    { id: "guidelines",    role: "system", content: buildGuidelines(),   priority: "normal" },
    { id: "skills",        role: "system", content: buildSkillsSection(),priority: "low" },
    { id: "context",       role: "system", content: buildContextInfo(),  priority: "low" },
    { id: "memory",        role: "system", content: memoryInjector.buildMemorySection(), priority: "normal" },
  ];
}
```

### 2. partitionHistory()

把对话历史分成两部分：
- **早期对话**：超出保留条数的部分，传给 Summarizer
- **近期对话**：保留原始格式，直接参与预算分配

分区策略由 `maxRawMessages` 或 `rawMessagesBudget` (token 预算占比) 控制。

### 3. runSummarizer() (可选)

将早期对话传给 Summarizer，产出摘要段。

```typescript
interface SummarySegment {
  content: string;
  tokenCount: number;
}
```

> 触发策略和异步更新机制后续细化。

### 4. allocateBudget()

核心预算分配算法：

```typescript
总预算 = model.contextWindow
预留  = system prompt 基础开销 (格式标记等)

1. 先分配 mandatory 段
2. 再分配 normal 段 (按注册顺序)
3. 再分配 low 段
4. 剩余预算分配给对话消息 (从最新到最旧)
5. 如仍有剩余, 尝试补回被裁剪的 low 段

任何段超出预算即被裁剪
```

```typescript
function allocateBudget(
  segments: PromptSegment[],
  dialogueMessages: Message[],
  totalBudget: number
): { selectedSegments: PromptSegment[]; selectedMessages: Message[] } {
  let remaining = totalBudget;

  // 按优先级排序
  const sorted = sortByPriority(segments);

  // 分配固定段
  const selected: PromptSegment[] = [];
  for (const seg of sorted) {
    if (remaining - seg.tokenCount <= 0 && seg.priority !== "mandatory") continue;
    remaining -= seg.tokenCount;
    selected.push(seg);
  }

  // 分配对话消息 (从新到旧)
  const selectedMessages: Message[] = [];
  for (const msg of [...dialogueMessages].reverse()) {
    const cost = estimateTokens(msg.content);
    if (remaining - cost <= 0) break;
    remaining -= cost;
    selectedMessages.unshift(msg);  // 保持顺序
  }

  return { selectedSegments, selectedMessages };
}
```

### 5. assembleMessages()

把选中的段和消息组装成 messages 数组。所有 system role 的段合并为一条 system message，对话消息保持原有 role。

```typescript
function assembleMessages(
  segments: PromptSegment[],
  dialogueMessages: Message[]
): Message[] {
  const systemContent = segments
    .filter(s => s.role === "system")
    .map(s => s.content)
    .join("\n\n");

  const messages: Message[] = [
    { role: "system", content: systemContent },
    ...dialogueMessages.map(m => ({ role: m.role, content: m.content })),
  ];

  return messages;
}
```

## 与 agent-session.ts 的集成

修改 `agent-session.ts` 中的 `prompt()` 方法：

```typescript
async prompt(text: string): Promise<string> {
  // 1. 用 PromptComposer 计算本次请求的 prompt
  const result = this.promptComposer.compose({
    dialogueMessages: this.agent.state.messages,
    contextWindow: this.model.contextWindow,
  });

  // 2. 设置到 agent
  this.agent.state.systemPrompt = result.systemPrompt;
  this.agent.state.messages = result.messages;

  // 3. 发送
  await this.agent.prompt(text);
  await this.agent.waitForIdle();

  // 4. 记录记忆注入（跟踪 call_count）
  this.promptComposer.recordInjection();
}
```

### PromptComposer 构造函数

```typescript
class PromptComposer {
  constructor(options: {
    soulIdentityBuilder: SoulIdentityBuilder;
    memoryInjector: MemoryInjector;
    skillInjector: SkillInjector;
    summarizer?: Summarizer;
    tokenEstimator: (text: string) => number;
  });
}
```

## 边界情况与错误处理

| 场景 | 处理方式 |
|------|----------|
| mandatory 段总和超过 context window | 抛出配置错误 (需调大 model 或减少 mandatory 内容) |
| 所有段加起来都没超预算 | 对话消息全部保留 (简化实现) |
| Summarizer 调用失败 | 跳过摘要段，保留原始早期对话尝试塞入 (可能被裁剪) |
| 首次对话无历史 | 直接跳过 partitionHistory 和 Summarizer |
| contextWindow 未配置 | 使用默认值 128000 |

## 后续可扩展

- **Depth 注入**: 在 assembleMessages 中支持将特定段插入到指定 depth 位置
- **Prompt Manager UI**: 基于段注册表，提供可视化拖拽排序
- **段级别 role 配置**: 允许用户覆盖每个段的默认 role
- **增量摘要**: 只对新增对话做摘要追加，而非每次重新生成
- **多模型策略**: Summarizer 可用小模型，主对话用大模型
- **精确 Token 计数**: 当 Model 接口暴露 countTokens 时切换
