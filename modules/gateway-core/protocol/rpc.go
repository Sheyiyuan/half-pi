package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// RejectCode 是 Hand 在执行前拒绝 RPC 的结构化原因。
type RejectCode string

const (
	RejectInvalidRequest         RejectCode = "invalid_request"
	RejectUnknownTool            RejectCode = "unknown_tool"
	RejectDenyTools              RejectCode = "deny_tools"
	RejectAllowToolsMiss         RejectCode = "allow_tools_miss"
	RejectApprovalRequired       RejectCode = "approval_required"
	RejectApprovalExpired        RejectCode = "approval_expired"
	RejectApprovalDigestMismatch RejectCode = "approval_digest_mismatch"
	RejectCheckFailed            RejectCode = "check_failed"
	RejectDeadlineExceeded       RejectCode = "deadline_exceeded"
	RejectDuplicateRun           RejectCode = "duplicate_run"
	RejectInternalError          RejectCode = "internal_error"
)

// CancelStatus 是 Hand 对取消请求的结构化处理结果。
type CancelStatus string

const (
	CancelCancelled   CancelStatus = "cancelled"
	CancelAlreadyDone CancelStatus = "already_done"
	CancelUnknownRun  CancelStatus = "unknown_run"
	CancelFailed      CancelStatus = "failed"
)

// Approval 表示 Mind 已完成的审批，不覆盖 Hand 本地安全检查。
type Approval struct {
	Approved   bool   `json:"approved"`
	Source     string `json:"source"`
	Mode       string `json:"mode,omitempty"`
	Approver   string `json:"approver,omitempty"`
	Reason     string `json:"reason,omitempty"`
	OneShot    bool   `json:"one_shot"`
	ArgsDigest string `json:"args_digest"`
	ApprovedAt int64  `json:"approved_at"` // Unix 毫秒
	ExpiresAt  int64  `json:"expires_at"`  // Unix 毫秒
}

// RPC 由 Mind 发送，请求 Hand 执行工具。
type RPC struct {
	RunID      string         `json:"run_id"`
	Tool       string         `json:"tool"`
	Args       map[string]any `json:"args"`
	DeadlineAt int64          `json:"deadline_at"` // Unix 毫秒
	Approval   *Approval      `json:"approval,omitempty"`
}

// RPCAccepted 表示 Hand 已完成本地守门并开始执行。
type RPCAccepted struct {
	RunID     string `json:"run_id"`
	StartedAt int64  `json:"started_at"` // Unix 毫秒
}

// RPCRejected 表示 Hand 在执行前拒绝请求。
type RPCRejected struct {
	RunID  string     `json:"run_id"`
	Code   RejectCode `json:"code"`
	Reason string     `json:"reason,omitempty"`
}

// ProgressKind 是远程命令输出流的类型。
type ProgressKind string

const (
	// ProgressStdout 表示标准输出。
	ProgressStdout ProgressKind = "stdout"
	// ProgressStderr 表示标准错误输出。
	ProgressStderr ProgressKind = "stderr"

	// MaxRPCProgressChunkBytes 限制单条进度消息的数据字节数。
	MaxRPCProgressChunkBytes = 4 << 10
	// MaxRPCProgressBytes 限制单次 run 可接收和转发的进度总字节数。
	MaxRPCProgressBytes = 1 << 20
	// MaxRPCProgressEvents 限制单次 run 可接收和转发的进度事件数。
	MaxRPCProgressEvents = 256
)

// RPCProgress 是 Hand 执行工具时发送的有界增量输出。
// Seq 独立于 Envelope.Seq，并在单个 run 内单调递增。
type RPCProgress struct {
	RunID string       `json:"run_id"`
	Seq   int64        `json:"seq"`
	Kind  ProgressKind `json:"kind"`
	Data  string       `json:"data"`
}

