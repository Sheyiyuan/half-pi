// 列出当前会话组可见的技能摘要，不返回正文。
package local

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
)

// listSkillsMaxBytes 限制单次输出规模，约覆盖 40 条摘要。
const listSkillsMaxBytes = 4096

func init() {
	executor.Register(executor.Tool{
		Name:        "list_skills",
		Description: "List skills available in the current workspace with name, description and tags. Use view_skill to load the full content of one skill.",
		Parameters:  &executor.ObjectSchema{},
		Execute: func(ctx context.Context, _ json.RawMessage) *executor.ToolResult {
			store := skillStoreFromContext(ctx)
			if store == nil {
				return &executor.ToolResult{Error: "skill system is not initialized"}
			}
			meta, _ := executor.LifecycleMetaFromContext(ctx)
			summaries := store.SummariesForGroup(meta.GroupID)
			if len(summaries) == 0 {
				return &executor.ToolResult{Success: true, Output: "no skills available"}
			}

			var buf strings.Builder
			for i, summary := range summaries {
				line := formatSkillSummary(summary)
				if buf.Len()+len(line) > listSkillsMaxBytes {
					fmt.Fprintf(&buf, "... %d more skills omitted\n", len(summaries)-i)
					break
				}
				buf.WriteString(line)
			}
			return &executor.ToolResult{Success: true, Output: buf.String()}
		},
	})
}

func formatSkillSummary(summary skill.SkillSummary) string {
	var line strings.Builder
	line.WriteString(summary.Name)
	line.WriteString(" — ")
	line.WriteString(summary.Description)
	if len(summary.Tags) > 0 {
		line.WriteString(" [")
		line.WriteString(strings.Join(summary.Tags, ", "))
		line.WriteString("]")
	}
	if summary.Always {
		line.WriteString(" [始终激活]")
	}
	line.WriteString("\n")
	return line.String()
}
