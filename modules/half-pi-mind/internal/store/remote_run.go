package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

// RemoteRunRecord 是持久化的远程执行审计记录。
type RemoteRunRecord struct {
	ID, SessionID, RequestID, HandID, Tool    string
	TraceID, SpanID, ParentSpanID             string
	GroupID, PrincipalID, LifecycleSource     string
	NodeID                                    string
	ArgsDigest, ApprovalSource, ApprovalMode  string
	ApprovalReason, RejectCode, Error         string
	Status                                    protocol.RunStatus
	CreatedAt, SentAt, AcceptedAt, FinishedAt time.Time
	DurationMs                                int64
}

// RemoteRunEventRecord 是一次持久化状态迁移。
type RemoteRunEventRecord struct {
	Seq                  int
	ProgressSeq          int64
	RunID, Type, Message string
	Kind                 protocol.ProgressKind
	Gap                  bool
	FromStatus, ToStatus protocol.RunStatus
	CreatedAt            time.Time
}

// AppendRemoteRunProgress 追加独立进度审计，不更新 remote_runs 状态。
func (s *Store) AppendRemoteRunProgress(progress remoteexec.AuditProgress) error {
	if len(progress.Data) > protocol.MaxRPCProgressChunkBytes {
		return fmt.Errorf("progress data exceeds %d bytes", protocol.MaxRPCProgressChunkBytes)
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO remote_run_events
		(run_id, seq, from_status, to_status, type, message, created_at, progress_seq, kind, gap)
		SELECT ?, COALESCE(MAX(seq), 0) + 1, '', '', 'progress', ?, ?, ?, ?, ?
		FROM remote_run_events WHERE run_id = ?`,
		progress.RunID, progress.Data, progress.At.UnixMilli(), progress.Seq, progress.Kind, progress.Gap, progress.RunID)
	if err != nil {
		return fmt.Errorf("insert remote run progress: %w", err)
	}
	return nil
}

// CreateRemoteRun 创建 run 及其 created 事件。
func (s *Store) CreateRemoteRun(run remoteexec.AuditRun) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertRemoteRun(tx, run); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateRemoteRunTask 原子创建后台 run、created 事件和任务快照。
func (s *Store) CreateRemoteRunTask(run remoteexec.AuditRun, task remoteexec.Task) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertRemoteRun(tx, run); err != nil {
		return err
	}
	if err := insertRemoteTask(tx, task); err != nil {
		return err
	}
	return tx.Commit()
}

func insertRemoteRun(tx interface {
	Exec(string, ...any) (sql.Result, error)
}, run remoteexec.AuditRun) error {
	createdAt := run.CreatedAt.UnixMilli()
	_, err := tx.Exec(`INSERT INTO remote_runs
			(id, session_id, request_id, trace_id, span_id, parent_span_id, group_id, principal_id,
			 lifecycle_source, node_id, hand_id, tool, args_digest, approval_source, approval_mode, approval_reason, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.SessionID, run.Metadata.RequestID, run.Metadata.TraceID, run.Metadata.SpanID,
		run.Metadata.ParentSpanID, run.Metadata.GroupID, run.Metadata.PrincipalID,
		run.Metadata.Source, run.Metadata.NodeID, run.HandID, run.Tool, run.Metadata.ArgsDigest,
		run.Metadata.ApprovalSource, run.Metadata.ApprovalMode, run.Metadata.ApprovalReason,
		run.Status, createdAt)
	if err != nil {
		return fmt.Errorf("insert remote run: %w", err)
	}
	_, err = tx.Exec(`INSERT INTO remote_run_events
		(run_id, seq, from_status, to_status, type, created_at) VALUES (?, 1, '', ?, 'created', ?)`,
		run.ID, run.Status, createdAt)
	if err != nil {
		return fmt.Errorf("insert remote run event: %w", err)
	}
	return nil
}

// TransitionRemoteRun 原子更新 run 并追加迁移事件。
func (s *Store) TransitionRemoteRun(change remoteexec.AuditTransition) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := change.At.UnixMilli()
	terminal := protocol.IsTerminalRunStatus(change.ToStatus)
	_, err = tx.Exec(`UPDATE remote_runs SET
		status = ?,
		reject_code = CASE WHEN ? <> '' THEN ? ELSE reject_code END,
		error = CASE WHEN ? <> '' THEN ? ELSE error END,
		sent_at = CASE WHEN ? = 'sent' THEN ? ELSE sent_at END,
		accepted_at = CASE WHEN (? IN ('accepted','running') OR ?) AND accepted_at = 0 THEN ? ELSE accepted_at END,
		finished_at = CASE WHEN ? THEN ? ELSE finished_at END,
		duration_ms = CASE WHEN ? THEN MAX(0, ? - created_at) ELSE duration_ms END
		WHERE id = ? AND status = ?`,
		change.ToStatus,
		change.RejectCode, change.RejectCode, change.Error, change.Error,
		change.ToStatus, at, change.ToStatus, change.Accepted, at,
		terminal, at, terminal, at, change.RunID, change.FromStatus)
	if err != nil {
		return fmt.Errorf("update remote run: %w", err)
	}
	var changed int
	if err := tx.QueryRow(`SELECT changes()`).Scan(&changed); err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("remote run %q transition source mismatch", change.RunID)
	}
	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM remote_run_events WHERE run_id = ?`, change.RunID).Scan(&seq); err != nil {
		return err
	}
	eventType := change.EventType
	if eventType == "" {
		eventType = string(change.ToStatus)
	}
	_, err = tx.Exec(`INSERT INTO remote_run_events
		(run_id, seq, from_status, to_status, type, message, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		change.RunID, seq, change.FromStatus, change.ToStatus, eventType, change.Message, at)
	if err != nil {
		return fmt.Errorf("insert remote run transition: %w", err)
	}
	return tx.Commit()
}