// RPCResult 是 Hand 执行工具后的最终返回。
type RPCResult struct {
	RunID     string `json:"run_id"`
	Success   bool   `json:"success"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// RPCCancel 请求 Hand 取消指定 run。
type RPCCancel struct {
	RunID  string `json:"run_id"`
	Reason string `json:"reason"`
}

// RPCCancelResult 是 Hand 对取消请求的响应。
type RPCCancelResult struct {
	RunID  string       `json:"run_id"`
	Status CancelStatus `json:"status"`
	Error  string       `json:"error,omitempty"`
}

var (
	ErrApprovalRequired       = errors.New("approval is required")
	ErrApprovalExpired        = errors.New("approval has expired")
	ErrApprovalDigestMismatch = errors.New("approval digest mismatch")
)

// ApprovalDigest 计算绑定 run、Hand、工具和参数的 SHA-256 摘要。
// 参数对象键由 encoding/json 确定性排序；字符串不做 Unicode 归一化。
func ApprovalDigest(runID, handID, tool string, args map[string]any) (string, error) {
	payload := struct {
		RunID  string         `json:"run_id"`
		HandID string         `json:"hand_id"`
		Tool   string         `json:"tool"`
		Args   map[string]any `json:"args"`
	}{runID, handID, tool, args}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal approval digest payload: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateApproval 校验 Approval 是否能授权指定请求的确认步骤。
func ValidateApproval(approval *Approval, runID, handID, tool string, args map[string]any, now time.Time) error {
	if approval == nil || !approval.Approved || approval.Source == "" || !approval.OneShot {
		return ErrApprovalRequired
	}
	nowMs := now.UnixMilli()
	if approval.ApprovedAt <= 0 || approval.ExpiresAt <= approval.ApprovedAt || nowMs < approval.ApprovedAt || nowMs > approval.ExpiresAt {
		return ErrApprovalExpired
	}
	digest, err := ApprovalDigest(runID, handID, tool, args)
	if err != nil {
		return err
	}
	if approval.ArgsDigest != digest {
		return ErrApprovalDigestMismatch
	}
	return nil
}

// ValidateRPC 校验 RPC 的必填字段。
func ValidateRPC(rpc RPC, now time.Time) error {
	if rpc.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if rpc.Tool == "" {
		return fmt.Errorf("tool is required")
	}
	if rpc.Args == nil {
		return fmt.Errorf("args is required")
	}
	if rpc.DeadlineAt <= now.UnixMilli() {
		return fmt.Errorf("deadline has expired")
	}
	return nil
}

// ValidateRPCAccepted 校验 accepted 消息。
func ValidateRPCAccepted(msg RPCAccepted) error {
	if msg.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if msg.StartedAt <= 0 {
		return fmt.Errorf("started_at is required")
	}
	return nil
}

// ValidateRPCRejected 校验 rejected 消息。
func ValidateRPCRejected(msg RPCRejected) error {
	if msg.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if msg.Code == "" {
		return fmt.Errorf("code is required")
	}
	if !validRejectCode(msg.Code) {
		return fmt.Errorf("unknown rejection code %q", msg.Code)
	}
	return nil
}

// ValidateRPCProgress 校验增量输出消息及单条消息上限。
func ValidateRPCProgress(msg RPCProgress) error {
	if msg.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if msg.Seq <= 0 {
		return fmt.Errorf("seq must be positive")
	}
	if msg.Kind != ProgressStdout && msg.Kind != ProgressStderr {
		return fmt.Errorf("unknown progress kind %q", msg.Kind)
	}
	if msg.Data == "" {
		return fmt.Errorf("data is required")
	}
	if !utf8.ValidString(msg.Data) {
		return fmt.Errorf("data must be valid UTF-8")
	}
	if len(msg.Data) > MaxRPCProgressChunkBytes {
		return fmt.Errorf("progress data exceeds %d bytes", MaxRPCProgressChunkBytes)
	}
	return nil
}

// ValidateRPCResult 校验最终结果消息。
func ValidateRPCResult(msg RPCResult) error {
	if msg.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	return nil
}

// ValidateRPCCancel 校验取消请求。
func ValidateRPCCancel(msg RPCCancel) error {
	if msg.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	return nil
}

// ValidateRPCCancelResult 校验取消结果。
func ValidateRPCCancelResult(msg RPCCancelResult) error {
	if msg.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if msg.Status == "" {
		return fmt.Errorf("status is required")
	}
	if !validCancelStatus(msg.Status) {
		return fmt.Errorf("unknown cancel status %q", msg.Status)
	}
	return nil
}

func validRejectCode(code RejectCode) bool {
	switch code {
	case RejectInvalidRequest, RejectUnknownTool,
		RejectDenyTools, RejectAllowToolsMiss, RejectApprovalRequired,
		RejectApprovalExpired, RejectApprovalDigestMismatch, RejectCheckFailed,
		RejectDeadlineExceeded, RejectDuplicateRun, RejectInternalError:
		return true
	default:
		return false
	}
}

func validCancelStatus(status CancelStatus) bool {
	switch status {
	case CancelCancelled, CancelAlreadyDone, CancelUnknownRun, CancelFailed:
		return true
	default:
		return false
	}
}
