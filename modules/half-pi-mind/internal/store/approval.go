package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
)

const (
	maxApprovalActorIDBytes     = 128
	maxApprovalActorLabelBytes  = 64
	maxApprovalActorSourceBytes = 32
)

// CreateApproval 创建不含原始参数的待审批审计记录。
func (s *Store) CreateApproval(record approval.AuditRecord) error {
	request := record.Request
	if request.ApprovalID == "" || request.ConversationID == "" || request.Tool == "" ||
		request.Reason == "" || request.ArgsDigest == "" || record.CreatedAt.IsZero() || request.ExpiresAt.IsZero() ||
		len(request.Reason) > protocol.MaxFaceApprovalReasonBytes || record.Status != approval.StatusPending {
		return fmt.Errorf("approval audit fields are required")
	}
	_, err := s.db.Exec(`INSERT INTO approval_audits
		(approval_id, conversation_id, request_id, run_id, trace_id, span_id, parent_span_id,
		 group_id, principal_id, lifecycle_source, node_id, tool, reason, args_digest, status, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		request.ApprovalID, request.ConversationID, request.RequestID, request.RunID,
		record.Meta.TraceID, record.Meta.SpanID, record.Meta.ParentSpanID, record.Meta.GroupID,
		record.Meta.PrincipalID, record.Meta.Source, record.Meta.NodeID,
		request.Tool, request.Reason, request.ArgsDigest, approval.StatusPending,
		record.CreatedAt.UnixMilli(), request.ExpiresAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("create approval audit: %w", err)
	}
	return nil
}

// FinishApproval 原子保存审批的首个终态。
func (s *Store) FinishApproval(approvalID string, status approval.Status, resolution approval.Resolution) error {
	if approvalID == "" || !terminalApprovalStatus(status) || resolution.ResolvedAt.IsZero() {
		return fmt.Errorf("terminal approval audit fields are required")
	}
	if status == approval.StatusResolved && (resolution.Decision == "" || resolution.Actor.ID == "" || resolution.Actor.Source == "") {
		return fmt.Errorf("resolved approval decision and actor are required")
	}
	if status == approval.StatusResolved && !validApprovalDecision(resolution.Decision) {
		return fmt.Errorf("invalid resolved approval decision %q", resolution.Decision)
	}
	if len(resolution.Actor.ID) > maxApprovalActorIDBytes || len(resolution.Actor.Label) > maxApprovalActorLabelBytes ||
		len(resolution.Actor.Source) > maxApprovalActorSourceBytes || len(resolution.Reason) > protocol.MaxFaceApprovalReasonBytes {
		return fmt.Errorf("terminal approval audit fields exceed size limits")
	}
	if status != approval.StatusResolved && (resolution.Decision != "" || resolution.Actor.ID != "" || resolution.Actor.Label != "" || resolution.Actor.Source != "") {
		return fmt.Errorf("non-resolved approval must not include a decision or actor")
	}
	result, err := s.db.Exec(`UPDATE approval_audits SET
		status = ?, decision = ?, actor_id = ?, actor_label = ?, source = ?, resolution_reason = ?, resolved_at = ?
		WHERE approval_id = ? AND status = ?`,
		status, resolution.Decision, resolution.Actor.ID, resolution.Actor.Label,
		resolution.Actor.Source, resolution.Reason, resolution.ResolvedAt.UnixMilli(),
		approvalID, approval.StatusPending)
	if err != nil {
		return fmt.Errorf("finish approval audit: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("finish approval audit rows: %w", err)
	}
	if changed != 1 {
		return fmt.Errorf("approval audit %q is not pending", approvalID)
	}
	return nil
}

// LookupApproval 返回指定审批审计记录。
func (s *Store) LookupApproval(approvalID string) (approval.AuditRecord, bool, error) {
	row := s.db.QueryRow(`SELECT approval_id, conversation_id, request_id, run_id,
		trace_id, span_id, parent_span_id, group_id, principal_id, lifecycle_source, node_id,
		tool, reason, args_digest,
		status, decision, actor_id, actor_label, source, resolution_reason, created_at, expires_at, resolved_at
		FROM approval_audits WHERE approval_id = ?`, approvalID)
	record, err := scanApproval(row)
	if err == sql.ErrNoRows {
		return approval.AuditRecord{}, false, nil
	}
	if err != nil {
		return approval.AuditRecord{}, false, err
	}
	return record, true, nil
}

// RecoverApprovals 将进程重启时不可能继续等待的审批标记为 cancelled。
func (s *Store) RecoverApprovals(now time.Time) (int, error) {
	result, err := s.db.Exec(`UPDATE approval_audits SET status = ?, resolved_at = ? WHERE status = ?`,
		approval.StatusCancelled, now.UnixMilli(), approval.StatusPending)
	if err != nil {
		return 0, fmt.Errorf("recover approval audits: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("recover approval audit rows: %w", err)
	}
	return int(count), nil
}

type approvalScanner interface{ Scan(...any) error }

func scanApproval(row approvalScanner) (approval.AuditRecord, error) {
	var (
		record                           approval.AuditRecord
		approvalID, conversationID       string
		requestID, runID, tool, reason   string
		traceID, spanID, parentSpanID    string
		groupID, principalID, nodeID     string
		lifecycleSource                  string
		argsDigest, status, decision     string
		actorID, actorLabel, source      string
		resolutionReason                 string
		createdAt, expiresAt, resolvedAt int64
	)
	if err := row.Scan(
		&approvalID, &conversationID, &requestID, &runID,
		&traceID, &spanID, &parentSpanID, &groupID, &principalID, &lifecycleSource, &nodeID,
		&tool, &reason, &argsDigest,
		&status, &decision, &actorID, &actorLabel, &source, &resolutionReason,
		&createdAt, &expiresAt, &resolvedAt,
	); err != nil {
		return approval.AuditRecord{}, err
	}
	record.Request = protocol.ApprovalRequest{
		ApprovalID: approvalID, ConversationID: conversationID, RequestID: requestID,
		RunID: runID, Tool: tool, Reason: reason, ArgsDigest: argsDigest,
		ExpiresAt: time.UnixMilli(expiresAt).UTC(),
	}
	record.Meta.SchemaVersion = 1
	record.Meta.TraceID, record.Meta.SpanID, record.Meta.ParentSpanID = traceID, spanID, parentSpanID
	record.Meta.RequestID, record.Meta.ConversationID, record.Meta.GroupID = requestID, conversationID, groupID
	record.Meta.PrincipalID, record.Meta.Source, record.Meta.NodeID = principalID, lifecycleSource, nodeID
	record.Status = approval.Status(status)
	record.Decision = protocol.FaceApprovalDecision(decision)
	record.Actor = approval.Actor{ID: actorID, Label: actorLabel, Source: source}
	record.ResolutionReason = resolutionReason
	record.CreatedAt = time.UnixMilli(createdAt).UTC()
	if resolvedAt > 0 {
		record.ResolvedAt = time.UnixMilli(resolvedAt).UTC()
	}
	return record, nil
}

func terminalApprovalStatus(status approval.Status) bool {
	switch status {
	case approval.StatusResolved, approval.StatusExpired, approval.StatusCancelled:
		return true
	default:
		return false
	}
}

func validApprovalDecision(decision protocol.FaceApprovalDecision) bool {
	switch decision {
	case protocol.FaceApprovalAllowOnce, protocol.FaceApprovalDenyOnce,
		protocol.FaceApprovalAllowSession, protocol.FaceApprovalDenySession:
		return true
	default:
		return false
	}
}
