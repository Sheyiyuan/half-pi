package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type Message struct {
	ID                int64
	SessionID         string
	Role              string
	Content           string
	RequestID         string
	ToolID            string
	ToolCalls         string
	CompactProjection string
	Seq               int
	CreatedAt         time.Time
}

func (s *Store) GetMessages(sessionID string) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, role, content, request_id, tool_id, tool_calls, compact_projection, seq, created_at
		 FROM messages WHERE session_id = ? ORDER BY seq`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var ca string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.RequestID, &m.ToolID, &m.ToolCalls, &m.CompactProjection, &m.Seq, &ca); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.CreatedAt = parseTime(ca)
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// AppendMessages 在 expectedLastSeq 未变化时原子追加一个连续消息批次。
func (s *Store) AppendMessages(sessionID string, expectedLastSeq int, messages []Message) error {
	_, err := s.appendMessages(sessionID, expectedLastSeq, messages, "", LifecycleEvent{})
	return err
}

// AppendMessagesWithCompactPending 原子追加完整工具批次，并在尚无 pending 时建立自动 Compact hint。
func (s *Store) AppendMessagesWithCompactPending(sessionID string, expectedLastSeq int, messages []Message,
	pendingID string, requestedEvent LifecycleEvent,
) (CompactPendingResult, error) {
	if !validUUIDv7(pendingID) || requestedEvent.SubjectID != sessionID {
		return CompactPendingResult{}, fmt.Errorf("invalid compact pending message append")
	}
	return s.appendMessages(sessionID, expectedLastSeq, messages, pendingID, requestedEvent)
}

func (s *Store) appendMessages(sessionID string, expectedLastSeq int, messages []Message,
	pendingID string, requestedEvent LifecycleEvent,
) (CompactPendingResult, error) {
	if sessionID == "" {
		return CompactPendingResult{}, fmt.Errorf("sessionID cannot be empty")
	}
	if expectedLastSeq < 0 {
		return CompactPendingResult{}, fmt.Errorf("expected last seq must not be negative")
	}
	if len(messages) == 0 {
		return CompactPendingResult{}, nil
	}
	for i, message := range messages {
		if message.Seq != expectedLastSeq+i+1 {
			return CompactPendingResult{}, fmt.Errorf("message batch must have contiguous seq values")
		}
		if err := validateCompactProjection(message); err != nil {
			return CompactPendingResult{}, err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return CompactPendingResult{}, fmt.Errorf("begin append messages: %w", err)
	}
	defer tx.Rollback()
	var lastSeq int
	var pending bool
	if err := tx.QueryRow(`SELECT history_generation, pending_compact FROM session_runtime WHERE session_id = ?`, sessionID).Scan(&lastSeq, &pending); err != nil {
		return CompactPendingResult{}, fmt.Errorf("read message generation: %w", err)
	}
	if lastSeq != expectedLastSeq {
		return CompactPendingResult{}, fmt.Errorf("message sequence conflict: got %d, expected %d", lastSeq, expectedLastSeq)
	}
	stmt, err := tx.Prepare(`INSERT INTO messages
		(session_id, role, content, request_id, tool_id, tool_calls, compact_projection, seq)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return CompactPendingResult{}, fmt.Errorf("prepare append messages: %w", err)
	}
	defer stmt.Close()
	for _, message := range messages {
		if _, err := stmt.Exec(sessionID, message.Role, message.Content, message.RequestID, message.ToolID,
			message.ToolCalls, message.CompactProjection, message.Seq); err != nil {
			return CompactPendingResult{}, fmt.Errorf("append message: %w", err)
		}
	}
	result, err := tx.Exec(`UPDATE sessions SET updated_at = datetime('now') WHERE id = ?`, sessionID)
	if err != nil {
		return CompactPendingResult{}, fmt.Errorf("touch session: %w", err)
	}
	if err := requireSessionUpdated(result, sessionID); err != nil {
		return CompactPendingResult{}, err
	}
	newLastSeq := expectedLastSeq + len(messages)
	createdPending := pendingID != "" && !pending
	snapshotAdvance := len(messages)
	if createdPending {
		snapshotAdvance++
	}
	query := `UPDATE session_runtime SET history_generation = ?,
		history_view_generation = history_view_generation + ?, snapshot_version = snapshot_version + ?, updated_at = ?`
	args := []any{newLastSeq, len(messages), snapshotAdvance, time.Now().UTC().UnixMilli()}
	if createdPending {
		query += `, pending_compact = 1, pending_compact_id = ?, pending_attempt = 0, pending_not_before = 0`
		args = append(args, pendingID)
	}
	query += ` WHERE session_id = ? AND history_generation = ?`
	args = append(args, sessionID, expectedLastSeq)
	if createdPending {
		query += ` AND pending_compact = 0`
	}
	result, err = tx.Exec(query, args...)
	if err != nil {
		return CompactPendingResult{}, fmt.Errorf("advance message generation: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return CompactPendingResult{}, fmt.Errorf("advance message generation: concurrent runtime mutation")
	}
	if createdPending {
		if err := insertLifecycleEvent(context.Background(), tx, requestedEvent); err != nil {
			return CompactPendingResult{}, err
		}
	}
	runtime, err := getSessionRuntime(context.Background(), tx, sessionID)
	if err != nil {
		return CompactPendingResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CompactPendingResult{}, fmt.Errorf("commit append messages: %w", err)
	}
	return CompactPendingResult{Runtime: runtime, Created: createdPending}, nil
}

