// Package store provides SQLite-backed persistence for sessions, groups, and messages.
package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps an SQLite database connection.
type Store struct {
	db   *sql.DB
	path string
}

// New opens the SQLite database at the given path and creates tables.
func New(path string) (*Store, error) {
	if err := secureDatabasePaths(path); err != nil {
		return nil, fmt.Errorf("secure database paths: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := secureSQLiteFiles(path); err != nil {
		db.Close()
		return nil, fmt.Errorf("secure SQLite files: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// LoadHandConnectionCredential 按 Hand label 读取握手所需的双秘密和主体 ID。
func (s *Store) LoadHandConnectionCredential(label string) (string, string, string, error) {
	if err := validateCredentialLabel(label); err != nil {
		return "", "", "", fmt.Errorf("authentication failed")
	}
	credential, err := s.handCredentialByLabel(label)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", "", fmt.Errorf("authentication failed")
		}
		return "", "", "", fmt.Errorf("load hand connection credential: %w", err)
	}
	return credential.Token, credential.ApplicationKey, strconv.FormatInt(credential.ID, 10), nil
}

// LoadFaceConnectionCredential 按 Face label 读取握手所需的双秘密和主体 ID。
func (s *Store) LoadFaceConnectionCredential(label string) (string, string, string, error) {
	if err := validateCredentialLabel(label); err != nil {
		return "", "", "", fmt.Errorf("authentication failed")
	}
	credential, err := s.faceTokenByLabel(label)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", "", fmt.Errorf("authentication failed")
		}
		return "", "", "", fmt.Errorf("load face connection credential: %w", err)
	}
	return credential.Token, credential.ApplicationKey, strconv.FormatInt(credential.ID, 10), nil
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}
	if err := secureSQLiteFiles(s.path); err != nil {
		return fmt.Errorf("secure SQLite files after enabling WAL: %w", err)
	}
	if _, err := s.db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
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
			mode        TEXT NOT NULL DEFAULT 'normal',
			active_hand TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (group_id) REFERENCES session_groups(id)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id  TEXT NOT NULL,
			role        TEXT NOT NULL,
				content     TEXT NOT NULL DEFAULT '',
				request_id  TEXT NOT NULL DEFAULT '',
				tool_id     TEXT NOT NULL DEFAULT '',
			tool_calls  TEXT NOT NULL DEFAULT '',
			compact_projection TEXT NOT NULL DEFAULT '',
			seq         INTEGER NOT NULL,
			created_at  TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, seq)`,
		`CREATE TABLE IF NOT EXISTS context_summaries (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			parent_summary_id TEXT NOT NULL DEFAULT '',
			supersedes_summary_id TEXT NOT NULL DEFAULT '',
			from_seq INTEGER NOT NULL,
			to_seq INTEGER NOT NULL,
			summary TEXT NOT NULL,
			summary_digest TEXT NOT NULL,
			source_digest TEXT NOT NULL,
			contract_digest TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			model_id TEXT NOT NULL,
			profile TEXT NOT NULL,
			policy_version TEXT NOT NULL,
			projection_version TEXT NOT NULL,
			generation_mode TEXT NOT NULL,
			generation_key TEXT NOT NULL DEFAULT '',
			source_estimated_tokens INTEGER NOT NULL,
			summary_estimated_tokens INTEGER NOT NULL,
			input_tokens INTEGER NOT NULL,
			output_tokens INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			UNIQUE(session_id, from_seq, to_seq, contract_digest),
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_context_summaries_session_to
			ON context_summaries(session_id, to_seq DESC)`,
		`CREATE TABLE IF NOT EXISTS session_runtime (
			session_id TEXT PRIMARY KEY,
			active_summary_id TEXT NOT NULL DEFAULT '',
			history_generation INTEGER NOT NULL DEFAULT 0,
			compact_generation INTEGER NOT NULL DEFAULT 0,
			history_view_generation INTEGER NOT NULL DEFAULT 0,
			pending_compact INTEGER NOT NULL DEFAULT 0,
			pending_compact_id TEXT NOT NULL DEFAULT '',
			pending_attempt INTEGER NOT NULL DEFAULT 0,
			pending_not_before INTEGER NOT NULL DEFAULT 0,
			snapshot_version INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_runtime_pending
			ON session_runtime(pending_compact, pending_not_before)`,
		`CREATE TABLE IF NOT EXISTS hand_tokens (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			label      TEXT NOT NULL,
			token      TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS hand_credentials (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			label           TEXT NOT NULL UNIQUE,
			token           TEXT NOT NULL UNIQUE,
			application_key TEXT NOT NULL UNIQUE,
			created_at      TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS face_tokens (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			label           TEXT NOT NULL UNIQUE,
			token           TEXT NOT NULL UNIQUE,
			application_key TEXT NOT NULL UNIQUE,
			scopes          TEXT NOT NULL,
			created_at      TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS remote_runs (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT '',
				request_id TEXT NOT NULL DEFAULT '',
				trace_id TEXT NOT NULL DEFAULT '',
				span_id TEXT NOT NULL DEFAULT '',
				parent_span_id TEXT NOT NULL DEFAULT '',
				group_id TEXT NOT NULL DEFAULT '',
				principal_id TEXT NOT NULL DEFAULT '',
				lifecycle_source TEXT NOT NULL DEFAULT '',
				node_id TEXT NOT NULL DEFAULT '',
				hand_id TEXT NOT NULL,
			tool TEXT NOT NULL,
			args_digest TEXT NOT NULL DEFAULT '',
			approval_source TEXT NOT NULL DEFAULT '',
			approval_mode TEXT NOT NULL DEFAULT '',
			approval_reason TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			reject_code TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			sent_at INTEGER NOT NULL DEFAULT 0,
			accepted_at INTEGER NOT NULL DEFAULT 0,
			finished_at INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS remote_run_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			from_status TEXT NOT NULL DEFAULT '',
			to_status TEXT NOT NULL,
			type TEXT NOT NULL,
			message TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			UNIQUE(run_id, seq),
			FOREIGN KEY (run_id) REFERENCES remote_runs(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_remote_runs_session ON remote_runs(session_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_remote_runs_hand ON remote_runs(hand_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_remote_runs_status ON remote_runs(status)`,
		`CREATE INDEX IF NOT EXISTS idx_remote_run_events_run ON remote_run_events(run_id, seq)`,
		`CREATE TABLE IF NOT EXISTS remote_tasks (
			task_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			hand_id TEXT NOT NULL,
			tool TEXT NOT NULL,
			args_digest TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			started_at INTEGER NOT NULL DEFAULT 0,
			finished_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			log_bytes INTEGER NOT NULL DEFAULT 0,
			truncated INTEGER NOT NULL DEFAULT 0,
			stale INTEGER NOT NULL DEFAULT 1,
			error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_remote_tasks_session ON remote_tasks(session_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_remote_tasks_hand ON remote_tasks(hand_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_remote_tasks_status ON remote_tasks(status)`,
		`CREATE TABLE IF NOT EXISTS approval_audits (
			approval_id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL,
				request_id TEXT NOT NULL DEFAULT '',
				run_id TEXT NOT NULL DEFAULT '',
				trace_id TEXT NOT NULL DEFAULT '',
				span_id TEXT NOT NULL DEFAULT '',
				parent_span_id TEXT NOT NULL DEFAULT '',
				group_id TEXT NOT NULL DEFAULT '',
				principal_id TEXT NOT NULL DEFAULT '',
				lifecycle_source TEXT NOT NULL DEFAULT '',
				node_id TEXT NOT NULL DEFAULT '',
				tool TEXT NOT NULL,
			reason TEXT NOT NULL,
			args_digest TEXT NOT NULL,
			status TEXT NOT NULL,
			decision TEXT NOT NULL DEFAULT '',
			actor_id TEXT NOT NULL DEFAULT '',
			actor_label TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			resolution_reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			resolved_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_approval_audits_conversation ON approval_audits(conversation_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_approval_audits_status ON approval_audits(status, expires_at)`,
		`CREATE TABLE IF NOT EXISTS management_audits (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			request_id TEXT NOT NULL,
			source TEXT NOT NULL,
			actor TEXT NOT NULL DEFAULT '',
			operation TEXT NOT NULL,
			target_type TEXT NOT NULL,
			target_id TEXT NOT NULL DEFAULT '',
			target_label TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			code TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_management_audits_request ON management_audits(request_id)`,
		`CREATE INDEX IF NOT EXISTS idx_management_audits_created ON management_audits(created_at)`,
		`CREATE TABLE IF NOT EXISTS security_decisions (
			id TEXT PRIMARY KEY,
			trace_id TEXT NOT NULL,
			span_id TEXT NOT NULL,
			request_id TEXT NOT NULL DEFAULT '',
			conversation_id TEXT NOT NULL DEFAULT '',
			group_id TEXT NOT NULL DEFAULT '',
			principal_id TEXT NOT NULL DEFAULT '',
			node_id TEXT NOT NULL DEFAULT '',
			action_kind TEXT NOT NULL,
			resource_name TEXT NOT NULL DEFAULT '',
			target_node TEXT NOT NULL DEFAULT '',
			input_digest TEXT NOT NULL DEFAULT '',
			risk_labels TEXT NOT NULL DEFAULT '[]',
			decision TEXT NOT NULL,
			reason_code TEXT NOT NULL DEFAULT '',
			rule_id TEXT NOT NULL DEFAULT '',
			policy_version TEXT NOT NULL DEFAULT '',
				approval_id TEXT NOT NULL DEFAULT '',
				run_id TEXT NOT NULL DEFAULT '',
				details_redacted TEXT NOT NULL DEFAULT '{}',
				created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_security_decisions_trace ON security_decisions(trace_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_security_decisions_conversation ON security_decisions(conversation_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS lifecycle_outbox (
			id TEXT PRIMARY KEY,
			event_type TEXT NOT NULL,
			schema_version INTEGER NOT NULL,
			trace_id TEXT NOT NULL,
			span_id TEXT NOT NULL,
			subject_id TEXT NOT NULL DEFAULT '',
			payload_redacted TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			available_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			delivered_at INTEGER NOT NULL DEFAULT 0,
			dead_letter_at INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_lifecycle_outbox_due ON lifecycle_outbox(delivered_at, dead_letter_at, available_at)`,
	}
	for _, stmt := range statements {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	if err := s.addColumnIfNotExists("sessions", "active_hand", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate active_hand: %w", err)
	}
	if err := s.addColumnIfNotExists("sessions", "mode", "TEXT NOT NULL DEFAULT 'normal'"); err != nil {
		return fmt.Errorf("migrate session mode: %w", err)
	}
	if err := s.addColumnIfNotExists("sessions", "updated_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate session updated_at: %w", err)
	}
	if err := s.addColumnIfNotExists("messages", "request_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate message request ID: %w", err)
	}
	if err := s.addColumnIfNotExists("messages", "compact_projection", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate message compact projection: %w", err)
	}
	if _, err := s.db.Exec(`INSERT INTO session_runtime (
		session_id, history_generation, history_view_generation, snapshot_version, updated_at
	) SELECT sessions.id, COALESCE(MAX(messages.seq), 0), COALESCE(MAX(messages.seq), 0),
		COALESCE(MAX(messages.seq), 0), ?
		FROM sessions LEFT JOIN messages ON messages.session_id = sessions.id
		GROUP BY sessions.id ON CONFLICT(session_id) DO NOTHING`, time.Now().UTC().UnixMilli()); err != nil {
		return fmt.Errorf("initialize session runtime: %w", err)
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_session_seq ON messages(session_id, seq)`); err != nil {
		return fmt.Errorf("migrate message sequence uniqueness: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET updated_at = created_at WHERE updated_at = ''`); err != nil {
		return fmt.Errorf("backfill session updated_at: %w", err)
	}
	if err := s.addColumnIfNotExists("hand_tokens", "hand_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate Hand token identity: %w", err)
	}
	if err := s.addColumnIfNotExists("remote_runs", "request_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate remote run request ID: %w", err)
	}
	for _, column := range []string{"trace_id", "span_id", "parent_span_id", "group_id", "principal_id", "lifecycle_source", "node_id"} {
		if err := s.addColumnIfNotExists("remote_runs", column, "TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("migrate remote run lifecycle %s: %w", column, err)
		}
	}
	if err := s.addColumnIfNotExists("remote_run_events", "progress_seq", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate remote progress seq: %w", err)
	}
	if err := s.addColumnIfNotExists("remote_run_events", "kind", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate remote progress kind: %w", err)
	}
	if err := s.addColumnIfNotExists("remote_run_events", "gap", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate remote progress gap: %w", err)
	}
	if err := s.addColumnIfNotExists("security_decisions", "details_redacted", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("migrate security decision details: %w", err)
	}
	if err := s.addColumnIfNotExists("security_decisions", "target_node", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate security decision target node: %w", err)
	}
	if err := s.addColumnIfNotExists("security_decisions", "risk_labels", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return fmt.Errorf("migrate security decision risk labels: %w", err)
	}
	for _, column := range []string{"trace_id", "span_id", "parent_span_id", "group_id", "principal_id", "lifecycle_source", "node_id"} {
		if err := s.addColumnIfNotExists("approval_audits", column, "TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("migrate approval lifecycle %s: %w", column, err)
		}
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_remote_run_progress_unique
		ON remote_run_events(run_id, progress_seq) WHERE progress_seq > 0`); err != nil {
		return fmt.Errorf("migrate remote progress uniqueness: %w", err)
	}

	return nil
}

const sqliteTimeFormat = "2006-01-02 15:04:05"

func parseTime(s string) time.Time {
	t, _ := time.Parse(sqliteTimeFormat, s)
	return t
}

func (s *Store) addColumnIfNotExists(table, column, colDef string) error {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dfltVal sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltVal, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	_, err = s.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + colDef)
	return err
}
