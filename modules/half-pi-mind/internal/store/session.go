package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Session 是持久化 conversation 的权威元数据。
type Session struct {
	ID          string
	GroupID     string
	Name        string
	Description string
	SoulPath    string
	Mode        string
	ActiveHand  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateSession 创建一个未命名 conversation。
func (s *Store) CreateSession(groupID, id string) error {
	return s.CreateSessionNamed(groupID, id, "")
}

// CreateSessionNamed 创建一个带可选名称的 conversation。
func (s *Store) CreateSessionNamed(groupID, id, name string) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, group_id, name) VALUES (?, ?, ?)`, id, groupID, name,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// GetSession 按 ID 返回 conversation，不存在时返回 nil。
func (s *Store) GetSession(id string) (*Session, error) {
	return getSession(s.db, id)
}

// ListSessions 返回 group 内的 conversation，按最近更新时间倒序排列。
func (s *Store) ListSessions(groupID string) ([]Session, error) {
	return listSessions(s.db, groupID)
}

// UpdateSessionName 更新 conversation 名称。
func (s *Store) UpdateSessionName(id, name string) error {
	result, err := s.db.Exec(`UPDATE sessions SET name = ?, updated_at = datetime('now') WHERE id = ?`, name, id)
	if err != nil {
		return fmt.Errorf("update session name: %w", err)
	}
	return requireSessionUpdated(result, id)
}

// SetSessionMode 更新 conversation 安全模式。
func (s *Store) SetSessionMode(id, mode string) error {
	switch mode {
	case "strict", "normal", "trust", "yolo":
	default:
		return fmt.Errorf("invalid session mode %q", mode)
	}
	result, err := s.db.Exec(`UPDATE sessions SET mode = ?, updated_at = datetime('now') WHERE id = ?`, mode, id)
	if err != nil {
		return fmt.Errorf("update session mode: %w", err)
	}
	return requireSessionUpdated(result, id)
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

	if len(messages) > 0 {
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
	}
	result, err := tx.Exec(`UPDATE sessions SET updated_at = datetime('now') WHERE id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	if err := requireSessionUpdated(result, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func getSession(db *sql.DB, id string) (*Session, error) {
	row := db.QueryRow(
		`SELECT id, group_id, name, description, soul_path, mode, active_hand, created_at, updated_at
		 FROM sessions WHERE id = ?`, id,
	)
	var sess Session
	var createdAt, updatedAt string
	err := row.Scan(
		&sess.ID, &sess.GroupID, &sess.Name, &sess.Description, &sess.SoulPath,
		&sess.Mode, &sess.ActiveHand, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	sess.CreatedAt = parseTime(createdAt)
	sess.UpdatedAt = parseTime(updatedAt)
	return &sess, nil
}

func listSessions(db *sql.DB, groupID string) ([]Session, error) {
	rows, err := db.Query(
		`SELECT id, group_id, name, description, soul_path, mode, active_hand, created_at, updated_at
		 FROM sessions WHERE group_id = ? ORDER BY updated_at DESC, id DESC`, groupID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var createdAt, updatedAt string
		if err := rows.Scan(
			&sess.ID, &sess.GroupID, &sess.Name, &sess.Description, &sess.SoulPath,
			&sess.Mode, &sess.ActiveHand, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sess.CreatedAt = parseTime(createdAt)
		sess.UpdatedAt = parseTime(updatedAt)
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

// SetActiveHand 设置会话默认 Hand。
func (s *Store) SetActiveHand(sessionID, handID string) error {
	res, err := s.db.Exec("UPDATE sessions SET active_hand = ?, updated_at = datetime('now') WHERE id = ?", handID, sessionID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("session %q not found", sessionID)
	}
	return nil
}

func requireSessionUpdated(result sql.Result, sessionID string) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated session count: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("session %q not found", sessionID)
	}
	return nil
}

// GetActiveHand 读取会话默认 Hand。列不存在或 session 不存在时返回空字符串。
func (s *Store) GetActiveHand(sessionID string) (string, error) {
	var handID string
	err := s.db.QueryRow("SELECT active_hand FROM sessions WHERE id = ?", sessionID).Scan(&handID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return handID, err
}
