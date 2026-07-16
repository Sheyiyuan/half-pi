# 工具开发指南

工具放在 `tools/` 下，每个工具一个 `tool_<name>.go` 文件，通过 `init()` 自注册。

## 工具结构

```go
func init() {
    executor.Register(executor.Tool{
        Name:        "tool_name",
        Description: "一句话说明工具用途",
        Parameters: &executor.ObjectSchema{
            Properties: []executor.PropertySchema{
                {Name: "arg1", Type: "string", Description: "参数说明"},
            },
            Required: []string{"arg1"},
        },
        DefaultConfirm: false, // true 则每次调用需用户确认
        Check:          nil,   // 安全检查函数（可选）
        Execute:        toolExecute,
    })
}
```

## 返回结果

工具执行后返回 `ToolResult`，包含两个输出字段：

| 字段 | 用途 | 示例 |
|------|------|------|
| `Output` | LLM 可读的文本描述 | `"已写入: main.go (1024 字节)"` |
| `Data` | 供 Face 渲染的结构化数据（可选） | `{"path":"main.go","bytes":1024}` |
| `Error` | 错误信息（LLM 可读） | `"文件不存在"` |

工具应优先填充 `Data`，`Output` 作为 LLM 的文本摘要。
Face 渲染时先检查 `Data`，有则用 JSON 渲染，无则 fallback 到 `Output`。

```go
return &executor.ToolResult{
    Success: true,
    Output:  "已编辑: main.go (1 处修改)",
    Data: map[string]any{
        "path":       "main.go",
        "diff":       "@@ -15,3 +15,4 @@...",
        "old_string": "旧内容",
        "new_string": "新内容",
    },
}
```

## 添加新工具

1. 在 `tools/` 下新建 `tool_<name>.go`
2. 在 `init()` 中注册 Tool
3. 实现 Execute 函数
