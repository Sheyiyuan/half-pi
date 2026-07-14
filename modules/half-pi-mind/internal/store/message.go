package store

import (
	"database/sql"
	"fmt"
	"time"
)

type Message struct {
	ID        int64
	SessionID string
	Role      string
	Content   string
	ToolID    string
	ToolCalls string
	Seq       int
	CreatedAt time.Time
}

func (s *Store) SaveMessages(sessionID string, messages []Message) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO messages (session_id, role, content, tool_id, tool_calls, seq) VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, m := range messages {
		if _, err := stmt.Exec(sessionID, m.Role, m.Content, m.ToolID, m.ToolCalls, m.Seq); err != nil {
			return fmt.Errorf("insert message: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) GetMessages(sessionID string) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, role, content, tool_id, tool_calls, seq, created_at
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
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.ToolID, &m.ToolCalls, &m.Seq, &ca); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.CreatedAt = parseTime(ca)
		messages = append(messages, m)
	}
	return messages, rows.Err()
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
