// 文件内容搜索——正则匹配。
package local

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "grep_regex",
		Description: "在文件中用正则表达式搜索。返回匹配行及上下文",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "regex", Type: "string", Description: "正则表达式（Go regexp 语法）"},
				{Name: "path", Type: "string", Description: "搜索目录或文件路径，默认为当前目录"},
				{Name: "include", Type: "string", Description: "glob 过滤文件名，如 \"*.go\"。默认不过滤"},
				{Name: "max_matches", Type: "number", Description: "最大匹配数，默认 200"},
			},
			Required: []string{"regex"},
		},
		Execute: grepRegexExecute,
	})
}

func grepRegexExecute(ctx context.Context, args json.RawMessage) *executor.ToolResult {
	var p struct {
		Regex      string  `json:"regex"`
		Path       string  `json:"path"`
		Include    string  `json:"include"`
		MaxMatches float64 `json:"max_matches"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("failed to parse args: %v", err)}
	}
	if p.Regex == "" {
		return &executor.ToolResult{Error: "regex cannot be empty"}
	}

	re, err := regexp.Compile(p.Regex)
	if err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("invalid regex: %v", err)}
	}

	if p.Path == "" {
		p.Path = "."
	}
	maxMatches := int(p.MaxMatches)
	if maxMatches <= 0 {
		maxMatches = grepMaxMatches
	}

	searchRegex := p.Regex
	results, totalMatches, filesScanned, limited, err := searchFiles(p.Path, p.Include, maxMatches, func(path, content string) []matchResult {
		return matchRegex(path, content, re, searchRegex)
	})
	if err != nil {
		return &executor.ToolResult{Error: err.Error()}
	}

	header := fmt.Sprintf("搜索: regex=%q", p.Regex)
	if p.Include != "" {
		header += fmt.Sprintf(" include=%q", p.Include)
	}
	header += fmt.Sprintf("  匹配: %d 处  扫描文件: %d", totalMatches, filesScanned)
	if limited {
		header += fmt.Sprintf("  [已达上限 %d]", maxMatches)
	}
	header += "\n\n"

	if len(results) == 0 {
		header += "(无匹配)\n"
	}

	return &executor.ToolResult{Success: true, Output: header + strings.Join(results, "\n") + "\n"}
}

func matchRegex(path, content string, re *regexp.Regexp, pattern string) []matchResult {
	lines := splitLines(content)
	matched := make([]bool, len(lines))
	matchCount := 0

	for i, line := range lines {
		if re.MatchString(line) {
			matched[i] = true
			matchCount++
		}
	}

	return []matchResult{{output: formatMatches(path, lines, matched), matchCount: matchCount}}
}
