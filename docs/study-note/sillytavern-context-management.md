# SillyTavern 酒馆上下文管理深度分析

> 基于 `/home/syy/code/SillyTavern/` (commit: 未标注)
> 核心代码: `public/script.js` (12537 行), `public/scripts/openai.js` (7249 行), `public/scripts/power-user.js`, `public/scripts/sysprompt.js`

---

## 一、总体架构

### 生成入口: `Generate()` (script.js:4231)

```
Generate()
  │
  ├─ 1. 预处理
  │   ├─ 事件 GENERATION_STARTED
  │   ├─ slash 命令处理
  │   ├─ 群聊分发 (generateGroupWrapper)
  │   ├─ 用户消息存储 (sendMessageAsUser)
  │   ├─ 角色卡字段提取 (getCharacterCardFields)
  │   ├─ Depth Prompt 注入 (setExtensionPrompt)
  │   └─ 消息预处理 (正则替换 + 文件附件 + reasoning 整合)
  │
  ├─ 2. Token Limit 确定 (getMaxPromptTokens)
  │
  ├─ 3. World Info 扫描 (getWorldInfoPrompt)
  │   ├─ 全局扫描 → worldInfoBefore / worldInfoAfter
  │   ├─ 深度注入 → worldInfoDepth (flushWIInjections)
  │   └─ 出站条目 → outletEntries
  │
  ├─ 4. Story String 构建 (renderStoryString + 格式处理)
  │
  ├─ 5. 上下文注入
  │   ├─ System Prompt (非OAI)
  │   ├─ Jailbreak / Post-History 注入 coreChat
  │   └─ doChatInject → 深度 prompts
  │
  ├─ 6. 上下文窗口裁剪 (Token Budget Filling)
  │   ├─ chat2 数组构建 (消息格式化)
  │   └─ 逐步填充直到 this_max_context
  │
  ├─ 7. Final Prompt 构建
  │   ├─ OAI: prepareOpenAIMessages()
  │   └─ 非OAI: getCombinedPrompt()
  │
  └─ 8. 生成请求 (发送给对应 backend)
```

---

## 二、两种路径: Text Completion vs Chat Completion

SillyTavern 的架构明确分为**两套完全独立的 prompt 构建路径**，选择由 `main_api` 决定。

### 路径 A: Text Completion (kobold / textgenerationwebui / novel)

最终 prompt 是在 `getCombinedPrompt()` (script.js:5073) 中拼接的：

```
combinedPrompt = combinedStoryString
               + mesExmString
               + mesSend  (对话消息数组)
               + generatedPromptCache  (cyclePrompt / 续写缓存)
```

- `mesSend` 通过 `arrMes` (已填充的消息数组) 反向构建，每条消息是 `{message, extensionPrompts[]}`
- 最后的 combine() 函数将各段拼接：`finalMesSend.map(e => e.extensionPrompts.join('') + e.message).join('')`

### 路径 B: Chat Completion (OpenAI / Claude / 等)

走 `prepareOpenAIMessages()` (openai.js:1533) 分三阶段：

```
preparePromptsForChatCompletion()  →  构建 prompt 对象集合
populateChatCompletion()           →  按优先级填入 budget
chatCompletion.getChat()           →  产出最终的 messages 数组
```

使用 `ChatCompletion` 类的 **Token Budget 机制**:
- 先 `reserveBudget()` 预留 mandatory prompts
- 再反向迭代消息，`canAfford()` 检查余量
- `freeBudget()` / 最后添加 control prompts

---

## 三、上下文窗口裁剪算法

### Text Completion 的裁剪 (script.js:4814-4911)

1. **预分配 injected messages** (depth prompts, 各种扩展注入)
   - 遍历 `injectedIndices`，从 `chat2` 中取对应索引的消息
   - 每条加完后检查 `tokenCount < this_max_context`

2. **填充普通消息**
   - 从 `chat2[0]` (最早的对话) 到 `chat2[n]` (最新) 依次添加
   - 跳过已 injected 的位置
   - 超限即 break (早进早出)

3. **填充 User Alignment Message** (可选)
   - 如果最后一条不是用户消息，插入

4. **填充 Message Examples**
   - 在消息之后尝试填入剩余预算
   - 如果 `pin_examples` 开启，跳过此步 (强制最早填入)

