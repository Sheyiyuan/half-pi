// Package taskmanager 提供 Hand 进程级的持久化后台任务管理。
package taskmanager

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

var taskIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

var (
	// ErrConflictingDuplicate 表示 task_id 已绑定到另一请求。
	ErrConflictingDuplicate = errors.New("task_id is already bound to another request")
	// ErrTaskQuota 表示持久化任务数量已达到上限。
	ErrTaskQuota = errors.New("retained task quota exceeded")
)

// Config 配置任务数据库、日志和执行配额。
type Config struct {
	Dir         string
	MaxRunning  int
	MaxRuntime  time.Duration
	MaxLogBytes int64
	Retention   time.Duration
	MaxRetained int
}

// Task 是可公开查询的后台任务元数据。
type Task struct {
	ID         string
	Tool       string
	Status     protocol.TaskStatus
	CreatedAt  int64
	StartedAt  int64
	FinishedAt int64
	LogBytes   int64
	Truncated  bool
	Error      string
}

// ExecuteFunc 执行已通过 Hand 守门的工具。
type ExecuteFunc func(context.Context, executor.ProgressFunc) *executor.ToolResult

type runningTask struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Manager 管理跨 WebSocket 连接存活的后台任务。
type Manager struct {
	db          *sql.DB
	cfg         Config
	ctx         context.Context
	cancel      context.CancelFunc
	sem         chan struct{}
	mu          sync.Mutex
	logsMu      sync.Mutex
	running     map[string]*runningTask
	logs        map[string]*sync.Mutex
	wg          sync.WaitGroup
	cleanupDone chan struct{}
	closed      bool
}

const closeWait = 2 * time.Second

// New 创建任务目录、数据库和日志目录，并将上次进程遗留任务标记为 lost。
func New(cfg Config) (*Manager, error) {
	if cfg.Dir == "" || cfg.MaxRunning <= 0 || cfg.MaxRuntime <= 0 || cfg.MaxRuntime > 24*time.Hour || cfg.MaxLogBytes <= 0 || cfg.Retention <= 0 || cfg.MaxRetained <= 0 {
		return nil, fmt.Errorf("invalid task manager config")
	}
	if err := secureDirectory(cfg.Dir); err != nil {
		return nil, fmt.Errorf("create task directory: %w", err)
	}
	logsDir := filepath.Join(cfg.Dir, "logs")
	if err := secureDirectory(logsDir); err != nil {
		return nil, fmt.Errorf("create task log directory: %w", err)
	}
	dbPath := filepath.Join(cfg.Dir, "tasks.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := validateOptionalRegular(dbPath + suffix); err != nil {
			return nil, fmt.Errorf("validate SQLite file: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open task database: %w", err)
	}
	db.SetMaxOpenConns(1)
	m := &Manager{db: db, cfg: cfg, sem: make(chan struct{}, cfg.MaxRunning), running: make(map[string]*runningTask), logs: make(map[string]*sync.Mutex), cleanupDone: make(chan struct{})}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	if err := m.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(dbPath, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("secure task database: %w", err)
	}
	if err := m.secureSQLiteFiles(); err != nil {
		db.Close()
		return nil, err
	}
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`UPDATE tasks SET status = ?, started_at = CASE WHEN started_at = 0 THEN created_at ELSE started_at END, finished_at = ?, error = ? WHERE status IN (?, ?)`,
		protocol.TaskLost, now, "Hand process stopped before task completion", protocol.TaskPending, protocol.TaskRunning); err != nil {
		db.Close()
		return nil, fmt.Errorf("recover tasks: %w", err)
	}
	if err := m.reconcileLogs(); err != nil {
		db.Close()
		return nil, err
	}
	if err := m.prune(); err != nil {
		db.Close()
		return nil, err
	}
	go m.cleanupLoop()
	return m, nil
}

