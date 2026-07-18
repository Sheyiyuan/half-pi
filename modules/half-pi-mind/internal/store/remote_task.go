package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

// CreateRemoteTask 创建不包含原始参数和 Hand 路径的后台任务元数据。
func (s *Store) CreateRemoteTask(task remoteexec.Task) error {
	return insertRemoteTask(s.db, task)
}

func insertRemoteTask(db interface {
	Exec(string, ...any) (sql.Result, error)
}, task remoteexec.Task) error {
	_, err := db.Exec(`INSERT INTO remote_tasks
		(task_id, session_id, hand_id, tool, args_digest, status, created_at, started_at, finished_at, updated_at, log_bytes, truncated, stale, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, task.TaskID, task.SessionID, task.HandID,
		task.Tool, task.ArgsDigest, task.Status, millis(task.CreatedAt), millis(task.StartedAt), millis(task.FinishedAt),
		millis(task.UpdatedAt), task.LogBytes, task.Truncated, task.Stale, task.Error)
	if err != nil {
		return fmt.Errorf("insert remote task: %w", err)
	}
	return nil
}

// UpdateRemoteTask 更新 Hand 返回的任务快照。
func (s *Store) UpdateRemoteTask(task remoteexec.Task) error {
	result, err := s.db.Exec(`UPDATE remote_tasks SET status = ?, started_at = ?, finished_at = ?, updated_at = ?,
		log_bytes = ?, truncated = ?, stale = ?, error = ? WHERE task_id = ? AND session_id = ? AND hand_id = ? AND tool = ?
		AND updated_at <= ? AND (status NOT IN ('succeeded','failed','cancelled','timed_out','lost') OR status = ?)`,
		task.Status, millis(task.StartedAt), millis(task.FinishedAt), millis(task.UpdatedAt), task.LogBytes,
		task.Truncated, task.Stale, task.Error, task.TaskID, task.SessionID, task.HandID, task.Tool,
		millis(task.UpdatedAt), task.Status)
	if err != nil {
		return fmt.Errorf("update remote task: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("remote task %q update is stale, terminal, or identity mismatched", task.TaskID)
	}
	return nil
}

// GetRemoteTask 查询任务元数据。
func (s *Store) GetRemoteTask(taskID string) (remoteexec.Task, error) {
	return scanRemoteTask(s.db.QueryRow(remoteTaskSelect+` WHERE task_id = ?`, taskID))
}

// ListRemoteTasksBySession 查询会话拥有的任务，按创建时间倒序返回。
func (s *Store) ListRemoteTasksBySession(sessionID string) ([]remoteexec.Task, error) {
	rows, err := s.db.Query(remoteTaskSelect+` WHERE session_id = ? ORDER BY created_at DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []remoteexec.Task
	for rows.Next() {
		task, err := scanRemoteTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// DeleteRemoteTask 删除指定任务元数据。
func (s *Store) DeleteRemoteTask(taskID string) error {
	result, err := s.db.Exec(`DELETE FROM remote_tasks WHERE task_id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("delete remote task: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("remote task %q not found", taskID)
	}
	return nil
}

// RecoverRemoteTasks 将遗留非终态快照标记为 stale，保留合法协议状态以便重连对账。
func (s *Store) RecoverRemoteTasks() (int, error) {
	result, err := s.db.Exec(`UPDATE remote_tasks SET stale = 1, updated_at = ?
		WHERE stale = 0 AND status NOT IN ('succeeded','failed','cancelled','timed_out','lost')`, time.Now().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("recover remote tasks: %w", err)
	}
	n, err := result.RowsAffected()
	return int(n), err
}

const remoteTaskSelect = `SELECT task_id, session_id, hand_id, tool, args_digest, status, created_at,
	started_at, finished_at, updated_at, log_bytes, truncated, stale, error FROM remote_tasks`

func scanRemoteTask(row scanner) (remoteexec.Task, error) {
	var task remoteexec.Task
	var created, started, finished, updated int64
	err := row.Scan(&task.TaskID, &task.SessionID, &task.HandID, &task.Tool, &task.ArgsDigest, &task.Status,
		&created, &started, &finished, &updated, &task.LogBytes, &task.Truncated, &task.Stale, &task.Error)
	if err != nil {
		return remoteexec.Task{}, err
	}
	task.CreatedAt = time.UnixMilli(created)
	task.UpdatedAt = time.UnixMilli(updated)
	if started > 0 {
		task.StartedAt = time.UnixMilli(started)
	}
	if finished > 0 {
		task.FinishedAt = time.UnixMilli(finished)
	}
	return task, nil
}

func millis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}

var _ remoteexec.TaskStore = (*Store)(nil)
