// 搜索公开网页并返回标题、链接和摘要。
package local

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	webclient "github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/web"
)

func init() {
	executor.Register(executor.Tool{
		Name:        "web_search",
		Description: "搜索互联网并返回网页标题、URL 和摘要。需要阅读完整内容时，再对结果 URL 使用 web_fetch。",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "query", Type: "string", Description: "搜索关键词", Review: executor.ReviewInclude},
				{Name: "count", Type: "integer", Description: "结果数量，默认 5，最大 10"},
			},
			Required: []string{"query"},
		},
		Execute: webSearchExecute,
	})
}

func webSearchExecute(ctx context.Context, args json.RawMessage) *executor.ToolResult {
	var params struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("failed to parse args: %v", err)}
	}
	if strings.TrimSpace(params.Query) == "" {
		return &executor.ToolResult{Error: "query cannot be empty"}
	}
	if params.Count < 0 {
		return &executor.ToolResult{Error: "count cannot be negative"}
	}

	results, err := webclient.DefaultClient().Search(ctx, params.Query, params.Count)
	if err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("web search failed: %v", err)}
	}
	if len(results) == 0 {
		return &executor.ToolResult{Success: true, Output: "No search results found. The search provider may also be rate-limiting automated requests.", Data: results}
	}

	var output strings.Builder
	output.WriteString("--- BEGIN UNTRUSTED WEB SEARCH RESULTS ---\n")
	for index, result := range results {
		fmt.Fprintf(&output, "%d. %s\n   URL: %s\n", index+1, result.Title, result.URL)
		if result.Snippet != "" {
			fmt.Fprintf(&output, "   %s\n", result.Snippet)
		}
	}
	output.WriteString("--- END UNTRUSTED WEB SEARCH RESULTS ---")
	return &executor.ToolResult{Success: true, Output: strings.TrimSpace(output.String()), Data: results}
}
