package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
	"unicode/utf8"

	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
)

// OutboxRecord 是待可靠投递的脱敏生命周期事实。
type OutboxRecord struct {
	ID            string
	EventType     string
	SchemaVersion int
	TraceID       string
	SpanID        string
	SubjectID     string
	Payload       json.RawMessage
	Attempts      int
	AvailableAt   time.Time
	CreatedAt     time.Time
}

// AppendSecurityDecision 原子保存安全决策及对应 outbox 事实。
func (s *Store) AppendSecurityDecision(ctx context.Context, mutation corelifecycle.AuditMutation) error {
	if mutation.EventID == "" || mutation.TraceID == "" || mutation.SpanID == "" || mutation.ActionKind == "" || mutation.Decision == "" {
		return fmt.Errorf("invalid security decision identity")
	}
	details := sanitizeAuditDetails(mutation.Details)
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode security decision details: %w", err)
	}
	riskLabels := sanitizeRiskLabels(mutation.RiskLabels)
	riskLabelsJSON, err := json.Marshal(riskLabels)
	if err != nil {
		return fmt.Errorf("encode security decision risk labels: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"event_id": mutation.EventID, "trace_id": mutation.TraceID, "span_id": mutation.SpanID,
		"request_id": mutation.RequestID, "conversation_id": mutation.ConversationID,
		"group_id": mutation.GroupID, "principal_id": mutation.PrincipalID, "node_id": mutation.NodeID,
		"action_kind": mutation.ActionKind, "resource_name": mutation.ResourceName,
		"target_node": mutation.TargetNode, "input_digest": mutation.InputDigest,
		"risk_labels": riskLabels, "decision": mutation.Decision,
		"reason_code": mutation.ReasonCode, "rule_id": mutation.RuleID,
		"policy_version": mutation.PolicyVersion, "approval_id": mutation.ApprovalID, "run_id": mutation.RunID,
		"details": details,
	})
	if err != nil {
		return fmt.Errorf("encode security decision outbox: %w", err)
	}
	createdAt := mutation.OccurredAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin security decision: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO security_decisions (
		id, trace_id, span_id, request_id, conversation_id, group_id, principal_id, node_id,
			action_kind, resource_name, target_node, input_digest, risk_labels, decision, reason_code, rule_id, policy_version,
			approval_id, run_id, details_redacted, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mutation.EventID, mutation.TraceID, mutation.SpanID, mutation.RequestID, mutation.ConversationID,
		mutation.GroupID, mutation.PrincipalID, mutation.NodeID, mutation.ActionKind, mutation.ResourceName,
		mutation.TargetNode, mutation.InputDigest, string(riskLabelsJSON), mutation.Decision, mutation.ReasonCode, mutation.RuleID, mutation.PolicyVersion,
		mutation.ApprovalID, mutation.RunID, string(detailsJSON), createdAt.UnixMilli()); err != nil {
		return fmt.Errorf("insert security decision: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO lifecycle_outbox (
		id, event_type, schema_version, trace_id, span_id, subject_id, payload_redacted,
		available_at, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mutation.EventID, "security.decision", mutation.SchemaVersion, mutation.TraceID, mutation.SpanID,
		mutation.ConversationID, string(payload), createdAt.UnixMilli(), createdAt.UnixMilli()); err != nil {
		return fmt.Errorf("insert lifecycle outbox: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit security decision: %w", err)
	}
	return nil
}

func sanitizeAuditDetails(source map[string]any) map[string]any {
	allowed := map[string]struct{}{
		"provider_id": {}, "model_id": {}, "profile": {}, "duration_ms": {}, "failure_code": {},
		"execution_outcome": {}, "delivery_outcome": {},
	}
	result := make(map[string]any)
	for key, value := range source {
		if _, ok := allowed[key]; !ok || value == nil {
			continue
		}
		reflected := reflect.ValueOf(value)
		switch reflected.Kind() {
		case reflect.String:
			result[key] = reflected.String()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result[key] = reflected.Int()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			result[key] = reflected.Uint()
		case reflect.Bool:
			result[key] = reflected.Bool()
		}
	}
	return result
}

func sanitizeRiskLabels(source []string) []string {
	result := make([]string, 0, min(len(source), 64))
	for _, label := range source {
		if len(result) == 64 {
			break
		}
		if label == "" || len(label) > 128 || !utf8.ValidString(label) {
			continue
		}
		result = append(result, label)
	}
	return result
}

// DueOutbox 返回到期且尚未终止的 outbox 记录。
func (s *Store) DueOutbox(ctx context.Context, now time.Time, limit int) ([]OutboxRecord, error) {
	if limit <= 0 || limit > 256 {
		limit = 64
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, event_type, schema_version, trace_id, span_id,
		subject_id, payload_redacted, attempts, available_at, created_at
		FROM lifecycle_outbox
		WHERE delivered_at = 0 AND dead_letter_at = 0 AND available_at <= ?
		ORDER BY created_at, id LIMIT ?`, now.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("query lifecycle outbox: %w", err)
	}
	defer rows.Close()
	var records []OutboxRecord
	for rows.Next() {
		var record OutboxRecord
		var payload string
		var availableAt, createdAt int64
		if err := rows.Scan(&record.ID, &record.EventType, &record.SchemaVersion, &record.TraceID, &record.SpanID,
			&record.SubjectID, &payload, &record.Attempts, &availableAt, &createdAt); err != nil {
			return nil, fmt.Errorf("scan lifecycle outbox: %w", err)
		}
		record.Payload = json.RawMessage(payload)
		record.AvailableAt = time.UnixMilli(availableAt).UTC()
		record.CreatedAt = time.UnixMilli(createdAt).UTC()
		records = append(records, record)
	}
	return records, rows.Err()
}

// FinishOutbox 标记一次投递成功。
func (s *Store) FinishOutbox(ctx context.Context, id string, deliveredAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE lifecycle_outbox SET delivered_at = ?, last_error = ''
		WHERE id = ? AND delivered_at = 0 AND dead_letter_at = 0`, deliveredAt.UnixMilli(), id)
	return requireOutboxUpdate(result, err)
}

// RetryOutbox 记录失败并安排重试或转入 dead letter。
func (s *Store) RetryOutbox(ctx context.Context, id string, attempts int, availableAt time.Time, lastError string, dead bool) error {
	if len(lastError) > 512 {
		lastError = lastError[:512]
	}
	deadAt := int64(0)
	if dead {
		deadAt = time.Now().UTC().UnixMilli()
	}
	result, err := s.db.ExecContext(ctx, `UPDATE lifecycle_outbox
		SET attempts = ?, available_at = ?, last_error = ?, dead_letter_at = ?
		WHERE id = ? AND delivered_at = 0 AND dead_letter_at = 0`,
		attempts, availableAt.UnixMilli(), lastError, deadAt, id)
	return requireOutboxUpdate(result, err)
}

// PruneLifecycleOutbox 删除超过保留期的已投递和 dead-letter 记录。
// pending 记录永不由 retention 清理。
func (s *Store) PruneLifecycleOutbox(ctx context.Context, deliveredBefore, deadBefore time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM lifecycle_outbox
		WHERE (delivered_at > 0 AND delivered_at < ?)
		   OR (dead_letter_at > 0 AND dead_letter_at < ?)`, deliveredBefore.UnixMilli(), deadBefore.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("prune lifecycle outbox: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned lifecycle outbox: %w", err)
	}
	return count, nil
}

func requireOutboxUpdate(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("outbox record is not pending")
	}
	return nil
}
