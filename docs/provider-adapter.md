# Provider 适配层设计

## 问题

agentcore 需要调用 LLM，但不同 provider 的 API 格式不兼容。
核心逻辑（tool call 循环、记忆管理、soul.md 拼接）不应为每个 provider 重写。

## 方案：适配器模式

```
agentcore (只认内部格式)
    │
    ├── openaiAdapter ──→ openai 兼容 API (deepseek, groq, openrouter……)
    └── geminiAdapter ──→ Google Gemini API
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

---

## 3. 适配器接口

```go
type Provider interface {
    Chat(ctx context.Context, req *LLMRequest) (*LLMResponse, error)
}
```

两个实现：

```
openaiImpl{
    BaseURL string   // deepseek: https://api.deepseek.com/v1
    APIKey  string
    Model   string   // "deepseek-chat"
    Client  *http.Client
}

geminiImpl{
    BaseURL string   // https://generativelanguage.googleapis.com/v1beta
    APIKey  string
    Model   string   // "gemini-2.0-flash"
    Client  *http.Client
}
```

---

## 4. 文件结构

```
internal/agentcore/
├── llm.go          ← Chat() 入口，tool call 循环
├── provider.go     ← Provider 接口 + LLMRequest/LLMResponse 类型定义
├── openai.go       ← openaiImpl：内部格式 ↔ OpenAI 格式
└── gemini.go       ← geminiImpl：内部格式 ↔ Gemini 格式
```

---

## 5. 总结

- 内部格式 5 个类型 + 1 个接口定义，只包含 half-pi 实际用的字段
- 每个适配器 2 个动作：Convert + Parse
- 加新 provider = 加一个适配器文件，核心逻辑不动
- 不引入任何第三方 LLM SDK 依赖
