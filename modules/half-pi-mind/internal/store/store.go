// Package store provides SQLite-backed persistence for sessions, groups, and messages.
package store

import (
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
			name        TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
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
		`CREATE TABLE IF NOT EXISTS hand_tokens (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			label      TEXT NOT NULL,
			token      TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
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
