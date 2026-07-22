package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ContextSummary 是一条不可变的累计上下文摘要节点。
type ContextSummary struct {
	ID                     string
	SessionID              string
	ParentSummaryID        string
	SupersedesSummaryID    string
	FromSeq                int
	ToSeq                  int
	Summary                string
	SummaryDigest          string
	SourceDigest           string
	ContractDigest         string
	ProviderID             string
	ModelID                string
	Profile                string
	PolicyVersion          string
	ProjectionVersion      string
	GenerationMode         string
	GenerationKey          string
	SourceEstimatedTokens  int64
	SummaryEstimatedTokens int64
	InputTokens            int64
	OutputTokens           int64
	CreatedAt              time.Time
}

// SessionRuntime 是持久化 conversation 上下文投影和 pending 状态。
type SessionRuntime struct {
	SessionID             string
	ActiveSummaryID       string
	HistoryGeneration     int64
	CompactGeneration     int64
	HistoryViewGeneration int64
	PendingCompact        bool
	PendingCompactID      string
	PendingAttempt        int64
	PendingNotBefore      int64
	SnapshotVersion       int64
	UpdatedAt             time.Time
}

// CompactSnapshot 是 Compactor 使用的一致 SQLite 快照。
type CompactSnapshot struct {
	Runtime             SessionRuntime
	Messages            []Message
	ActiveSummary       *ContextSummary
	SummaryNodeCount    int
	SummaryStorageBytes int64
}

// PendingExpectation 绑定一次自动 attempt 或手动 admission 观察到的 pending 状态。
type PendingExpectation struct {
	ID       string
	Attempt  int64
	Required bool
}

// CompactCommit 描述一次摘要节点和 active pointer 的原子提交。
type CompactCommit struct {
	Summary                       ContextSummary
	ExpectedHistoryGeneration     int64
	ExpectedActiveSummaryID       string
	ExpectedHistoryViewGeneration int64
	Pending                       PendingExpectation
	ConsumePending                bool
	AllowSameRange                bool
	CompletedEvent                LifecycleEvent
}

// CompactCommitResult 返回提交后的权威节点与 runtime。
type CompactCommitResult struct {
	Summary        ContextSummary
	Runtime        SessionRuntime
	Reused         bool
	StateChanged   bool
	PendingChanged bool
}

// CompactPendingResult 描述 pending 建立或合并后的状态。
type CompactPendingResult struct {
	Runtime SessionRuntime
	Created bool
}

// CompactFailure 描述一次与 pending/outbox 原子收束的失败。
type CompactFailure struct {
	SessionID       string
	ExpectedPending PendingExpectation
	Automatic       bool
	RateLimited     bool
	RetryNotBefore  int64
	FailedEvent     LifecycleEvent
}

// CompactFailureResult 返回失败事务是否改变了 pending 状态。
type CompactFailureResult struct {
	Runtime      SessionRuntime
	StateChanged bool
}

