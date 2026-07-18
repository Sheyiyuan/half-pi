// Package approval 管理 conversation 级异步审批及其脱敏审计。
package approval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// Status 是审批审计记录的生命周期状态。
type Status string

const (
	StatusPending   Status = "pending"
	StatusResolved  Status = "resolved"
	StatusExpired   Status = "expired"
	StatusCancelled Status = "cancelled"
)

var (
	// ErrNotFound 表示审批不存在或已随调用取消。
	ErrNotFound = errors.New("approval not found")
	// ErrExpired 表示审批已超过裁决期限。
	ErrExpired = errors.New("approval expired")
	// ErrConflict 表示审批已经被其他裁决者完成。
	ErrConflict = errors.New("approval already resolved")
	// ErrNotOwned 表示审批不属于当前运行时可访问的 conversation。
	ErrNotOwned = errors.New("approval is not owned by conversation runtime")
	// ErrNotAccepted 表示调用方未能发送 accepted，审批裁决已审计但不得放行工具。
	ErrNotAccepted = errors.New("approval resolution response was not accepted")
)

// Request 是 Agent Core 提交给 Broker 的脱敏审批请求。
type Request struct {
	ConversationID string
	RequestID      string
	RunID          string
	Tool           string
	Reason         string
	ArgsDigest     string
}

// Actor 是一次审批裁决的身份来源。
type Actor struct {
	ID     string
	Label  string
	Source string
}

// Resolution 是首个合法审批裁决的稳定结果。
type Resolution struct {
	ApprovalID     string
	ConversationID string
	Decision       protocol.FaceApprovalDecision
	Actor          Actor
	Reason         string
	ResolvedAt     time.Time
}

// ResolveHooks 在 Broker 锁内校验归属，并在持久化成功后排入 accepted 响应。
type ResolveHooks struct {
	Validate func(protocol.ApprovalRequest) bool
	Accepted func(protocol.ApprovalRequest) bool
}

// Allowed 返回该裁决是否允许工具继续执行。
func (r Resolution) Allowed() bool {
	return r.Decision == protocol.FaceApprovalAllowOnce || r.Decision == protocol.FaceApprovalAllowSession
}

// CheckResult 是安全检查和可选审批后的完整结果。
type CheckResult struct {
	Blocked    bool
	Reason     string
	Resolution Resolution
}

// AuditRecord 是 SQLite 可持久化的审批请求和裁决摘要。
type AuditRecord struct {
	Request          protocol.ApprovalRequest
	Status           Status
	Decision         protocol.FaceApprovalDecision
	Actor            Actor
	ResolutionReason string
	CreatedAt        time.Time
	ResolvedAt       time.Time
}

// Auditor 是 Broker 所需的审批审计存储接口。
type Auditor interface {
	CreateApproval(AuditRecord) error
	FinishApproval(string, Status, Resolution) error
	LookupApproval(string) (AuditRecord, bool, error)
}

// FallbackResolver 让 REPL 等本地入口通过同一个 Broker 提交裁决，并应响应 context 取消。
type FallbackResolver func(context.Context, protocol.ApprovalRequest) (Actor, protocol.FaceApprovalDecision, string, bool)

// ArgsDigest 返回不会暴露原始参数的规范 SHA-256 标识。
func ArgsDigest(args json.RawMessage) string {
	sum := sha256.Sum256(args)
	return "sha256:" + hex.EncodeToString(sum[:])
}
