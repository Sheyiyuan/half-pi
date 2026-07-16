// 精确编辑文件：查找 old_string 并替换为 new_string。
// old_string 必须在文件中唯一匹配，否则返回错误。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "edit_file",
		Description: "精确修改文件中唯一匹配的 old_string 为 new_string。old_string 不唯一时会列出所有匹配行，请扩大上下文使其唯一",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "path", Type: "string", Description: "要编辑的文件路径"},
				{Name: "old_string", Type: "string", Description: "要被替换的原文，必须在文件中唯一出现"},
				{Name: "new_string", Type: "string", Description: "替换后的新内容"},
			},
			Required: []string{"path", "old_string", "new_string"},
		},
		DefaultConfirm: true,
		Execute:        editFileExecute,
	})
}

func editFileExecute(ctx context.Context, args json.RawMessage) *executor.ToolResult {
	var p struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("failed to parse args: %v", err)}
	}
	if p.Path == "" || p.OldString == "" {
		return &executor.ToolResult{Error: "path and old_string cannot be empty"}
	}

	data, err := os.ReadFile(p.Path)
	if err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("failed to read file: %v", err)}
	}

	content := string(data)
	count := strings.Count(content, p.OldString)

	if count == 0 {
		return &executor.ToolResult{
			Error: "old_string not found in file. Re-read the file with read_file to verify current content",
		}
	}

	if count > 1 {
		var report strings.Builder
		report.WriteString(fmt.Sprintf("old_string 匹配到 %d 处:\n\n", count))
		for i, line := range strings.Split(content, "\n") {
			if strings.Contains(line, p.OldString) {
				report.WriteString(fmt.Sprintf("  第 %d 行: %s\n", i+1, strings.TrimSpace(line)))
			}
		}
		report.WriteString("\n请扩大 old_string 的上下文使其唯一匹配。")
		return &executor.ToolResult{Error: report.String()}
	}

	newContent := strings.Replace(content, p.OldString, p.NewString, 1)
	if err := os.WriteFile(p.Path, []byte(newContent), 0644); err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("failed to write: %v", err)}
	}

	diff := buildDiff(content, p.OldString, p.NewString)

	return &executor.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("已编辑: %s (%d 处修改)", p.Path, 1),
		Data: map[string]any{
			"path":       p.Path,
			"diff":       diff,
			"old_string": p.OldString,
			"new_string": p.NewString,
		},
	}
}

// buildDiff 为单次替换构造简略差异，格式类似 unified diff。
// 替换位置前后各显示 2 行上下文。
func buildDiff(content, oldStr, newStr string) string {
	idx := strings.Index(content, oldStr)
	if idx < 0 {
		return ""
	}
	before := content[:idx]
	after := content[idx+len(oldStr):]

	oldLines := strings.Split(oldStr, "\n")
	newLines := strings.Split(newStr, "\n")

	// 计算 oldStr 起始行号（0-indexed）
	lineNum := 0
	for _, line := range strings.Split(before, "\n") {
		if line == "" && lineNum > 0 {
			// last newline before empty
		}
		lineNum++
	}
	lineNum-- // 转为 0-indexed

	// 取上下文（前 2 行）
	ctxBefore := strings.Split(before, "\n")
	ctxStart := lineNum - 2
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxLines := ctxBefore[ctxStart:lineNum]

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", ctxStart+1, len(ctxBefore)-ctxStart, ctxStart+1, len(ctxBefore)-ctxStart-len(oldLines)+len(newLines)))
	for _, l := range ctxLines {
		buf.WriteString(" " + l + "\n")
	}
	for _, l := range oldLines {
		buf.WriteString("-" + l + "\n")
	}
	for _, l := range newLines {
		buf.WriteString("+" + l + "\n")
	}
	// 后 2 行上下文
	afterLines := strings.Split(after, "\n")
	ctxEnd := 2
	if ctxEnd > len(afterLines) {
		ctxEnd = len(afterLines)
	}
	for _, l := range afterLines[:ctxEnd] {
		buf.WriteString(" " + l + "\n")
	}
	return strings.TrimRight(buf.String(), "\n")
}
