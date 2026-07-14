package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Session struct {
	ID          string
	GroupID     string
	Name        string
	Description string
	SoulPath    string
	CreatedAt   time.Time
}

// CreateSession inserts a new session.
func (s *Store) CreateSession(groupID, id string) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, group_id) VALUES (?, ?)`, id, groupID,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// GetSession returns a session by ID.
func (s *Store) GetSession(id string) (*Session, error) {
	return getSession(s.db, id)
}

// ListSessions returns sessions in a group, newest first.
func (s *Store) ListSessions(groupID string) ([]Session, error) {
	return listSessions(s.db, groupID)
}

// UpdateSessionName sets the name of a session.
func (s *Store) UpdateSessionName(id, name string) error {
	_, err := s.db.Exec(`UPDATE sessions SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return fmt.Errorf("update session name: %w", err)
	}
	return nil
}

// GetMessageCount returns the number of messages in a session.
func (s *Store) GetMessageCount(sessionID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = ?`, sessionID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count messages: %w", err)
	}
	return count, nil
}

// ReplaceMessages deletes all messages in a session and inserts new ones in a single transaction.
func (s *Store) ReplaceMessages(sessionID string, messages []Message) error {
	if sessionID == "" {
		return fmt.Errorf("sessionID cannot be empty")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM messages WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("delete messages: %w", err)
	}

	if len(messages) == 0 {
		return tx.Commit()
	}

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

func getSession(db *sql.DB, id string) (*Session, error) {
	row := db.QueryRow(
		`SELECT id, group_id, name, description, soul_path, created_at FROM sessions WHERE id = ?`, id,
	)
	var sess Session
	var ca string
	err := row.Scan(&sess.ID, &sess.GroupID, &sess.Name, &sess.Description, &sess.SoulPath, &ca)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	sess.CreatedAt = parseTime(ca)
	return &sess, nil
}

func listSessions(db *sql.DB, groupID string) ([]Session, error) {
	rows, err := db.Query(
		`SELECT id, group_id, name, description, soul_path, created_at
		 FROM sessions WHERE group_id = ? ORDER BY created_at DESC`, groupID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var ca string
		if err := rows.Scan(&sess.ID, &sess.GroupID, &sess.Name, &sess.Description, &sess.SoulPath, &ca); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sess.CreatedAt = parseTime(ca)
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// FindSessionsByPrefix 返回 group 内 ID 前缀匹配的所有会话（忽略 UUID 中的 `-` 分隔符）。
func (s *Store) FindSessionsByPrefix(groupID, prefix string) ([]Session, error) {
	all, err := s.ListSessions(groupID)
	if err != nil {
		return nil, err
	}
	normalized := strings.ReplaceAll(prefix, "-", "")
	var matches []Session
	for _, sess := range all {
		id := strings.ReplaceAll(sess.ID, "-", "")
		if strings.HasPrefix(id, normalized) {
			matches = append(matches, sess)
		}
	}
	return matches, nil
}
