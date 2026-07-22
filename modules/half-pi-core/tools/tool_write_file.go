// 创建或覆盖文件。写操作每次执行默认需用户确认。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "write_file",
		Description: "创建或覆盖文件，写入完整内容。需要覆盖已有文件时使用此工具",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "path", Type: "string", Description: "要写入的文件路径"},
				{Name: "content", Type: "string", Description: "要写入的完整内容", Review: executor.ReviewRedact},
			},
			Required: []string{"path", "content"},
		},
		DefaultConfirm: true,
		Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
			var p struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("failed to parse args: %v", err)}
			}
			if p.Path == "" {
				return &executor.ToolResult{Error: "path cannot be empty"}
			}

			if err := os.WriteFile(p.Path, []byte(p.Content), 0644); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("failed to write: %v", err)}
			}

			return &executor.ToolResult{
				Success: true,
				Output:  fmt.Sprintf("已写入: %s (%d 字节)", p.Path, len(p.Content)),
				Data: map[string]any{
					"path":   p.Path,
					"bytes":  len(p.Content),
					"action": "created",
				},
			}
		},
	})
}
