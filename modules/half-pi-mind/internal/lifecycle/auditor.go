package lifecycle

import (
	"context"

	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
)

type securityDecisionStore interface {
	AppendSecurityDecision(context.Context, corelifecycle.AuditMutation) error
}

// StoreAuditor 将安全决策和可靠 outbox 事实提交到同一存储事务。
type StoreAuditor struct {
	store securityDecisionStore
}

// NewStoreAuditor 创建安全决策审计器。
func NewStoreAuditor(store securityDecisionStore) *StoreAuditor {
	return &StoreAuditor{store: store}
}

// ID 返回稳定 Hook ID。
func (*StoreAuditor) ID() string { return "builtin.security-auditor" }

// Commit 持久化脱敏的安全决策。
func (a *StoreAuditor) Commit(ctx context.Context, mutation corelifecycle.AuditMutation) error {
	return a.store.AppendSecurityDecision(ctx, mutation)
}