var (
	compactVersionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	compactDigestPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ContextSummaryDigest 计算绑定最终摘要正文的规范摘要。
func ContextSummaryDigest(summary string) string {
	hash := sha256.New()
	writeCompactDigestField(hash, "half-pi:compact-summary:v1")
	writeCompactDigestField(hash, summary)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// ContextSourceDigest 计算一个原始消息前缀及其安全工具投影的规范摘要。
func ContextSourceDigest(sessionID string, fromSeq, toSeq int, messages []Message) (string, error) {
	if sessionID == "" || fromSeq != 1 || toSeq < fromSeq || len(messages) != toSeq-fromSeq+1 {
		return "", fmt.Errorf("invalid compact source range")
	}
	hash := sha256.New()
	writeCompactDigestField(hash, "half-pi:compact-source:v1")
	writeCompactDigestField(hash, sessionID)
	writeCompactDigestField(hash, strconv.Itoa(fromSeq))
	writeCompactDigestField(hash, strconv.Itoa(toSeq))
	for index, message := range messages {
		if message.SessionID != "" && message.SessionID != sessionID {
			return "", fmt.Errorf("compact source session mismatch")
		}
		if message.Seq != fromSeq+index {
			return "", fmt.Errorf("compact source sequence is not contiguous")
		}
		writeCompactDigestField(hash, strconv.Itoa(message.Seq))
		writeCompactDigestField(hash, message.Role)
		writeCompactDigestField(hash, message.Content)
		writeCompactDigestField(hash, message.RequestID)
		writeCompactDigestField(hash, message.ToolID)
		writeCompactDigestField(hash, message.ToolCalls)
		writeCompactDigestField(hash, message.CompactProjection)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// ContextContractDigest 计算摘要生成契约的规范摘要。
func ContextContractDigest(sourceDigest, projectionVersion, policyVersion, profile, generationMode, parentSummaryID, generationKey string) string {
	hash := sha256.New()
	for _, field := range []string{
		"half-pi:compact-contract:v1", sourceDigest, projectionVersion, policyVersion,
		profile, generationMode, parentSummaryID, generationKey,
	} {
		writeCompactDigestField(hash, field)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// GetCompactSnapshot 读取消息、runtime、active summary 和存储统计的一致快照。
func (s *Store) GetCompactSnapshot(ctx context.Context, sessionID string) (CompactSnapshot, error) {
	if sessionID == "" {
		return CompactSnapshot{}, fmt.Errorf("session ID is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CompactSnapshot{}, fmt.Errorf("begin compact snapshot: %w", err)
	}
	defer tx.Rollback()
	runtime, err := getSessionRuntime(ctx, tx, sessionID)
	if err != nil {
		return CompactSnapshot{}, err
	}
	messages, err := getCompactMessages(ctx, tx, sessionID)
	if err != nil {
		return CompactSnapshot{}, err
	}
	result := CompactSnapshot{Runtime: runtime, Messages: messages}
	if runtime.ActiveSummaryID != "" {
		summary, err := getContextSummaryByID(ctx, tx, runtime.ActiveSummaryID)
		if err != nil {
			return CompactSnapshot{}, fmt.Errorf("read active context summary: %w", err)
		}
		result.ActiveSummary = &summary
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(LENGTH(CAST(summary AS BLOB))), 0)
		FROM context_summaries WHERE session_id = ?`, sessionID).
		Scan(&result.SummaryNodeCount, &result.SummaryStorageBytes); err != nil {
		return CompactSnapshot{}, fmt.Errorf("read context summary stats: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CompactSnapshot{}, fmt.Errorf("commit compact snapshot: %w", err)
	}
	return result, nil
}

// FindContextSummaryByContract 查询相同范围和 contract 的既有节点。
func (s *Store) FindContextSummaryByContract(ctx context.Context, sessionID string, fromSeq, toSeq int, contractDigest string) (*ContextSummary, error) {
	row := s.db.QueryRowContext(ctx, contextSummarySelect+`
		WHERE session_id = ? AND from_seq = ? AND to_seq = ? AND contract_digest = ?`,
		sessionID, fromSeq, toSeq, contractDigest)
	summary, err := scanContextSummary(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find context summary contract: %w", err)
	}
	return &summary, nil
}

// EnsureCompactPending 将首次自动需求与 requested outbox 原子提交；重复需求只合并。
func (s *Store) EnsureCompactPending(ctx context.Context, sessionID, pendingID string, requestedEvent LifecycleEvent) (CompactPendingResult, error) {
	if !validUUIDv7(pendingID) {
		return CompactPendingResult{}, fmt.Errorf("pending compact ID must be UUIDv7")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CompactPendingResult{}, fmt.Errorf("begin compact pending: %w", err)
	}
	defer tx.Rollback()
	runtime, err := getSessionRuntime(ctx, tx, sessionID)
	if err != nil {
		return CompactPendingResult{}, err
	}
	if runtime.PendingCompact {
		if err := tx.Commit(); err != nil {
			return CompactPendingResult{}, fmt.Errorf("commit merged compact pending: %w", err)
		}
		return CompactPendingResult{Runtime: runtime}, nil
	}
	now := time.Now().UTC().UnixMilli()
	result, err := tx.ExecContext(ctx, `UPDATE session_runtime SET pending_compact = 1, pending_compact_id = ?,
		pending_attempt = 0, pending_not_before = 0, snapshot_version = snapshot_version + 1, updated_at = ?
		WHERE session_id = ? AND pending_compact = 0`, pendingID, now, sessionID)
	if err != nil {
		return CompactPendingResult{}, fmt.Errorf("set compact pending: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return CompactPendingResult{}, fmt.Errorf("set compact pending: concurrent mutation")
	}
	if requestedEvent.SubjectID != sessionID {
		return CompactPendingResult{}, fmt.Errorf("compact requested event subject mismatch")
	}
	if err := insertLifecycleEvent(ctx, tx, requestedEvent); err != nil {
		return CompactPendingResult{}, err
	}
	runtime, err = getSessionRuntime(ctx, tx, sessionID)
	if err != nil {
		return CompactPendingResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CompactPendingResult{}, fmt.Errorf("commit compact pending: %w", err)
	}
	return CompactPendingResult{Runtime: runtime, Created: true}, nil
}

// AdmitCompactAttempt 在 provider 调用前原子递增 durable attempt。
func (s *Store) AdmitCompactAttempt(ctx context.Context, sessionID, pendingID string, expectedAttempt int64) (SessionRuntime, error) {
	if !validUUIDv7(pendingID) || expectedAttempt < 0 || expectedAttempt == math.MaxInt64 {
		return SessionRuntime{}, fmt.Errorf("invalid compact attempt admission")
	}
	now := time.Now().UTC().UnixMilli()
	result, err := s.db.ExecContext(ctx, `UPDATE session_runtime SET pending_attempt = pending_attempt + 1,
		snapshot_version = snapshot_version + 1, updated_at = ?
		WHERE session_id = ? AND pending_compact = 1 AND pending_compact_id = ? AND pending_attempt = ?`,
		now, sessionID, pendingID, expectedAttempt)
	if err != nil {
		return SessionRuntime{}, fmt.Errorf("admit compact attempt: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return SessionRuntime{}, fmt.Errorf("compact attempt conflict")
	}
	return s.GetSessionRuntime(ctx, sessionID)
}

// ClearCompactPending 按 ID/attempt CAS 静默清除重新评估后不再需要的 pending。
func (s *Store) ClearCompactPending(ctx context.Context, sessionID, pendingID string, expectedAttempt int64) (SessionRuntime, error) {
	if !validUUIDv7(pendingID) || expectedAttempt < 0 {
		return SessionRuntime{}, fmt.Errorf("invalid compact pending expectation")
	}
	now := time.Now().UTC().UnixMilli()
	result, err := s.db.ExecContext(ctx, clearPendingSQL+`, snapshot_version = snapshot_version + 1, updated_at = ?
		WHERE session_id = ? AND pending_compact = 1 AND pending_compact_id = ? AND pending_attempt = ?`,
		now, sessionID, pendingID, expectedAttempt)
	if err != nil {
		return SessionRuntime{}, fmt.Errorf("clear compact pending: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return SessionRuntime{}, fmt.Errorf("compact pending conflict")
	}
	return s.GetSessionRuntime(ctx, sessionID)
}

// FinishCompactFailure 将 failed outbox 与对应 pending 清理或 cooldown 原子提交。
func (s *Store) FinishCompactFailure(ctx context.Context, failure CompactFailure) (CompactFailureResult, error) {
	if failure.SessionID == "" || failure.ExpectedPending.Attempt < 0 {
		return CompactFailureResult{}, fmt.Errorf("invalid compact failure")
	}
	if failure.ExpectedPending.ID != "" && !validUUIDv7(failure.ExpectedPending.ID) {
		return CompactFailureResult{}, fmt.Errorf("invalid compact pending expectation")
	}
	if failure.ExpectedPending.Required && !validUUIDv7(failure.ExpectedPending.ID) {
		return CompactFailureResult{}, fmt.Errorf("required compact pending expectation is missing")
	}
	if failure.FailedEvent.SubjectID != failure.SessionID {
		return CompactFailureResult{}, fmt.Errorf("compact failed event subject mismatch")
	}
	now := time.Now().UTC().UnixMilli()
	if failure.RateLimited && failure.RetryNotBefore <= now {
		return CompactFailureResult{}, fmt.Errorf("retry-not-before must be in the future")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CompactFailureResult{}, fmt.Errorf("begin compact failure: %w", err)
	}
	defer tx.Rollback()
	runtime, err := getSessionRuntime(ctx, tx, failure.SessionID)
	if err != nil {
		return CompactFailureResult{}, err
	}
	changed := false
	if failure.Automatic {
		if !failure.ExpectedPending.Required || !validUUIDv7(failure.ExpectedPending.ID) {
			return CompactFailureResult{}, fmt.Errorf("automatic compact failure requires pending expectation")
		}
		var result sql.Result
		if failure.RateLimited {
			result, err = tx.ExecContext(ctx, `UPDATE session_runtime SET pending_not_before = ?,
				snapshot_version = snapshot_version + 1, updated_at = ?
				WHERE session_id = ? AND pending_compact = 1 AND pending_compact_id = ? AND pending_attempt = ?`,
				failure.RetryNotBefore, now, failure.SessionID,
				failure.ExpectedPending.ID, failure.ExpectedPending.Attempt)
		} else {
			result, err = tx.ExecContext(ctx, clearPendingSQL+`, snapshot_version = snapshot_version + 1, updated_at = ?
				WHERE session_id = ? AND pending_compact = 1 AND pending_compact_id = ? AND pending_attempt = ?`,
				now, failure.SessionID, failure.ExpectedPending.ID, failure.ExpectedPending.Attempt)
		}
		if err == nil {
			affected, rowsErr := result.RowsAffected()
			if rowsErr != nil {
				return CompactFailureResult{}, fmt.Errorf("inspect compact failure update: %w", rowsErr)
			}
			changed = affected == 1
		}
	} else if failure.RateLimited && failure.ExpectedPending.Required {
		result, updateErr := tx.ExecContext(ctx, `UPDATE session_runtime SET pending_not_before = ?,
			snapshot_version = snapshot_version + 1, updated_at = ?
			WHERE session_id = ? AND pending_compact = 1 AND pending_compact_id = ? AND pending_attempt = ?`,
			failure.RetryNotBefore, now, failure.SessionID,
			failure.ExpectedPending.ID, failure.ExpectedPending.Attempt)
		err = updateErr
		if err == nil {
			affected, rowsErr := result.RowsAffected()
			if rowsErr != nil {
				return CompactFailureResult{}, fmt.Errorf("inspect compact failure update: %w", rowsErr)
			}
			changed = affected == 1
		}
	}
	if err != nil {
		return CompactFailureResult{}, fmt.Errorf("update compact failure state: %w", err)
	}
	if err := insertLifecycleEvent(ctx, tx, failure.FailedEvent); err != nil {
		return CompactFailureResult{}, err
	}
	runtime, err = getSessionRuntime(ctx, tx, failure.SessionID)
	if err != nil {
		return CompactFailureResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CompactFailureResult{}, fmt.Errorf("commit compact failure: %w", err)
	}
	return CompactFailureResult{Runtime: runtime, StateChanged: changed}, nil
}

// CommitContextSummary 原子插入/复用摘要、切换 active pointer、消费 pending 并写 completed outbox。
func (s *Store) CommitContextSummary(ctx context.Context, commit CompactCommit) (CompactCommitResult, error) {
	if err := validateContextSummary(commit.Summary); err != nil {
		return CompactCommitResult{}, err
	}
	if commit.CompletedEvent.SubjectID != commit.Summary.SessionID {
		return CompactCommitResult{}, fmt.Errorf("compact completed event subject mismatch")
	}
	if commit.ExpectedHistoryGeneration < 0 || commit.ExpectedHistoryViewGeneration < 0 || commit.Pending.Attempt < 0 {
		return CompactCommitResult{}, fmt.Errorf("invalid compact commit expectation")
	}
	if commit.Pending.Required && !validUUIDv7(commit.Pending.ID) {
		return CompactCommitResult{}, fmt.Errorf("invalid compact pending expectation")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CompactCommitResult{}, fmt.Errorf("begin context summary commit: %w", err)
	}
	defer tx.Rollback()
	runtime, err := getSessionRuntime(ctx, tx, commit.Summary.SessionID)
	if err != nil {
		return CompactCommitResult{}, err
	}
	var active *ContextSummary
	if runtime.ActiveSummaryID != "" {
		stored, err := getContextSummaryByID(ctx, tx, runtime.ActiveSummaryID)
		if err != nil {
			return CompactCommitResult{}, fmt.Errorf("read active context summary: %w", err)
		}
		active = &stored
	}
	candidate, exists, err := findContextSummaryContract(ctx, tx, commit.Summary)
	if err != nil {
		return CompactCommitResult{}, err
	}
	reused := exists
	if exists {
		if err := validateReusableSummary(candidate, commit.Summary); err != nil {
			return CompactCommitResult{}, err
		}
	} else {
		candidate = commit.Summary
	}

	fastPath := active != nil && active.SessionID == commit.Summary.SessionID &&
		active.FromSeq == commit.Summary.FromSeq && active.ToSeq == commit.Summary.ToSeq &&
		active.ContractDigest == commit.Summary.ContractDigest
	if fastPath {
		if err := validateReusableSummary(*active, commit.Summary); err != nil {
			return CompactCommitResult{}, err
		}
		candidate = *active
		exists = true
		reused = true
	}
	pendingMatches := runtime.PendingCompact && runtime.PendingCompactID == commit.Pending.ID &&
		runtime.PendingAttempt == commit.Pending.Attempt
	pendingChanged := false
	stateChanged := false
	if fastPath {
		if err := validateSummarySource(ctx, tx, candidate, runtime.HistoryGeneration); err != nil {
			return CompactCommitResult{}, err
		}
		if err := validateSummaryReferences(ctx, tx, candidate); err != nil {
			return CompactCommitResult{}, err
		}
		if commit.ConsumePending && pendingMatches {
			result, err := tx.ExecContext(ctx, clearPendingSQL+`, snapshot_version = snapshot_version + 1,
				updated_at = ? WHERE session_id = ? AND pending_compact = 1 AND pending_compact_id = ? AND pending_attempt = ?`,
				time.Now().UTC().UnixMilli(), candidate.SessionID, commit.Pending.ID, commit.Pending.Attempt)
			if err != nil {
				return CompactCommitResult{}, fmt.Errorf("consume compact pending: %w", err)
			}
			if affected, err := result.RowsAffected(); err != nil || affected != 1 {
				return CompactCommitResult{}, fmt.Errorf("consume compact pending: concurrent mutation")
			}
			pendingChanged = true
			stateChanged = true
		}
	} else {
		if runtime.HistoryGeneration != commit.ExpectedHistoryGeneration ||
			runtime.ActiveSummaryID != commit.ExpectedActiveSummaryID ||
			runtime.HistoryViewGeneration != commit.ExpectedHistoryViewGeneration {
			return CompactCommitResult{}, fmt.Errorf("compact conflict: conversation generation changed")
		}
		if commit.Pending.Required && !pendingMatches {
			return CompactCommitResult{}, fmt.Errorf("compact conflict: pending attempt changed")
		}
		if err := validateSummarySource(ctx, tx, candidate, runtime.HistoryGeneration); err != nil {
			return CompactCommitResult{}, err
		}
		if err := validateSummaryTransition(ctx, tx, runtime, candidate, commit.AllowSameRange, !exists); err != nil {
			return CompactCommitResult{}, err
		}
		if !exists {
			candidate.CreatedAt = time.Now().UTC()
			if err := insertContextSummary(ctx, tx, candidate); err != nil {
				return CompactCommitResult{}, err
			}
		}
		clearPending := commit.ConsumePending && pendingMatches
		query := `UPDATE session_runtime SET active_summary_id = ?, compact_generation = compact_generation + 1,
			history_view_generation = history_view_generation + 1, snapshot_version = snapshot_version + 1, updated_at = ?`
		if clearPending {
			query += `, pending_compact = 0, pending_compact_id = '', pending_attempt = 0, pending_not_before = 0`
			pendingChanged = true
		}
		query += ` WHERE session_id = ? AND history_generation = ? AND active_summary_id = ? AND history_view_generation = ?`
		args := []any{candidate.ID, time.Now().UTC().UnixMilli(), candidate.SessionID,
			commit.ExpectedHistoryGeneration, commit.ExpectedActiveSummaryID, commit.ExpectedHistoryViewGeneration}
		if commit.Pending.Required {
			query += ` AND pending_compact = 1 AND pending_compact_id = ? AND pending_attempt = ?`
			args = append(args, commit.Pending.ID, commit.Pending.Attempt)
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return CompactCommitResult{}, fmt.Errorf("switch active context summary: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return CompactCommitResult{}, fmt.Errorf("compact conflict: runtime changed during commit")
		}
		stateChanged = true
	}
	if err := insertLifecycleEvent(ctx, tx, commit.CompletedEvent); err != nil {
		return CompactCommitResult{}, err
	}
	runtime, err = getSessionRuntime(ctx, tx, candidate.SessionID)
	if err != nil {
		return CompactCommitResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CompactCommitResult{}, fmt.Errorf("commit context summary: %w", err)
	}
	candidate.CreatedAt = candidate.CreatedAt.UTC()
	return CompactCommitResult{
		Summary: candidate, Runtime: runtime, Reused: reused || fastPath,
		StateChanged: stateChanged, PendingChanged: pendingChanged,
	}, nil
}

// GetSessionRuntime 返回单个 conversation 的权威 runtime。
func (s *Store) GetSessionRuntime(ctx context.Context, sessionID string) (SessionRuntime, error) {
	return getSessionRuntime(ctx, s.db, sessionID)
}

const clearPendingSQL = `UPDATE session_runtime SET pending_compact = 0, pending_compact_id = '', pending_attempt = 0, pending_not_before = 0`

const contextSummarySelect = `SELECT id, session_id, parent_summary_id, supersedes_summary_id,
	from_seq, to_seq, summary, summary_digest, source_digest, contract_digest, provider_id, model_id,
	profile, policy_version, projection_version, generation_mode, generation_key,
	source_estimated_tokens, summary_estimated_tokens, input_tokens, output_tokens, created_at
	FROM context_summaries `

func getSessionRuntime(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sessionID string) (SessionRuntime, error) {
	var runtime SessionRuntime
	var pending bool
	var updatedAt int64
	err := queryer.QueryRowContext(ctx, `SELECT session_id, active_summary_id, history_generation,
		compact_generation, history_view_generation, pending_compact, pending_compact_id,
		pending_attempt, pending_not_before, snapshot_version, updated_at
		FROM session_runtime WHERE session_id = ?`, sessionID).Scan(
		&runtime.SessionID, &runtime.ActiveSummaryID, &runtime.HistoryGeneration,
		&runtime.CompactGeneration, &runtime.HistoryViewGeneration, &pending, &runtime.PendingCompactID,
		&runtime.PendingAttempt, &runtime.PendingNotBefore, &runtime.SnapshotVersion, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return SessionRuntime{}, fmt.Errorf("session %q not found", sessionID)
	}
	if err != nil {
		return SessionRuntime{}, fmt.Errorf("get session runtime: %w", err)
	}
	runtime.PendingCompact = pending
	runtime.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	if runtime.PendingCompact != (runtime.PendingCompactID != "") || runtime.PendingAttempt < 0 || runtime.PendingNotBefore < 0 {
		return SessionRuntime{}, fmt.Errorf("session runtime pending invariant is invalid")
	}
	return runtime, nil
}

func getCompactMessages(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, sessionID string) ([]Message, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT id, session_id, role, content, request_id,
		tool_id, tool_calls, compact_projection, seq, created_at
		FROM messages WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get compact messages: %w", err)
	}
	defer rows.Close()
	var messages []Message
	for rows.Next() {
		var message Message
		var createdAt string
		if err := rows.Scan(&message.ID, &message.SessionID, &message.Role, &message.Content,
			&message.RequestID, &message.ToolID, &message.ToolCalls, &message.CompactProjection,
			&message.Seq, &createdAt); err != nil {
			return nil, fmt.Errorf("scan compact message: %w", err)
		}
		message.CreatedAt = parseTime(createdAt)
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func scanContextSummary(row scanner) (ContextSummary, error) {
	var summary ContextSummary
	var createdAt int64
	err := row.Scan(&summary.ID, &summary.SessionID, &summary.ParentSummaryID, &summary.SupersedesSummaryID,
		&summary.FromSeq, &summary.ToSeq, &summary.Summary, &summary.SummaryDigest, &summary.SourceDigest,
		&summary.ContractDigest, &summary.ProviderID, &summary.ModelID, &summary.Profile, &summary.PolicyVersion,
		&summary.ProjectionVersion, &summary.GenerationMode, &summary.GenerationKey,
		&summary.SourceEstimatedTokens, &summary.SummaryEstimatedTokens, &summary.InputTokens,
		&summary.OutputTokens, &createdAt)
	if err != nil {
		return ContextSummary{}, err
	}
	summary.CreatedAt = time.UnixMilli(createdAt).UTC()
	return summary, nil
}

func getContextSummaryByID(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (ContextSummary, error) {
	return scanContextSummary(queryer.QueryRowContext(ctx, contextSummarySelect+` WHERE id = ?`, id))
}

func findContextSummaryContract(ctx context.Context, tx *sql.Tx, candidate ContextSummary) (ContextSummary, bool, error) {
	summary, err := scanContextSummary(tx.QueryRowContext(ctx, contextSummarySelect+`
		WHERE session_id = ? AND from_seq = ? AND to_seq = ? AND contract_digest = ?`,
		candidate.SessionID, candidate.FromSeq, candidate.ToSeq, candidate.ContractDigest))
	if err == sql.ErrNoRows {
		return ContextSummary{}, false, nil
	}
	if err != nil {
		return ContextSummary{}, false, fmt.Errorf("find context summary contract: %w", err)
	}
	return summary, true, nil
}

func validateContextSummary(summary ContextSummary) error {
	if !validUUIDv7(summary.ID) || summary.SessionID == "" || summary.FromSeq != 1 || summary.ToSeq < summary.FromSeq {
		return fmt.Errorf("invalid context summary identity or range")
	}
	if summary.ParentSummaryID != "" && !validUUIDv7(summary.ParentSummaryID) {
		return fmt.Errorf("invalid context summary parent")
	}
	if summary.SupersedesSummaryID != "" && !validUUIDv7(summary.SupersedesSummaryID) {
		return fmt.Errorf("invalid context summary supersedes identity")
	}
	if summary.Summary == "" || !utf8.ValidString(summary.Summary) || summary.SummaryDigest != ContextSummaryDigest(summary.Summary) {
		return fmt.Errorf("invalid context summary digest")
	}
	if !compactDigestPattern.MatchString(summary.SourceDigest) || !compactDigestPattern.MatchString(summary.ContractDigest) ||
		summary.ProviderID == "" || summary.ModelID == "" {
		return fmt.Errorf("context summary contract fields are required")
	}
	if !compactVersionPattern.MatchString(summary.Profile) || !compactVersionPattern.MatchString(summary.PolicyVersion) ||
		!compactVersionPattern.MatchString(summary.ProjectionVersion) {
		return fmt.Errorf("invalid context summary version metadata")
	}
	switch summary.GenerationMode {
	case "full", "incremental":
		if summary.GenerationKey != "" {
			return fmt.Errorf("generation key is only valid for explicit rebase")
		}
	case "rebase":
		if summary.GenerationKey != "" && !compactDigestPattern.MatchString(summary.GenerationKey) {
			return fmt.Errorf("invalid rebase generation key")
		}
	default:
		return fmt.Errorf("invalid context summary generation mode")
	}
	if summary.GenerationMode == "incremental" && summary.ParentSummaryID == "" {
		return fmt.Errorf("incremental summary requires parent")
	}
	if summary.GenerationMode != "incremental" && summary.ParentSummaryID != "" {
		return fmt.Errorf("full summary cannot have parent")
	}
	if summary.SourceEstimatedTokens < 0 || summary.SummaryEstimatedTokens < 0 || summary.InputTokens < 0 || summary.OutputTokens < 0 {
		return fmt.Errorf("context summary token counts must not be negative")
	}
	expectedContract := ContextContractDigest(summary.SourceDigest, summary.ProjectionVersion,
		summary.PolicyVersion, summary.Profile, summary.GenerationMode, summary.ParentSummaryID, summary.GenerationKey)
	if summary.ContractDigest != expectedContract {
		return fmt.Errorf("invalid context summary contract digest")
	}
	return nil
}

func validateStoredSummary(summary ContextSummary) error {
	if err := validateContextSummary(summary); err != nil {
		return fmt.Errorf("context summary integrity error")
	}
	return nil
}

func validateReusableSummary(stored, candidate ContextSummary) error {
	if err := validateStoredSummary(stored); err != nil {
		return err
	}
	if stored.SessionID != candidate.SessionID || stored.FromSeq != candidate.FromSeq || stored.ToSeq != candidate.ToSeq ||
		stored.ContractDigest != candidate.ContractDigest || stored.SourceDigest != candidate.SourceDigest ||
		stored.ProjectionVersion != candidate.ProjectionVersion || stored.PolicyVersion != candidate.PolicyVersion ||
		stored.Profile != candidate.Profile || stored.GenerationMode != candidate.GenerationMode ||
		stored.GenerationKey != candidate.GenerationKey || stored.ParentSummaryID != candidate.ParentSummaryID {
		return fmt.Errorf("context summary contract invariant mismatch")
	}
	return nil
}

func validateSummaryTransition(ctx context.Context, tx *sql.Tx, runtime SessionRuntime, candidate ContextSummary, allowSameRange, inserting bool) error {
	if runtime.ActiveSummaryID != "" {
		active, err := getContextSummaryByID(ctx, tx, runtime.ActiveSummaryID)
		if err != nil {
			return fmt.Errorf("read current active summary: %w", err)
		}
		if err := validateStoredSummary(active); err != nil {
			return err
		}
		if candidate.ToSeq < active.ToSeq || (candidate.ToSeq == active.ToSeq && !allowSameRange) {
			return fmt.Errorf("compact conflict: summary coverage cannot move backward")
		}
		if candidate.GenerationMode == "incremental" && candidate.ToSeq <= active.ToSeq {
			return fmt.Errorf("incremental summary must extend active coverage")
		}
		if candidate.GenerationMode == "incremental" && candidate.ParentSummaryID != runtime.ActiveSummaryID {
			return fmt.Errorf("incremental summary must use active parent")
		}
		if inserting && candidate.SupersedesSummaryID != runtime.ActiveSummaryID {
			return fmt.Errorf("summary supersedes identity mismatch")
		}
	} else if inserting && candidate.SupersedesSummaryID != "" {
		return fmt.Errorf("first summary cannot supersede another node")
	}
	return validateSummaryReferences(ctx, tx, candidate)
}

func validateSummaryReferences(ctx context.Context, tx *sql.Tx, candidate ContextSummary) error {
	if candidate.ParentSummaryID != "" {
		parent, err := getContextSummaryByID(ctx, tx, candidate.ParentSummaryID)
		if err != nil {
			return fmt.Errorf("read parent context summary: %w", err)
		}
		if err := validateStoredSummary(parent); err != nil {
			return err
		}
		if parent.SessionID != candidate.SessionID || parent.ToSeq >= candidate.ToSeq ||
			parent.ProjectionVersion != candidate.ProjectionVersion || parent.PolicyVersion != candidate.PolicyVersion ||
			parent.Profile != candidate.Profile {
			return fmt.Errorf("context summary parent is incompatible")
		}
	}
	if candidate.SupersedesSummaryID != "" {
		superseded, err := getContextSummaryByID(ctx, tx, candidate.SupersedesSummaryID)
		if err != nil {
			return fmt.Errorf("read superseded context summary: %w", err)
		}
		if superseded.SessionID != candidate.SessionID || validateStoredSummary(superseded) != nil {
			return fmt.Errorf("context summary supersedes node is invalid")
		}
	}
	return nil
}

func validateSummarySource(ctx context.Context, tx *sql.Tx, summary ContextSummary, historyGeneration int64) error {
	if err := validateStoredSummary(summary); err != nil {
		return err
	}
	if int64(summary.ToSeq) > historyGeneration {
		return fmt.Errorf("context summary range exceeds conversation history")
	}
	messages, err := getCompactMessagesThrough(ctx, tx, summary.SessionID, summary.ToSeq)
	if err != nil {
		return err
	}
	digest, err := ContextSourceDigest(summary.SessionID, summary.FromSeq, summary.ToSeq, messages)
	if err != nil {
		return err
	}
	if digest != summary.SourceDigest {
		return fmt.Errorf("context summary source integrity error")
	}
	return nil
}

func getCompactMessagesThrough(ctx context.Context, tx *sql.Tx, sessionID string, toSeq int) ([]Message, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, session_id, role, content, request_id,
		tool_id, tool_calls, compact_projection, seq, created_at
		FROM messages WHERE session_id = ? AND seq >= 1 AND seq <= ? ORDER BY seq`, sessionID, toSeq)
	if err != nil {
		return nil, fmt.Errorf("read compact source messages: %w", err)
	}
	defer rows.Close()
	messages := make([]Message, 0, toSeq)
	for rows.Next() {
		var message Message
		var createdAt string
		if err := rows.Scan(&message.ID, &message.SessionID, &message.Role, &message.Content,
			&message.RequestID, &message.ToolID, &message.ToolCalls, &message.CompactProjection,
			&message.Seq, &createdAt); err != nil {
			return nil, fmt.Errorf("scan compact source message: %w", err)
		}
		message.CreatedAt = parseTime(createdAt)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read compact source messages: %w", err)
	}
	return messages, nil
}

func insertContextSummary(ctx context.Context, tx *sql.Tx, summary ContextSummary) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO context_summaries (
		id, session_id, parent_summary_id, supersedes_summary_id, from_seq, to_seq, summary,
		summary_digest, source_digest, contract_digest, provider_id, model_id, profile, policy_version,
		projection_version, generation_mode, generation_key, source_estimated_tokens,
		summary_estimated_tokens, input_tokens, output_tokens, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		summary.ID, summary.SessionID, summary.ParentSummaryID, summary.SupersedesSummaryID,
		summary.FromSeq, summary.ToSeq, summary.Summary, summary.SummaryDigest, summary.SourceDigest,
		summary.ContractDigest, summary.ProviderID, summary.ModelID, summary.Profile, summary.PolicyVersion,
		summary.ProjectionVersion, summary.GenerationMode, summary.GenerationKey,
		summary.SourceEstimatedTokens, summary.SummaryEstimatedTokens, summary.InputTokens,
		summary.OutputTokens, summary.CreatedAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("insert context summary: %w", err)
	}
	return nil
}

func validUUIDv7(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.Version() == 7
}

func writeCompactDigestField(writer interface{ Write([]byte) (int, error) }, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}
