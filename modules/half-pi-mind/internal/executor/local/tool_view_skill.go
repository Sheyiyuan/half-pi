// 按名称加载技能全文，注入对话上下文。
package local

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
)

// 全局技能仓库，由 main.go 在启动时设置。
var skillStore *skill.Store
var skillStoreMu sync.RWMutex

func init() {
	executor.Register(executor.Tool{
		Name:        "view_skill",
		Description: "View the full content of a skill. Use to load domain-specific knowledge on demand.",
		Parameters: &executor.ObjectSchema{
			Properties: []executor.PropertySchema{
				{Name: "name", Type: "string", Description: "Skill name"},
			},
			Required: []string{"name"},
		},
		Execute: func(ctx context.Context, args json.RawMessage) *executor.ToolResult {
			var p struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return &executor.ToolResult{Error: fmt.Sprintf("failed to parse args: %v", err)}
			}
			if p.Name == "" {
				return &executor.ToolResult{Error: "skill name is required"}
			}
			skillStoreMu.RLock()
			store := skillStore
			skillStoreMu.RUnlock()
			if store == nil {
				return &executor.ToolResult{Error: "skill system is not initialized"}
			}
			meta, _ := executor.LifecycleMetaFromContext(ctx)
			sk, ok := store.GetForGroup(p.Name, meta.GroupID)
			if !ok {
				return &executor.ToolResult{Error: fmt.Sprintf("skill not found: %s", p.Name)}
			}

			return &executor.ToolResult{
				Success: true,
				Output:  fmt.Sprintf("[Skill: %s]\n\n%s", sk.Name, sk.Content),
			}
		},
	})
}

// SetSkillStore 设置全局技能仓库。
func SetSkillStore(s *skill.Store) {
	skillStoreMu.Lock()
	skillStore = s
	skillStoreMu.Unlock()
}
