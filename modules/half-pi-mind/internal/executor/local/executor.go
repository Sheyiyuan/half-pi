// Package local 提供 Mind 本地工具执行器和远程 Hand 路由工具。
package local

import (
	"context"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	_ "github.com/Sheyiyuan/half-pi/modules/half-pi-core/tools"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
)

type LocalExecutor struct {
	bridge  *RemoteBridge
	catalog *executor.Catalog
	skills  *skill.Store
}

// New 创建本地工具执行器。
func New(bridge ...*RemoteBridge) *LocalExecutor {
	var remote *RemoteBridge
	if len(bridge) > 0 {
		remote = bridge[0]
	}
	return &LocalExecutor{
		bridge:  remote,
		catalog: executor.DefaultCatalog(),
	}
}

// NewWithCatalog 使用指定工具目录创建本地工具执行器。
func NewWithCatalog(catalog *executor.Catalog, bridge ...*RemoteBridge) *LocalExecutor {
	if catalog == nil {
		catalog = executor.DefaultCatalog()
	}
	var remote *RemoteBridge
	if len(bridge) > 0 {
		remote = bridge[0]
	}
	return &LocalExecutor{
		bridge:  remote,
		catalog: catalog,
	}
}

// SetSkills 绑定技能仓库，由 view_skill / list_skills 通过上下文读取。
func (e *LocalExecutor) SetSkills(s *skill.Store) {
	e.skills = s
}

// Tools 返回本地可用工具列表。
func (e *LocalExecutor) Tools() []executor.Tool {
	if e.catalog == nil {
		return executor.RegisteredTools()
	}
	return e.catalog.Tools()
}

// Catalog 返回本执行器解析工具所用的目录。
func (e *LocalExecutor) Catalog() *executor.Catalog {
	if e.catalog == nil {
		return executor.DefaultCatalog()
	}
	return e.catalog
}

// PrepareToolContext 注入远程执行桥和技能仓库，但不执行或绕过任何安全检查。
func (e *LocalExecutor) PrepareToolContext(ctx context.Context) context.Context {
	ctx = WithSkillStore(ctx, e.skills)
	if e.bridge != nil {
		ctx = WithRemoteBridge(ctx, e.bridge)
	}
	return ctx
}
