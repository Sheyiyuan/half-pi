// 获取公开网页并提取适合模型阅读的文本。
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
		Name:        "web_fetch",
		Description: "获取一个公开 HTTP(S) 网页并提取可读文本。用于阅读 web_search 找到的页面；不支持登录、脚本渲染或文件下载。",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "url", Type: "string", Description: "要读取的公开网页 URL", Review: executor.ReviewInclude},
			},
			Required: []string{"url"},
		},
		Check:   webFetchCheck,
		Execute: webFetchExecute,
	})
}

func webFetchCheck(args json.RawMessage) (executor.Decision, string) {
	var params struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return executor.DecisionDeny, fmt.Sprintf("invalid web_fetch arguments: %v", err)
	}
	if err := webclient.ValidateURL(params.URL); err != nil {
		return executor.DecisionDeny, err.Error()
	}
	return executor.DecisionAllow, ""
}

func webFetchExecute(ctx context.Context, args json.RawMessage) *executor.ToolResult {
	var params struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("failed to parse args: %v", err)}
	}
	if strings.TrimSpace(params.URL) == "" {
		return &executor.ToolResult{Error: "url cannot be empty"}
	}

	page, err := webclient.DefaultClient().Fetch(ctx, params.URL)
	if err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("web fetch failed: %v", err)}
	}

	var output strings.Builder
	if page.Title != "" {
		fmt.Fprintf(&output, "Title: %s\n", page.Title)
	}
	fmt.Fprintf(&output, "URL: %s\nContent-Type: %s\n", page.URL, page.ContentType)
	if page.Truncated {
		output.WriteString("Content was truncated to the tool output limit.\n")
	}
	output.WriteString("\n--- BEGIN UNTRUSTED WEB CONTENT ---\n")
	output.WriteString(page.Content)
	output.WriteString("\n--- END UNTRUSTED WEB CONTENT ---")
	return &executor.ToolResult{Success: true, Output: output.String(), Data: page}
}
