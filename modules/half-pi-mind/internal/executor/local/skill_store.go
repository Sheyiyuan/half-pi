// 技能仓库的上下文注入，供 view_skill / list_skills 读取。
package local

import (
	"context"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
)

type skillStoreKey struct{}

// WithSkillStore 将技能仓库绑定到工具执行上下文。
func WithSkillStore(ctx context.Context, store *skill.Store) context.Context {
	if store == nil {
		return ctx
	}
	return context.WithValue(ctx, skillStoreKey{}, store)
}

func skillStoreFromContext(ctx context.Context) *skill.Store {
	store, _ := ctx.Value(skillStoreKey{}).(*skill.Store)
	return store
}
