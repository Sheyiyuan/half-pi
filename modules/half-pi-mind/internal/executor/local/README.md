# 添加新工具

在 `internal/executor/local/` 下新建 `tool_<name>.go`，按以下模板填写：

```go
// <一句话说明工具用途>
package local

import (
    "context"
    "encoding/json"
    "fmt"
    "github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor"
)

func init() {
    executor.Register(executor.Tool{
        Name:        "<name>",
        Description: "<LLM 理解工具用途的说明>",
        Parameters: &executor.ObjectSchema{
            Properties: []executor.PropertySchema{
                {Name: "<参数名>", Type: "string", Description: "<参数说明>"},
            },
            Required: []string{"<必填参数名>"},
        },
        Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
            var p struct { ... }
            if err := json.Unmarshal(args, &p); err != nil {
                return &executor.ToolResult{Error: "参数解析失败"}
            }
            return &executor.ToolResult{Success: true, Output: "结果"}
        },
    })
}
```

注册后立即可用，无需修改其他文件。
