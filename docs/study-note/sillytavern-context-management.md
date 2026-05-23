# SillyTavern 酒馆上下文管理深度分析

> 基于 [`/home/syy/code/SillyTavern/`](https://github.com/SillyTavern/SillyTavern) (commit: 51ad27f)
> 核心代码: `public/scripts/openai.js` (7249 行), `public/scripts/power-user.js`, `public/scripts/sysprompt.js`

---

## 一、Chat Completion 路径设计哲学

酒馆把 prompt 构建当成**资源管理问题**来解：

- **约束**: 模型有固定的 context window（`this_max_context`），预算有限
- **需求**: 角色卡、WI、系统提示、扩展注入、对话历史……全都要放
- **解法**: 离散化 → 优先级排序 → Token Budget 动态填充

这不是字符串拼接，是**组合器**——每段内容独立管理，按优先级和预算动态装配。

---

## 二、三阶段构建流程

```
preparePromptsForChatCompletion()    # 列清单：有什么
         ↓
populateChatCompletion()             # 按预算填：能放什么
         ↓
chatCompletion.getChat()             # 产出：最终 messages 数组
```

### 阶段一: preparePromptsForChatCompletion()

把所有可能进入 prompt 的内容列成 prompt 对象。每个对象带:
- **标识符**: main / jailbreak / depth_1 / nsfw / quietPrompt 等
- **role**: system / user / assistant
- **位置**: 在 prompt 中的相对次序
- **优先级**: mandatory / normal / control

此时**没有任何取舍**，所有备选内容都在清单里。

### 阶段二: populateChatCompletion()

使用 `ChatCompletion` 类的 Token Budget 机制：
1. `reserveBudget()` — 先预留 mandatory prompts（如 main system prompt）
2. 反向遍历消息（最新→最旧），`canAfford()` 逐条检查 token 余量
3. 超限即停，后续消息被裁剪
4. `freeBudget()` — 最后添加 control prompts（quiet prompt 等）

关键：反向遍历意味着越新的消息越优先保留，这是对 recency bias 的显式利用。

### 阶段三: chatCompletion.getChat()

返回最终要发送的 messages 数组，此时已完成所有裁剪和排序。

---

## 三、Depth 机制

核心思路：**利用模型的 recency bias，把关键设定放在离输出最近的位置。**

```
depth=0 → 最后一条消息之后（最近，影响力最强）
depth=1 → 倒数第二条之后
depth=N → 倒数第 N+1 条之后
```

### 实现机制

1. 所有需深度注入的内容通过 `setExtensionPrompt(key, value, IN_CHAT, depth)` 注册
2. `doChatInject()` 收集这些提示，按 depth 映射到 coreChat 中的索引位置
3. 生成 `injectedIndices[]`，标记哪些位置被注入
4. 在填充阶段，这些 injected 消息**优先于**普通对话消息被填入预算

这意味着：**depth prompt 挤占的是对话历史的位置，而不是叠加在 system prompt 之上。**

### Prompt Manager

OAI 路径中的 Prompt Manager（openai.js）是 SillyTavern 的高级功能：
- 支持用户自定义 prompt 段的顺序、role、注入位置
- 通过可视化 UI 拖拽管理
- 本质是一个**组合器**——把角色卡、WI、系统提示、对话历史等模块化组件拼成最终 messages

---

## 四、与 half-pi 当前实现的对比

| 维度 | SillyTavern（Chat Completion） | half-pi（当前） |
|------|-------------------------------|-----------------|
| **预算管理** | Token Budget 系统，先 reserve 再 canAfford | 全量拼接，唯一预算控制是 MemoryInjector 的 5% 硬限制 |
| **Depth 机制** | 锚点到指定深度，利用 recency bias | 无。所有内容平铺在 system prompt 中 |
| **离散化** | 每段独立标识符 + role + 优先级 | 一次性字符串拼接，无元数据 |
| **两阶段构建** | preparePrompts → populateChatCompletion | buildSystemPrompt() 一次性返回 |
| **Role 分离** | 各 prompt 段可指定 system / user / assistant | 全部在同一个 system prompt 内 |
| **裁剪策略** | 反向遍历，预算超即停 | 模型自行截断或爆 context window |

---

## 五、值得借鉴的设计点

1. **Token Budget 作为第一公民** — 系统 prompt 和对话历史共用同一个预算池，而不是各自为政
2. **离散 prompt 段 + 元数据** — 每段有标识符和 role，未来支持可视化编排
3. **Depth 注入** — 对 roleplay 场景尤其有价值，可保关键设定在近端
4. **两阶段构建** — 分离"有什么"和"放什么"，逻辑清晰
