package remoteexec

import (
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// AuditMetadata 是创建远程执行审计记录所需的脱敏元数据。
type AuditMetadata struct {
	ArgsDigest     string
	ApprovalSource string
	ApprovalMode   string
	ApprovalReason string
}

// AuditRun 是审计层的 run 创建记录。
type AuditRun struct {
	ID        string
	SessionID string
	HandID    string
	Tool      string
	Metadata  AuditMetadata
	Status    protocol.RunStatus
	CreatedAt time.Time
}

// AuditTransition 是一次原子状态迁移记录。
type AuditTransition struct {
	RunID      string
	FromStatus protocol.RunStatus
	ToStatus   protocol.RunStatus
	RejectCode protocol.RejectCode
	Error      string
	EventType  string
	Message    string
	Accepted   bool
	At         time.Time
}

// Auditor 持久化 run 创建和状态迁移。
type Auditor interface {
	CreateRemoteRun(AuditRun) error
	TransitionRemoteRun(AuditTransition) error
}