5. **记录** `setInContextMessages(arrMes.length - injectedIndices.length, type)`

```
注意: 消息遍历是从旧到新 (chat2[0]=最早, chat2[n-1]=最新)
但最终输出的 mesSend 是 arrMes.reverse() 后的结果
→ 实际发送的 prompt 中，旧消息在前，新消息在后
```

### Chat Completion 的裁剪 (openai.js:876-1050+)

1. 预留 `chatHistory` 位置标记
2. 预留 newChat prompt 和 groupNudge/continueNudge
3. 从 `messages.reverse()` (最新→最旧) 遍历
4. 每条: `chatMessage = Message.fromPromptAsync(promptManager.preparePrompt(prompt))`
5. `chatCompletion.add(chatMessage)` → 内部自动检查 budget，超限则停止

---

## 四、各种提示词的注入时机与位置

### 1. Story String (script.js:4663-4676)

- **构建**: `renderStoryString(storyStringParams)` (power-user.js:2234)
  - Handlebars 模板引擎编译用户的 `context.story_string` 模板
  - 参数: `{description, personality, persona, scenario, system, wiBefore, wiAfter, ...}`
- **注入时机**:
  - 位置 `IN_PROMPT` (默认): 拼接为 `combinedStoryString`，直接位于 prompt 开头
  - 位置 `IN_CHAT`: 通过 `setExtensionPrompt(inject_ids.STORY_STRING, combinedStoryString, IN_CHAT, depth)` 作为深度注入
  - OAI 路径: 不走这个机制，由 Prompt Manager 分别处理各字段

### 2. System Prompt (script.js:4628-4638, sysprompt.js)

- **数据来源**: `power_user.sysprompt.content`
- **非 OAI**: 作为 `system` 参数传入 `renderStoryString()`，在模板中用 `{{system}}` 渲染
  - 如果 `power_user.prefer_character_prompt` 且角色卡片有 system 字段，会合并: `substituteParams(system, { original: sysprompt })`
- **OAI**: 由 Prompt Manager 作为 `main` 标识符的 system role message 注入
- **注入时机**: 在 World Info 扫描之后，上下文填充之前

### 3. Jailbreak / Post-History (script.js:4689-4706)

- **数据来源**: `power_user.sysprompt.post_history`
- **非 OAI**: 直接作为 `is_user: true` 消息 「假用户消息」 注入到 `coreChat` 末尾
  - Continue 模式则 splice 到倒数第二条
  - 会偏移 `injectedIndices` 索引
- **OAI**: 由 Prompt Manager 作为 `jailbreak` 标识符的 system message 注入
  - 注入顺序在 `nsfw` 之后、user-relative prompts 之前 (openai.js:1246)
- **注入时机**: doChatInject 之后，上下文填充之前

### 4. World Info (script.js:4576, world-info.js)

- **扫描时机**: 消息预处理之后，上下文填充之前
- **构建**: `getWorldInfoPrompt(chatForWI, this_max_context, dryRun, globalScanData)`
- **产物**:
  - `worldInfoString` — 纯文本版本 (用于调试显示)
  - `worldInfoBefore` — 应放在 story string 开头的内容
  - `worldInfoAfter` — 应放在 story string 末尾的内容
  - `worldInfoDepth` — 需要深度注入的条目 (通过 `flushWIInjections()` → `setExtensionPrompt`)
  - `worldInfoExamples` — 注入到 message examples 的条目
- **注入**: worldInfoBefore/After 作为渲染参数传入 story string 模板

### 5. Depth Prompt (角色卡深度提示, script.js:4413-4427)

- **数据来源**: 角色卡 `data.extensions.depth_prompt` 字段 (深度/角色/文本)
- **群聊模式**: 遍历 `getGroupDepthPrompts()` 批量注入
- **注入**: `setExtensionPrompt(IN_CHAT, depthPromptDepth, depthPromptRole)`
- **深度概念**: 距离最后一条消息的距离
  - depth=0 → 最后一条消息之后
  - depth=1 → 倒数第二条之后

### 6. Extension Prompts (A/N, Summary, Smart Context, Vectors, 等)