func (m *Manager) migrate() error {
	if _, err := m.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("enable task WAL: %w", err)
	}
	_, err := m.db.Exec(`CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		tool TEXT NOT NULL,
		approval_digest TEXT NOT NULL,
		args_digest TEXT NOT NULL,
		approval_source TEXT NOT NULL DEFAULT '',
		approval_mode TEXT NOT NULL DEFAULT '',
		approval_approver TEXT NOT NULL DEFAULT '',
		max_runtime_ms INTEGER NOT NULL,
		status TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		started_at INTEGER NOT NULL DEFAULT 0,
		finished_at INTEGER NOT NULL DEFAULT 0,
		log_bytes INTEGER NOT NULL DEFAULT 0,
		truncated INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		log_error TEXT NOT NULL DEFAULT '',
		details_retained INTEGER NOT NULL DEFAULT 1,
		cleanup_error TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		return fmt.Errorf("migrate task database: %w", err)
	}
	for _, statement := range []string{
		`ALTER TABLE tasks ADD COLUMN log_error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN details_retained INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE tasks ADD COLUMN cleanup_error TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := m.db.Exec(statement); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("migrate task database column: %w", err)
		}
	}
	return nil
}

func secureDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s is not a real directory", path)
	}
	return os.Chmod(path, 0700)
}

func validateOptionalRegular(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

func (m *Manager) secureSQLiteFiles() error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := filepath.Join(m.cfg.Dir, "tasks.db") + suffix
		if err := validateOptionalRegular(path); err != nil {
			return fmt.Errorf("validate SQLite file: %w", err)
		}
		if _, err := os.Lstat(path); err == nil {
			if err := os.Chmod(path, 0600); err != nil {
				return fmt.Errorf("secure SQLite file: %w", err)
			}
		}
	}
	return nil
}

// Start 持久化任务后异步执行。exact duplicate 返回已有任务且不再次执行。
func (m *Manager) Start(rpc protocol.RPC, approvalDigest string, execute ExecuteFunc) (Task, bool, error) {
	if rpc.Background == nil || rpc.Background.TaskID != rpc.RunID || !taskIDPattern.MatchString(rpc.RunID) {
		return Task{}, false, fmt.Errorf("task_id must be 16 lowercase hexadecimal characters")
	}
	argsDigest, err := digestArgs(rpc.Args)
	if err != nil {
		return Task{}, false, err
	}
	if execute == nil {
		return Task{}, false, fmt.Errorf("execute function is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Task{}, false, fmt.Errorf("task manager is closed")
	}
	if existing, err := m.get(rpc.RunID); err == nil {
		var storedApproval, storedArgs string
		if err := m.db.QueryRow(`SELECT approval_digest, args_digest FROM tasks WHERE id = ?`, rpc.RunID).Scan(&storedApproval, &storedArgs); err != nil {
			return Task{}, false, err
		}
		if storedApproval != approvalDigest || storedArgs != argsDigest {
			return Task{}, false, ErrConflictingDuplicate
		}
		return existing, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, err
	}
	if err := m.prune(); err != nil {
		return Task{}, false, err
	}
	if err := m.pruneToCapacity(); err != nil {
		return Task{}, false, err
	}
	var count int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE details_retained = 1`).Scan(&count); err != nil {
		return Task{}, false, err
	}
	if count >= m.cfg.MaxRetained {
		return Task{}, false, ErrTaskQuota
	}
	runtime := min(time.Duration(rpc.Background.MaxRuntimeMS)*time.Millisecond, m.cfg.MaxRuntime)
	now := time.Now().UnixMilli()
	approval := rpc.Approval
	_, err = m.db.Exec(`INSERT INTO tasks
		(id, tool, approval_digest, args_digest, approval_source, approval_mode, approval_approver, max_runtime_ms, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, rpc.RunID, rpc.Tool, approvalDigest, argsDigest,
		approval.Source, approval.Mode, approval.Approver, runtime.Milliseconds(), protocol.TaskPending, now)
	if err != nil {
		return Task{}, false, fmt.Errorf("insert task: %w", err)
	}
	task := Task{ID: rpc.RunID, Tool: rpc.Tool, Status: protocol.TaskPending, CreatedAt: now}
	m.wg.Add(1)
	go m.run(task.ID, rpc.Tool, runtime, execute)
	return task, true, nil
}

func (m *Manager) run(id, tool string, runtime time.Duration, execute ExecuteFunc) {
	defer m.wg.Done()
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-m.ctx.Done():
		m.finish(id, protocol.TaskLost, "Hand process stopped before task start")
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, runtime)
	running := &runningTask{cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		m.finish(id, protocol.TaskLost, "Hand process stopped before task start")
		return
	}
	m.running[id] = running
	started := time.Now().UnixMilli()
	updateResult, err := m.db.Exec(`UPDATE tasks SET status = ?, started_at = ? WHERE id = ? AND status = ?`, protocol.TaskRunning, started, id, protocol.TaskPending)
	if err != nil {
		m.mu.Unlock()
		m.finish(id, protocol.TaskFailed, err.Error())
		m.removeRunning(id, running)
		cancel()
		return
	}
	updated, err := updateResult.RowsAffected()
	if err != nil || updated != 1 {
		m.mu.Unlock()
		m.removeRunning(id, running)
		cancel()
		return
	}
	m.mu.Unlock()

	result := execute(executor.WithFinalOutputLimit(ctx, m.cfg.MaxLogBytes), func(progress executor.Progress) {
		if supportsProgress(tool) {
			_ = m.appendLog(id, []byte(progress.Data))
		}
	})
	status, errMsg := protocol.TaskSucceeded, ""
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status, errMsg = protocol.TaskTimedOut, ctx.Err().Error()
		} else {
			status, errMsg = protocol.TaskCancelled, ctx.Err().Error()
		}
	} else if result == nil {
		status, errMsg = protocol.TaskFailed, "tool returned nil result"
	} else {
		if !supportsProgress(tool) {
			_ = m.appendLog(id, []byte(result.Output))
			_ = m.appendLog(id, []byte(result.Error))
		}
		if !result.Success {
			status, errMsg = protocol.TaskFailed, "tool execution failed"
		}
	}
	m.finish(id, status, errMsg)
	m.removeRunning(id, running)
	cancel()
}

