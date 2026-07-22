package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSessionRuntimeMetadataMigratesLegacySchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE session_groups (
			id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', work_dir TEXT NOT NULL,
			soul_path TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY, group_id TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '', soul_path TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (group_id) REFERENCES session_groups(id)
		)`,
		`INSERT INTO session_groups (id, work_dir) VALUES ('group-1', '/legacy')`,
		`INSERT INTO sessions (id, group_id, name, created_at) VALUES
			('conversation-1', 'group-1', 'legacy', '2026-01-02 03:04:05')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session, err := store.GetSession("conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	if session.Mode != "normal" || session.ActiveHand != "" {
		t.Fatalf("migrated runtime state = mode %q, hand %q", session.Mode, session.ActiveHand)
	}
	if session.UpdatedAt.IsZero() || !session.UpdatedAt.Equal(session.CreatedAt) {
		t.Fatalf("migrated timestamps = created %v, updated %v", session.CreatedAt, session.UpdatedAt)
	}
	if err := store.CreateSession("group-1", "conversation-2"); err != nil {
		t.Fatal(err)
	}
	created, err := store.GetSession("conversation-2")
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
		t.Fatalf("new session timestamps after migration = created %v, updated %v", created.CreatedAt, created.UpdatedAt)
	}
}

func TestSessionRuntimeMetadataRejectsInvalidMode(t *testing.T) {
	store := newTestStore(t)
	group, err := store.UpsertGroup("/runtime-mode")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(group.ID, "conversation-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionMode("conversation-1", "unsafe"); err == nil {
		t.Fatal("invalid session mode was accepted")
	}
	if err := store.SetSessionMode("missing", "normal"); err == nil {
		t.Fatal("missing conversation mode update succeeded")
	}
}

func TestSessionRuntimeMetadataCanonicalizesReviewAliases(t *testing.T) {
	store := newTestStore(t)
	group, err := store.UpsertGroup("/runtime-mode-alias")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(group.ID, "conversation-review"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionMode("conversation-review", "review"); err != nil {
		t.Fatal(err)
	}
	session, err := store.GetSession("conversation-review")
	if err != nil || session.Mode != "review" {
		t.Fatalf("review mode = %+v, err %v", session, err)
	}
	if err := store.SetSessionMode("conversation-review", "trust"); err != nil {
		t.Fatal(err)
	}
	session, err = store.GetSession("conversation-review")
	if err != nil || session.Mode != "review" {
		t.Fatalf("legacy alias mode = %+v, err %v", session, err)
	}
	if err := store.SetSessionMode("conversation-review", "ai_review"); err != nil {
		t.Fatal(err)
	}
	session, err = store.GetSession("conversation-review")
	if err != nil || session.Mode != "review" {
		t.Fatalf("ai_review alias mode = %+v, err %v", session, err)
	}
}