- **机制**: `setExtensionPrompt(key, value, position, depth, role)`
- **位置类型**:
  - `IN_PROMPT` (0) — 在 story string 内的 `{{afterScenarioAnchor}}` 位置
  - `IN_CHAT` (1) — 注入到对话消息中指定深度
  - `BEFORE_PROMPT` (2) — 在 story string 之前 (`beforeScenarioAnchor`)
  - `NONE` (未指定) — 不参与自动注入
- **OAI 路径**: 通过 `populateChatCompletion` 中 knownPrompts 的 `injectToMain()` 注入到 main prompt 的指定位置

### 7. Quiet Prompt (script.js:4322-4326, 4972-4977)

- **用途**: 静默生成 (如 /sysgen、内部 RAG 查询等)
- **第一阶段 (4322)**: 预处理，`substituteParams`
- **第二阶段 (4972)**: 深度 0 追加到最后一条消息
  - 非 Instruct: 直接 `\n{quiet_prompt}`
  - Instruct: 通过 `formatInstructModeChat` 格式化
- **OAI 路径**: 作为 `quietPrompt` 标识符，注入到 control prompts 末尾

### 8. Message Examples (script.js:4901-4911, 4557-4603)

- **解析**: `parseMesExamples(mesExamples, isInstruct)` (行 4557)
- **WI 注入**: `worldInfoExamples` 合并到 examples 数组 (行 4580-4596)
- **Instruct 格式化**: `formatInstructModeExamples()` 将对话格式转为 instruct 格式 (行 4601-4603)
- **注入时机**: 在对话消息填充后
- **Pin 机制**: `power_user.pin_examples` 强制最先注入，不参与 token 预算竞争

### 9. Persona Description (script.js:4624-4625 引用)

- **来源**: `addPersonaDescriptionExtensionPrompt()`
- **位置**: 根据 `power_user.persona_description_position` 决定
  - `IN_PROMPT` → 在 story string 中作为 `{{persona}}` 渲染
  - `IN_CHAT` → 通过 `setExtensionPrompt` 注入到指定深度

### 10. User Alignment Message (script.js:4759-4776)

- **非 OAI**: 在 Instruct 模式下，如果最后一条消息不是用户消息，插入格式化后的 alignment 消息
- **时机**: 消息填充完成之后

### 11. CFG Prompt (Classifier-Free Guidance, script.js:5088-5108)

- **深度 0**: 追加到最后一条 `mesSend` 消息末尾
- **其他深度**: 作为 `extensionPrompt` 注入到对应深度的消息

---

## 五、关于 `extensionPrompt` 注入机制

核心是 `doChatInject()` 函数 (script.js:4684-4687)：

```
doChatInject(coreChat, isContinue) → injectedIndices[]
```

- 收集所有通过 `setExtensionPrompt(IN_CHAT)` 注册的提示词
- 按深度映射到 coreChat 中的位置
- 返回 `injectedIndices` 数组，标记哪些索引被注入
- 在 Text Completion 路径中，这些 injected 消息**先于**普通消息被预填入上下文 (行 4821-4841)

---

## 六、关键数据结构

```
coreChat       — 过滤后的对话数组 (排除 system 消息, 保留包含工具调用的)
chat2          — 格式化后的对话字符串数组 (从旧到新，索引 0=最早)
mesSend        — {message: string, extensionPrompts: string[]}[]
                 最终 prompt 中的对话段
arrMes         — 实际被填入上下文的 chat2 子集
injectedIndices — arrMes 中被注入 extension prompts 的索引
```

---

## 七、核心观察

1. **逐条填充，Token 精确控制**: 每条消息在填充前都经过 `getTokenCountAsync()` 计算，不是简单的「取最近 N 条」

2. **注入优先级逆序**: Injected messages 优先于普通对话消息填充，这意味着深度提示即使位于较远位置也能被保留

3. **两套独立路径**: OAI Chat Completion 和 Text Completion 的上下文管理机制完全不同。OAI 使用 Prompt Manager 的 Token Budget 抽象，Text Completion 更接近原始的 prompt 字符串拼接

4. **Stage 化的事件系统**: 每个关键阶段都有 `eventSource.emit`，允许扩展通过事件拦截和修改 prompt

5. **Prompt Manager 的深度**: OAI 路径中的 Prompt Manager (openai.js) 是 SillyTavern 的高级功能，支持用户自定义 prompt 的顺序、角色、注入位置，通过可视化 UI 管理