func (m *Manager) removeRunning(id string, running *runningTask) {
	m.mu.Lock()
	if m.running[id] == running {
		delete(m.running, id)
		close(running.done)
	}
	m.mu.Unlock()
}

func (m *Manager) finish(id string, status protocol.TaskStatus, errMsg string) {
	_, _ = m.db.Exec(`UPDATE tasks SET status = ?, started_at = CASE WHEN started_at = 0 THEN created_at ELSE started_at END, finished_at = ?, error = ? WHERE id = ? AND status IN (?, ?)`,
		status, time.Now().UnixMilli(), errMsg, id, protocol.TaskPending, protocol.TaskRunning)
}

// Status 返回任务状态。
func (m *Manager) Status(id string) (Task, error) {
	if !taskIDPattern.MatchString(id) {
		return Task{}, sql.ErrNoRows
	}
	return m.get(id)
}

func (m *Manager) get(id string) (Task, error) {
	var t Task
	var truncated int
	err := m.db.QueryRow(`SELECT id, tool, status, created_at, started_at, finished_at, log_bytes, truncated, error FROM tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.Tool, &t.Status, &t.CreatedAt, &t.StartedAt, &t.FinishedAt, &t.LogBytes, &truncated, &t.Error)
	t.Truncated = truncated != 0
	return t, err
}

// ReadLog 从精确字节偏移读取有界日志。
func (m *Manager) ReadLog(id string, offset int64, limit int) ([]byte, int64, bool, bool, error) {
	t, err := m.Status(id)
	if err != nil {
		return nil, offset, false, false, err
	}
	if offset >= t.LogBytes {
		return []byte{}, offset, true, t.Truncated, nil
	}
	lock := m.logLock(id)
	lock.Lock()
	defer lock.Unlock()
	f, err := openRegularLog(m.logPath(id))
	if err != nil {
		return nil, offset, false, t.Truncated, err
	}
	defer f.Close()
	n := min(int64(limit), t.LogBytes-offset)
	data := make([]byte, n)
	read, err := f.ReadAt(data, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, offset, false, t.Truncated, err
	}
	data = data[:read]
	next := offset + int64(read)
	return data, next, next >= t.LogBytes, t.Truncated, nil
}

func (m *Manager) appendLog(id string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	lock := m.logLock(id)
	lock.Lock()
	defer lock.Unlock()
	t, err := m.get(id)
	if err != nil {
		return err
	}
	path := m.logPath(id)
	f, size, err := openAppendLog(path)
	if err != nil {
		m.markLogError(id, err)
		return err
	}
	defer f.Close()
	if size != t.LogBytes {
		if _, err := m.db.Exec(`UPDATE tasks SET log_bytes = ?, truncated = truncated OR ?, log_error = ? WHERE id = ?`, size, size > m.cfg.MaxLogBytes, "log length reconciled before append", id); err != nil {
			return err
		}
		t.LogBytes = size
	}
	remaining := m.cfg.MaxLogBytes - t.LogBytes
	if remaining <= 0 {
		_, err = m.db.Exec(`UPDATE tasks SET truncated = 1 WHERE id = ?`, id)
		return err
	}
	write := data
	truncated := false
	if int64(len(write)) > remaining {
		write = write[:remaining]
		truncated = true
	}
	n, writeErr := f.Write(write)
	if syncErr := f.Sync(); syncErr != nil && writeErr == nil {
		writeErr = syncErr
	}
	actual, statErr := f.Stat()
	if statErr != nil {
		m.markLogError(id, statErr)
		return statErr
	}
	actualSize := actual.Size()
	logErr := ""
	if writeErr != nil || n != len(write) {
		truncated = true
		if writeErr != nil {
			logErr = writeErr.Error()
		} else {
			logErr = io.ErrShortWrite.Error()
		}
	}
	_, err = m.db.Exec(`UPDATE tasks SET log_bytes = ?, truncated = truncated OR ?, log_error = ? WHERE id = ?`, actualSize, truncated, logErr, id)
	if err == nil && writeErr != nil {
		return writeErr
	}
	return err
}

func openAppendLog(path string) (*os.File, int64, error) {
	before, err := os.Lstat(path)
	flags := os.O_WRONLY | os.O_APPEND
	if errors.Is(err, os.ErrNotExist) {
		flags |= os.O_CREATE | os.O_EXCL
	} else if err != nil {
		return nil, 0, err
	} else if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, 0, err
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, 0, fmt.Errorf("log is not regular: %w", err)
	}
	if before != nil && !os.SameFile(before, info) {
		f.Close()
		return nil, 0, fmt.Errorf("log changed while opening")
	}
	return f, info.Size(), nil
}

func openRegularLog(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		return nil, fmt.Errorf("log changed while opening")
	}
	return f, nil
}

func (m *Manager) markLogError(id string, err error) {
	_, _ = m.db.Exec(`UPDATE tasks SET truncated = 1, log_error = ? WHERE id = ?`, err.Error(), id)
}

func (m *Manager) logLock(id string) *sync.Mutex {
	m.logsMu.Lock()
	defer m.logsMu.Unlock()
	lock := m.logs[id]
	if lock == nil {
		lock = &sync.Mutex{}
		m.logs[id] = lock
	}
	return lock
}

func (m *Manager) logPath(id string) string {
	if !taskIDPattern.MatchString(id) {
		panic("unsafe task id used for log path")
	}
	return filepath.Join(m.cfg.Dir, "logs", id+".log")
}

// Cancel 幂等取消任务。
func (m *Manager) Cancel(id string) (protocol.TaskCancelStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.Status(id)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.TaskCancelUnknownTask, nil
	}
	if err != nil {
		return protocol.TaskCancelFailed, err
	}
	if protocol.IsTerminalTaskStatus(t.Status) {
		if t.Status == protocol.TaskCancelled {
			return protocol.TaskCancelCancelled, nil
		}
		return protocol.TaskCancelAlreadyDone, nil
	}
	running := m.running[id]
	if running == nil {
		m.finish(id, protocol.TaskCancelled, "cancelled before execution")
		return protocol.TaskCancelCancelled, nil
	}
	running.cancel()
	m.mu.Unlock()
	select {
	case <-running.done:
		m.mu.Lock()
		return protocol.TaskCancelCancelled, nil
	case <-time.After(1500 * time.Millisecond):
		m.mu.Lock()
		return protocol.TaskCancelFailed, fmt.Errorf("task did not stop before cancel timeout")
	}
}

// Close 取消并等待进程内任务落入终态，然后关闭数据库。
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.cancel()
	for _, running := range m.running {
		running.cancel()
	}
	m.mu.Unlock()
	select {
	case <-waitGroupDone(&m.wg):
		<-m.cleanupDone
		return m.db.Close()
	case <-time.After(closeWait):
		_, err := m.db.Exec(`UPDATE tasks SET status = ?, started_at = CASE WHEN started_at = 0 THEN created_at ELSE started_at END, finished_at = ?, error = ? WHERE status IN (?, ?)`,
			protocol.TaskLost, time.Now().UnixMilli(), "Hand shutdown timed out waiting for tool", protocol.TaskPending, protocol.TaskRunning)
		go func() {
			m.wg.Wait()
			<-m.cleanupDone
			_ = m.db.Close()
		}()
		return err
	}
}

func waitGroupDone(wg *sync.WaitGroup) <-chan struct{} {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	return done
}

func (m *Manager) prune() error {
	cutoff := time.Now().Add(-m.cfg.Retention).UnixMilli()
	rows, err := m.db.Query(`SELECT id FROM tasks WHERE details_retained = 1 AND finished_at > 0 AND finished_at < ?`, cutoff)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := m.pruneDetails(id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) pruneToCapacity() error {
	for {
		var count int
		if err := m.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE details_retained = 1`).Scan(&count); err != nil {
			return err
		}
		if count < m.cfg.MaxRetained {
			return nil
		}
		var id string
		err := m.db.QueryRow(`SELECT id FROM tasks WHERE details_retained = 1 AND finished_at > 0 ORDER BY finished_at, created_at LIMIT 1`).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := m.pruneDetails(id); err != nil {
			return err
		}
	}
}