// GetRemoteRun 按 ID 查询审计记录。
func (s *Store) GetRemoteRun(id string) (RemoteRunRecord, error) {
	row := s.db.QueryRow(remoteRunSelect+` WHERE id = ?`, id)
	return scanRemoteRun(row)
}

// ListRemoteRunsBySession 查询会话的远程执行记录。
func (s *Store) ListRemoteRunsBySession(sessionID string) ([]RemoteRunRecord, error) {
	return s.listRemoteRuns(`session_id = ?`, sessionID)
}

// ListRemoteRunsByHand 查询 Hand 的远程执行记录。
func (s *Store) ListRemoteRunsByHand(handID string) ([]RemoteRunRecord, error) {
	return s.listRemoteRuns(`hand_id = ?`, handID)
}

// ListRemoteRunEvents 查询 run 的有序事件。
func (s *Store) ListRemoteRunEvents(runID string) ([]RemoteRunEventRecord, error) {
	rows, err := s.db.Query(`SELECT seq, run_id, from_status, to_status, type, message, created_at,
		progress_seq, kind, gap
		FROM remote_run_events WHERE run_id = ? ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RemoteRunEventRecord
	for rows.Next() {
		var event RemoteRunEventRecord
		var createdAt int64
		if err := rows.Scan(&event.Seq, &event.RunID, &event.FromStatus, &event.ToStatus, &event.Type, &event.Message, &createdAt,
			&event.ProgressSeq, &event.Kind, &event.Gap); err != nil {
			return nil, err
		}
		event.CreatedAt = time.UnixMilli(createdAt)
		result = append(result, event)
	}
	return result, rows.Err()
}

// RecoverRemoteRuns 将启动前遗留的非终态 run 幂等转换为 lost。
func (s *Store) RecoverRemoteRuns() (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	terminal := []string{"succeeded", "failed", "rejected", "cancelled", "timed_out", "lost"}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(terminal)), ",")
	args := make([]any, len(terminal))
	for i := range terminal {
		args[i] = terminal[i]
	}
	rows, err := tx.Query(`SELECT id, status FROM remote_runs WHERE status NOT IN (`+placeholders+`)`, args...)
	if err != nil {
		return 0, err
	}
	type pending struct{ id, status string }
	var runs []pending
	for rows.Next() {
		var run pending
		if err := rows.Scan(&run.id, &run.status); err != nil {
			rows.Close()
			return 0, err
		}
		runs = append(runs, run)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	now := time.Now().UnixMilli()
	for _, run := range runs {
		var seq int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM remote_run_events WHERE run_id = ?`, run.id).Scan(&seq); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE remote_runs SET status = 'lost', error = 'startup_recovery', finished_at = ?, duration_ms = MAX(0, ? - created_at) WHERE id = ?`, now, now, run.id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO remote_run_events
			(run_id, seq, from_status, to_status, type, message, created_at)
			VALUES (?, ?, ?, 'lost', 'startup_recovery', 'Mind restarted before terminal state', ?)`,
			run.id, seq, run.status, now); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(runs), nil
}

const remoteRunSelect = `SELECT id, session_id, request_id, trace_id, span_id, parent_span_id,
		group_id, principal_id, lifecycle_source, node_id, hand_id, tool, args_digest,
	approval_source, approval_mode, approval_reason, status, reject_code, error,
	created_at, sent_at, accepted_at, finished_at, duration_ms FROM remote_runs`

func (s *Store) listRemoteRuns(where string, arg any) ([]RemoteRunRecord, error) {
	rows, err := s.db.Query(remoteRunSelect+` WHERE `+where+` ORDER BY created_at`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RemoteRunRecord
	for rows.Next() {
		run, err := scanRemoteRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanRemoteRun(row scanner) (RemoteRunRecord, error) {
	var run RemoteRunRecord
	var createdAt, sentAt, acceptedAt, finishedAt int64
	err := row.Scan(&run.ID, &run.SessionID, &run.RequestID,
		&run.TraceID, &run.SpanID, &run.ParentSpanID, &run.GroupID, &run.PrincipalID, &run.LifecycleSource, &run.NodeID,
		&run.HandID, &run.Tool, &run.ArgsDigest,
		&run.ApprovalSource, &run.ApprovalMode, &run.ApprovalReason, &run.Status,
		&run.RejectCode, &run.Error, &createdAt, &sentAt, &acceptedAt, &finishedAt, &run.DurationMs)
	if err != nil {
		return RemoteRunRecord{}, err
	}
	run.CreatedAt = time.UnixMilli(createdAt)
	if sentAt > 0 {
		run.SentAt = time.UnixMilli(sentAt)
	}
	if acceptedAt > 0 {
		run.AcceptedAt = time.UnixMilli(acceptedAt)
	}
	if finishedAt > 0 {
		run.FinishedAt = time.UnixMilli(finishedAt)
	}
	return run, nil
}

var _ remoteexec.Auditor = (*Store)(nil)
var _ remoteexec.TaskAuditor = (*Store)(nil)
var _ remoteexec.ProgressAuditor = (*Store)(nil)
