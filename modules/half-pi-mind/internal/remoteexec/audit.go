package remoteexec

import (
	"errors"
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

// AuditProgress 是独立于状态迁移的远程输出审计记录。
type AuditProgress struct {
	RunID string
	Seq   int64
	Kind  protocol.ProgressKind
	Data  string
	Gap   bool
	At    time.Time
}

// Auditor 持久化 run 创建和状态迁移。
type Auditor interface {
	CreateRemoteRun(AuditRun) error
	TransitionRemoteRun(AuditTransition) error
}

// TaskAuditor 原子持久化后台 run 及其任务快照。
type TaskAuditor interface {
	CreateRemoteRunTask(AuditRun, Task) error
}

// ProgressAuditor 可选地持久化进度；失败不得阻断 run 状态机。
type ProgressAuditor interface {
	AppendRemoteRunProgress(AuditProgress) error
}

type auditFailure struct {
	err error
}

func (e auditFailure) Error() string { return e.err.Error() }
func (e auditFailure) Unwrap() error { return e.err }

// IsAuditFailure 判断状态更新是否因审计持久化失败而中止。
func IsAuditFailure(err error) bool {
	var target auditFailure
	return errors.As(err, &target)
}