func (m *Manager) pruneDetails(id string) error {
	lock := m.logLock(id)
	lock.Lock()
	defer lock.Unlock()
	if err := os.Remove(m.logPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		_, _ = m.db.Exec(`UPDATE tasks SET cleanup_error = ? WHERE id = ?`, err.Error(), id)
		return err
	}
	_, err := m.db.Exec(`UPDATE tasks SET approval_source = '', approval_mode = '', approval_approver = '', log_bytes = 0, truncated = 1, error = '', log_error = '', details_retained = 0, cleanup_error = '' WHERE id = ?`, id)
	return err
}

func (m *Manager) reconcileLogs() error {
	rows, err := m.db.Query(`SELECT id, log_bytes FROM tasks WHERE details_retained = 1`)
	if err != nil {
		return err
	}
	type retainedLog struct {
		id    string
		bytes int64
	}
	var retained []retainedLog
	for rows.Next() {
		var log retainedLog
		if err := rows.Scan(&log.id, &log.bytes); err != nil {
			rows.Close()
			return err
		}
		retained = append(retained, log)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	known := make(map[string]bool, len(retained))
	for _, log := range retained {
		id, stored := log.id, log.bytes
		known[id] = true
		lock := m.logLock(id)
		lock.Lock()
		path := m.logPath(id)
		info, statErr := os.Lstat(path)
		actual, logErr := int64(0), ""
		truncated := false
		switch {
		case errors.Is(statErr, os.ErrNotExist):
			truncated = stored > 0
			if truncated {
				logErr = "log file missing during startup reconciliation"
			}
		case statErr != nil:
			lock.Unlock()
			return statErr
		case info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular():
			lock.Unlock()
			return fmt.Errorf("task log %s is not a regular file", path)
		default:
			if err := os.Chmod(path, 0600); err != nil {
				lock.Unlock()
				return err
			}
			actual = info.Size()
			oversize := actual > m.cfg.MaxLogBytes
			if oversize {
				if err := os.Truncate(path, m.cfg.MaxLogBytes); err != nil {
					lock.Unlock()
					return err
				}
				actual = m.cfg.MaxLogBytes
			}
			truncated = actual != stored || oversize
			if actual != stored {
				logErr = "log length reconciled during startup"
			}
		}
		if _, err := m.db.Exec(`UPDATE tasks SET log_bytes = ?, truncated = truncated OR ?, log_error = ? WHERE id = ?`, actual, truncated, logErr, id); err != nil {
			lock.Unlock()
			return err
		}
		lock.Unlock()
	}
	entries, err := os.ReadDir(filepath.Join(m.cfg.Dir, "logs"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".log")
		path := filepath.Join(m.cfg.Dir, "logs", name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("orphan task log %s is not a regular file", path)
		}
		if filepath.Ext(name) != ".log" || !taskIDPattern.MatchString(id) || !known[id] {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove orphan task log: %w", err)
			}
		}
	}
	return nil
}

func (m *Manager) cleanupLoop() {
	defer close(m.cleanupDone)
	interval := min(max(m.cfg.Retention/4, time.Minute), time.Hour)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			if !m.closed {
				_ = m.prune()
				_ = m.pruneToCapacity()
				_ = m.reconcileLogs()
			}
			m.mu.Unlock()
		case <-m.ctx.Done():
			return
		}
	}
}

func digestArgs(args map[string]any) (string, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("marshal args digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func supportsProgress(tool string) bool {
	switch tool {
	case "exec_command", "exec_cmd", "exec_ps":
		return true
	default:
		return false
	}
}
