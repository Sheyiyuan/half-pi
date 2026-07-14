// Package store provides SQLite-backed persistence for sessions, groups, and messages.
package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps an SQLite database connection.
type Store struct {
	db *sql.DB
}

// New opens the SQLite database at the given path and creates tables.
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS session_groups (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL DEFAULT '',
			work_dir    TEXT NOT NULL,
			soul_path   TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id          TEXT PRIMARY KEY,
			group_id    TEXT NOT NULL,
			soul_path   TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (group_id) REFERENCES session_groups(id)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id  TEXT NOT NULL,
			role        TEXT NOT NULL,
			content     TEXT NOT NULL DEFAULT '',
			tool_id     TEXT NOT NULL DEFAULT '',
			tool_calls  TEXT NOT NULL DEFAULT '',
			seq         INTEGER NOT NULL,
			created_at  TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, seq)`,
	}
	for _, stmt := range statements {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	return nil
}

const sqliteTimeFormat = "2006-01-02 15:04:05"

func parseTime(s string) time.Time {
	t, _ := time.Parse(sqliteTimeFormat, s)
	return t
}

type SessionGroup struct {
	ID        string
	Name      string
	WorkDir   string
	SoulPath  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func GroupID(workDir string) string {
	h := sha256.Sum256([]byte(workDir))
	return fmt.Sprintf("%x", h[:8])
}

func (s *Store) UpsertGroup(workDir string) (*SessionGroup, error) {
	id := GroupID(workDir)
	return upsertGroup(s.db, id, workDir)
}

func (s *Store) GetGroup(id string) (*SessionGroup, error) {
	return getGroup(s.db, id)
}

func (s *Store) GetGroupByWorkDir(workDir string) (*SessionGroup, error) {
	return getGroup(s.db, GroupID(workDir))
}

func (s *Store) ListGroups() ([]SessionGroup, error) {
	return listGroups(s.db)
}

func upsertGroup(db *sql.DB, id, workDir string) (*SessionGroup, error) {
	_, err := db.Exec(
		`INSERT INTO session_groups (id, work_dir) VALUES (?, ?)
		 ON CONFLICT(id) DO UPDATE SET work_dir = excluded.work_dir, updated_at = datetime('now')`,
		id, workDir,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert group: %w", err)
	}
	return getGroup(db, id)
}

func getGroup(db *sql.DB, id string) (*SessionGroup, error) {
	row := db.QueryRow(
		`SELECT id, name, work_dir, soul_path, created_at, updated_at FROM session_groups WHERE id = ?`, id,
	)
	var g SessionGroup
	var ca, ua string
	err := row.Scan(&g.ID, &g.Name, &g.WorkDir, &g.SoulPath, &ca, &ua)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get group: %w", err)
	}
	g.CreatedAt = parseTime(ca)
	g.UpdatedAt = parseTime(ua)
	return &g, nil
}

func listGroups(db *sql.DB) ([]SessionGroup, error) {
	rows, err := db.Query(
		`SELECT id, name, work_dir, soul_path, created_at, updated_at FROM session_groups ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	var groups []SessionGroup
	for rows.Next() {
		var g SessionGroup
		var ca, ua string
		if err := rows.Scan(&g.ID, &g.Name, &g.WorkDir, &g.SoulPath, &ca, &ua); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		g.CreatedAt = parseTime(ca)
		g.UpdatedAt = parseTime(ua)
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// ── Session ──

type Session struct {
	ID        string
	GroupID   string
	SoulPath  string
	CreatedAt time.Time
}

func (s *Store) CreateSession(groupID, id string) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, group_id) VALUES (?, ?)`, id, groupID,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) GetSession(id string) (*Session, error) {
	row := s.db.QueryRow(
		`SELECT id, group_id, soul_path, created_at FROM sessions WHERE id = ?`, id,
	)
	var sess Session
	var ca string
	err := row.Scan(&sess.ID, &sess.GroupID, &sess.SoulPath, &ca)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	sess.CreatedAt = parseTime(ca)
	return &sess, nil
}

func (s *Store) ListSessions(groupID string) ([]Session, error) {
	rows, err := s.db.Query(
		`SELECT id, group_id, soul_path, created_at FROM sessions WHERE group_id = ? ORDER BY created_at DESC`, groupID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var ca string
		if err := rows.Scan(&sess.ID, &sess.GroupID, &sess.SoulPath, &ca); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sess.CreatedAt = parseTime(ca)
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// ── Message ──

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
