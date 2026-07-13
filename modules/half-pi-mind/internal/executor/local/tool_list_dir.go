// 列出目录内容。输出格式：权限 大小  路径。按名称排序，目录末尾带 /。
package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "list_dir",
		Description: "列出目录内容",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "path", Type: "string", Description: "目录路径，默认为当前目录"},
			},
		},
		Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
			var p struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}
			}
			dir := p.Path
			if dir == "" {
				dir = "."
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("读取目录失败: %v", err)}
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Name() < entries[j].Name()
			})
			sep := fmt.Sprint(strings.Repeat("-", 40))
			var buf strings.Builder
			buf.WriteString(sep + "\n")
			for _, entry := range entries {
				info, _ := entry.Info()
				fmt.Fprintf(&buf, "%s %8d  %s\n", info.Mode().String(), info.Size(), filepath.Join(dir, entry.Name()))
			}
			buf.WriteString(sep + "\n")
			return &executor.ToolResult{Success: true, Output: buf.String()}
		},
	})
}