func validateCompactProjection(message Message) error {
	if message.CompactProjection == "" {
		return nil
	}
	if message.Role != "tool" {
		return fmt.Errorf("compact projection is only valid for tool messages")
	}
	if len(message.CompactProjection) > 4<<10 {
		return fmt.Errorf("compact projection exceeds 4096 bytes")
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader([]byte(message.CompactProjection)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil || value == nil {
		return fmt.Errorf("compact projection must be a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("compact projection must contain one JSON value")
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, []byte(message.CompactProjection)) {
		return fmt.Errorf("compact projection must use canonical JSON")
	}
	return nil
}

// GetMessagesBefore 按 seq 向前分页，返回结果始终按 seq 升序排列。
func (s *Store) GetMessagesBefore(sessionID string, beforeSeq, limit int) ([]Message, bool, error) {
	if sessionID == "" || beforeSeq < 0 || limit <= 0 {
		return nil, false, fmt.Errorf("invalid message page query")
	}
	query := `SELECT id, session_id, role, content, request_id, tool_id, tool_calls, compact_projection, seq, created_at
		FROM messages WHERE session_id = ? ORDER BY seq DESC LIMIT ?`
	args := []any{sessionID, limit + 1}
	if beforeSeq > 0 {
		query = `SELECT id, session_id, role, content, request_id, tool_id, tool_calls, compact_projection, seq, created_at
			FROM messages WHERE session_id = ? AND seq < ? ORDER BY seq DESC LIMIT ?`
		args = []any{sessionID, beforeSeq, limit + 1}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("get message page: %w", err)
	}
	defer rows.Close()
	messages := make([]Message, 0, limit+1)
	for rows.Next() {
		var message Message
		var createdAt string
		if err := rows.Scan(&message.ID, &message.SessionID, &message.Role, &message.Content, &message.RequestID,
			&message.ToolID, &message.ToolCalls, &message.CompactProjection, &message.Seq, &createdAt); err != nil {
			return nil, false, fmt.Errorf("scan message page: %w", err)
		}
		message.CreatedAt = parseTime(createdAt)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("read message page: %w", err)
	}
	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	return messages, hasMore, nil
}

func (s *Store) GetLastSeq(sessionID string) (int, error) {
	var seq sql.NullInt64
	err := s.db.QueryRow(
		`SELECT MAX(seq) FROM messages WHERE session_id = ?`, sessionID,
	).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("get last seq: %w", err)
	}
	if !seq.Valid {
		return 0, nil
	}
	return int(seq.Int64), nil
}
