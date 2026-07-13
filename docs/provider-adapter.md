# Provider 适配层设计

## 问题

agentcore 需要调用 LLM，但不同 provider 的 API 格式不兼容。
核心逻辑（tool call 循环、记忆管理、soul.md 拼接）不应为每个 provider 重写。

## 方案：适配器模式

```
agentcore (只认内部格式)
    │
    ├── openaiAdapter ──→ OpenAI 兼容 API (deepseek, groq, openrouter……)
    ├── geminiAdapter ──→ Google Gemini API
    └── anthropicAdapter ──→ Anthropic Claude API
```

每个适配器做且只做两件事：
1. `Convert(内部请求) → 厂商格式`
2. `Parse(厂商响应) → 内部响应`

---

## 1. 内部格式

agentcore 唯一认识的格式。设计原则：**只包含 half-pi 实际用到的字段**，不追求覆盖全部 API。

```go
// ── 角色 ──
type Role string
const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleTool      Role = "tool"
)

// ── 消息 ──
type Message struct {
    Role    Role
    Content string      // 纯文本（不处理多模态）
    ToolID  string      // tool 角色专用
}

// ── 工具定义（JSON Schema 描述参数）──
type ToolDef struct {
    Name        string
    Description string
    Parameters  *jsonschema.Schema   // Go 里用一个结构体描述 JSON Schema
}

// ── 工具调用（LLM 返回的）──
type ToolCall struct {
    ID   string
    Name string
    Args string   // JSON
}

// ── 请求 ──
type LLMRequest struct {
    System   string         // soul.md 内容（拼成 system message）
    Messages []Message
    Tools    []ToolDef
    Temperature float32
    MaxTokens   int
}

// ── 响应 ──
type LLMResponse struct {
    Content   string
    ToolCalls []ToolCall
    Usage     Usage
}

type Usage struct {
    InputTokens  int
    OutputTokens int
}
```

---

## 2. OpenAI 格式 vs Gemini 格式 对照

### 2.1 请求结构

| 概念 | 内部格式 | OpenAI (deepseek) | Gemini |
|------|---------|-------------------|--------|
| system prompt | `System string` | `{role: "system", content: "…"}` 放 messages 里 | `system_instruction: {parts: [{text: "…"}]}` 独立字段 |
| 用户消息 | `{Role: user, Content: "…"}` | `{role: "user", content: [{type: "text", text: "…"}]}` | `{role: "user", parts: [{text: "…"}]}` |
| 助手消息 | `{Role: assistant, Content: "…"}` | `{role: "assistant", content: "…"}` | `{role: "model", parts: [{text: "…"}]}` |
| tool 结果 | `{Role: tool, Content: "…", ToolID: "…"}` | `{role: "tool", content: "…", tool_call_id: "…"}` | `{role: "function", parts: [{functionResponse: {name: "…", response: {result: "…"}}}]}` |
| 工具定义 | `ToolDef[]` | `tools: [{type: "function", function: {name, description, parameters}}]` | `tools: [{function_declarations: [{name, description, parameters}]}]` |
| 工具调用 | `ToolCall[]` | `tool_calls: [{id, type: "function", function: {name, arguments}}]` | `functionCall: {name, args}` |

### 2.2 关键差异点

| 差异 | OpenAI | Gemini | 适配器处理 |
|------|--------|--------|-----------|
| system 位置 | messages 数组的第一个 | 独立字段 `system_instruction` | OpenAI：拼到 messages[0]；Gemini：取出来放 system_instruction |
| tool 角色名 | `role: "tool"` | `role: "function"` | 映射角色名 |
| assistant tool_call 结构 | `role: "assistant"` 消息里带 `tool_calls` 数组 | `role: "model"` 消息里带 `functionCall` 对象（每消息最多一个） | OpenAI：一个 assistant 消息可能有多个 tool_calls；Gemini：一个 model 消息可能只返回一个 functionCall，需要拆/合 |
| 消息数组 vs contents | 线性 messages 数组 | `contents` + `system_instruction` 分开 | Gemini：system 抽出来，工具调用历史需要按 LLM 格式重组角色名 |
| 字段命名 | camelCase | camelCase（大体一致） | 不需要特别处理 |

### 2.3 转内部格式映射逻辑

```
── OpenAI 请求构建（内部 → OpenAI）──

system message → messages[0] = {role: "system", content: system}
user/assistant → 角色名直接映射
tool 消息      → {role: "tool", tool_call_id: toolID, content: content}
tools          → {type: "function", function: {name, description, parameters}}

── OpenAI 响应解析（OpenAI → 内部）──

content        → response.Content
tool_calls     → for each: response.ToolCalls.append({ID, Name, Args})


── Gemini 请求构建（内部 → Gemini）──

system         → system_instruction.parts[0].text
user/assistant → 角色映射: assistant → model
tool 消息      → role: function, parts: [{functionResponse: {name, response: {result}}}]
tools          → function_declarations: [{name, description, parameters}]

── Gemini 响应解析（Gemini → 内部）──

text           → candidates[0].content.parts[0].text
functionCall   → for each candidate: 
                 遍历 parts，如果 type == functionCall，拆成 ToolCall
                 注意：一个消息可能有多个 parts，部分是 text 部分是 functionCall
```

