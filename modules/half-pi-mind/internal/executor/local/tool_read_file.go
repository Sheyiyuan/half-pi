// 读取文件内容，支持指定行范围。
// 返回带行号的文本，方便 LLM 精确定位。
// 内置行数、字符数双上限，防止超长文件或超长行撑爆上下文。
// 被截断的长行可通过 char_offset 继续读取。
package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor"
)

const (
	maxReadLines = 2000
	maxReadChars = 100000
)

func init() {
	executor.Register(executor.Tool{
		Name:        "read_file",
		Description: "读取文件内容，可选指定行范围（offset 起始行，limit 行数）。长行被截断时用 char_offset 继续",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "path", Type: "string", Description: "要读取的文件路径"},
				{Name: "offset", Type: "number", Description: "起始行号（1-indexed），默认 1"},
				{Name: "limit", Type: "number", Description: "读取行数，默认 0 表示读全部（受系统上限约束）"},
				{Name: "char_offset", Type: "number", Description: "字符偏移（0-indexed），对 offset 行的第 char_offset 个字符开始读。仅用于继续读取被截断的长行"},
			},
			Required: []string{"path"},
		},
		Execute: readFileExecute,
	})
}

func readFileExecute(ctx context.Context, args json.RawMessage) *executor.ToolResult {
	var p struct {
		Path       string  `json:"path"`
		Offset     float64 `json:"offset"`
		Limit      float64 `json:"limit"`
		CharOffset float64 `json:"char_offset"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("failed to parse args: %v", err)}
	}
	if p.Path == "" {
		return &executor.ToolResult{Error: "path cannot be empty"}
	}

	data, err := os.ReadFile(p.Path)
	if err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("failed to read: %v", err)}
	}

	lines := splitLines(string(data))
	total := len(lines)

	offset := int(p.Offset)
	if offset <= 0 {
		offset = 1
	}
	limit := int(p.Limit)
	if limit <= 0 {
		limit = maxReadLines
	}
	if limit > maxReadLines {
		limit = maxReadLines
	}
	charOffset := int(p.CharOffset)
	if charOffset < 0 {
		charOffset = 0
	}

	start := offset - 1 // 转为 0-indexed
	if start >= total {
		return &executor.ToolResult{Error: fmt.Sprintf("offset %d exceeds file length %d", offset, total)}
	}

	var out strings.Builder
	var totalChars int
	actualEnd := start
	truncByChars := false

	for i := start; i < total; i++ {
		line := lines[i]
		charsRemaining := maxReadChars - totalChars

		// char_offset 只在第一行生效
		if i == start && charOffset > 0 {
			if charOffset >= len(line) {
				charOffset = 0
				continue // 跳完 charOffset 发现已经越过了整行
			}
			line = line[charOffset:]
		}

		lineLen := len(line)

		// 字符上限触发截断
		if lineLen > charsRemaining {
			truncByChars = true
			// 截断输出 + 标记
			out.WriteString(formatLine(i+1, line[:charsRemaining]))
			out.WriteString("…[截断]\n")
			totalChars += charsRemaining
			actualEnd = i + 1
			break
		}

		out.WriteString(formatLine(i+1, line))
		totalChars += lineLen
		actualEnd = i + 1

		// 行数上限触发
		if i-start+1 >= limit {
			break
		}
	}

	// 元信息头
	header := fmt.Sprintf("[文件: %s  总行数: %d  显示: %d-%d", p.Path, total, start+1, actualEnd)
	if actualEnd < total {
		if truncByChars {
			header += fmt.Sprintf("  已达字符上限 %d", maxReadChars)
		} else {
			header += fmt.Sprintf("  已达行数上限 %d", maxReadLines)
		}
	}
	header += "]\n"

	var result strings.Builder
	result.WriteString(header)
	result.WriteString("\n")

	if total == 0 {
		result.WriteString("(空文件)\n")
	} else {
		result.WriteString(out.String())
	}

	if actualEnd < total {
		nextOffset := actualEnd + 1
		if truncByChars {
			result.WriteString(fmt.Sprintf("\n[文件未读完。用 offset=%d, char_offset=%d 继续（总字符数: %s）]",
				actualEnd, totalChars, formatCharCount(lines[actualEnd-1])))
		} else {
			result.WriteString(fmt.Sprintf("\n[文件未读完，剩余 %d 行。用 offset=%d 继续]",
				total-actualEnd, nextOffset))
		}
	}

	return &executor.ToolResult{Success: true, Output: result.String()}
}

func formatLine(lineno int, content string) string {
	return fmt.Sprintf("%6d  %s\n", lineno, content)
}

func formatCharCount(line string) string {
	n := len(line)
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 1000000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
