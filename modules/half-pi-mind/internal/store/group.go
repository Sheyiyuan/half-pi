package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"
)

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