### 2.4 Anthropic (Claude)

| 概念 | 内部格式 | Anthropic |
|------|---------|-----------|
| system prompt | `System string` | `system: "…"` 独立字段（支持纯文本或 block 数组） |
| 用户消息 | `{Role: user, Content: "…"}` | `{role: "user", content: [{type: "text", text: "…"}]}` content 永远是 block 数组 |
| 助手消息 | `{Role: assistant, Content: "…"}` | `{role: "assistant", content: [{type: "text", text: "…"}]}` |
| tool 结果 | `{Role: tool, Content: "…", ToolID: "…"}` | `{role: "user", content: [{type: "tool_result", tool_use_id: "…", content: "…"}]}` |
| 工具定义 | `ToolDef[]` | `tools: [{name, description, input_schema}]` |
| 工具调用 | `ToolCall[]` | 在 assistant 的 content block 里：`{type: "tool_use", id, name, input}` |

#### Anthropic 格式独有特征

1. **content 永远是 block 数组** —— 不能传纯字符串，每条消息的 content 都是 `[]Block`。OpenAI 可以传字符串，Gemini 传 `parts[]`。适配器要做对应的序列化。

2. **tool 调用在 content block 里** —— 不像 OpenAI 有独立的 `tool_calls` 字段，Anthropic 的 tool_use 混在 content 数组里，和 text block 平级。解析时需要遍历 content 区分 `type: "text"` 和 `type: "tool_use"`。

3. **tool 结果用 user 角色** —— 没有 `role: "tool"`，而是 `role: "user"` 配合 `content: [{type: "tool_result", tool_use_id: "…"}]`。这对内部格式映射影响最大——给 LLM 发消息时，tool 角色的消息必须转为 user 角色的 tool_result block。

4. **system 字段是独立顶层字段**（类似 Gemini），不能放在 messages 里。

#### 映射逻辑

```
── Anthropic 请求构建（内部 → Anthropic）──

system         → 独立字段 system: "…"
user           → role: "user", content: [{type: "text", text: "…"}]
assistant      → role: "assistant", content: 
                  如果无 tool_call：[{type: "text", text: "…"}]
                  有 tool_call：拆成 text block + tool_use blocks
tool 消息      → role: "user", content: [{type: "tool_result", tool_use_id, content: "…"}]
tools          → {name, description, input_schema}
                 注意 Anthropic 不支持 type: "function" 外层包装

── Anthropic 响应解析（Anthropic → 内部）──

content blocks → 遍历 content 数组，type: "text" 拼到 Content，
                 type: "tool_use" 拆成 ToolCall
max_tokens     → 必填字段（deepseek 不用传，但 Anthropic 报了错才给结果）
```

---

## 3. 三大 provider 格式对比总表

| 特性 | OpenAI 兼容 | Gemini | Anthropic |
|------|------------|--------|-----------|
| system prompt | messages[0].role=system | 独立字段 system_instruction | 独立字段 system |
| content 格式 | 字符串 / block 数组 | parts[] 数组 | block[] 数组 |
| 助手 role 名 | "assistant" | "model" | "assistant" |
| tool 调用位置 | 独立的 `tool_calls` 字段 | `functionCall` 在 parts 里 | `type: "tool_use"` 在 content block 里 |
| tool 结果角色 | "tool" | "function" | "user"（tool_result block） |
| 工具定义包装 | `{type: "function", function: …}` | `{function_declarations: […]}` | 直接 `{name, description, …}` |
| 必填字段 | 无特殊 | 无特殊 | `max_tokens` 必填 |

```go
type Provider interface {
    Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error)
}
```

三个实现：

```
openai   → deepseek / groq / openrouter 等
gemini   → Google Gemini
anthropic → Anthropic Claude
```

---

## 4. 文件结构

```
internal/llm/                    ← 独立包，只负责 LLM 通信
├── provider.go                  ← Provider 接口 + LLMRequest/LLMResponse
├── openai.go                    ← OpenAI 兼容适配器
├── gemini.go                    ← Gemini 适配器
└── anthropic.go                 ← Anthropic 适配器

internal/agentcore/              ← 业务逻辑，import llm
├── core.go                      ← 主循环、tool call 循环
├── soul.go                      ← soul.md 加载
├── session.go                   ← 上下文管理
└── dispatch.go                  ← 设备选择
```

依赖方向：`agentcore → llm`，llm 不依赖 agentcore 任何东西。

---

## 5. 总结

- 内部格式 5 个类型 + 1 个接口定义，只包含 half-pi 实际用的字段
- 每个适配器 2 个动作：Convert + Parse
- 加新 provider = 加一个适配器文件，核心逻辑不动
- 不引入任何第三方 LLM SDK 依赖
