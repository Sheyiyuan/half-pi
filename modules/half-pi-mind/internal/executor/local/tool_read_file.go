// 读取文件内容。返回纯文本，不支持二进制。
package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "read_file",
		Description: "读取文件内容",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "path", Type: "string", Description: "要读取的文件路径"},
			},
			Required: []string{"path"},
		},
		Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
			var p struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}
			}
			if p.Path == "" {
				return &executor.ToolResult{Error: "path 不能为空"}
			}
			data, err := os.ReadFile(p.Path)
			if err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("读取失败: %v", err)}
			}
			return &executor.ToolResult{Success: true, Output: string(data)}
		},
	})
}
