package taskmanager

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func testConfig(dir string) Config {
	return Config{Dir: dir, MaxRunning: 4, MaxRuntime: time.Hour, MaxLogBytes: 1 << 20, Retention: 7 * 24 * time.Hour, MaxRetained: 1000}
}

func backgroundRPC(id string) protocol.RPC {
	return protocol.RPC{
		RunID: id, Tool: "test", Args: map[string]any{"secret": "not-persisted"},
		Approval:   &protocol.Approval{Source: "mind", Mode: "normal", Approver: "user"},
		Background: &protocol.RPCBackgroundOptions{TaskID: id, MaxRuntimeMS: 1000},
	}
}

func waitTerminal(t *testing.T, manager *Manager, id string) Task {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		task, err := manager.Status(id)
		if err == nil && protocol.IsTerminalTaskStatus(task.Status) {
			return task
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s did not finish: %+v, %v", id, task, err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestManagerPersistsMetadataWithoutRawArgs(t *testing.T) {
	dir := t.TempDir()
	m, err := New(testConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	rpc := backgroundRPC("0123456789abcdef")
	_, _, err = m.Start(rpc, "approval-digest", func(context.Context, executor.ProgressFunc) *executor.ToolResult {
		return &executor.ToolResult{Success: true, Output: "result"}
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, m, rpc.RunID)
	data, err := os.ReadFile(filepath.Join(dir, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "not-persisted") {
		t.Fatal("raw RPC args were persisted")
	}
	wal, err := os.ReadFile(filepath.Join(dir, "tasks.db-wal"))
	if err == nil && strings.Contains(string(wal), "not-persisted") {
		t.Fatal("raw RPC args were persisted in WAL")
	}
	info, err := os.Stat(filepath.Join(dir, "tasks.db"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("database mode = %v, err = %v", info.Mode().Perm(), err)
	}
	for _, path := range []string{dir, filepath.Join(dir, "logs")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("directory %s mode = %v, err = %v", path, info.Mode().Perm(), err)
		}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if info, err := os.Stat(filepath.Join(dir, "tasks.db") + suffix); err == nil && info.Mode().Perm() != 0600 {
			t.Fatalf("SQLite sidecar %s mode = %v", suffix, info.Mode().Perm())
		}
	}
}

func TestManagerLogCapPagingAndPathSafety(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.MaxLogBytes = 5
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	rpc := backgroundRPC("1111111111111111")
	_, _, err = m.Start(rpc, "digest", func(_ context.Context, progress executor.ProgressFunc) *executor.ToolResult {
		progress(executor.Progress{Kind: "stdout", Data: "abc"})
		progress(executor.Progress{Kind: "stderr", Data: "def"})
		return &executor.ToolResult{Success: true, Output: "abcdef"}
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, m, rpc.RunID)
	data, next, eof, truncated, err := m.ReadLog(rpc.RunID, 1, 3)
	if err != nil || string(data) != "bcd" || next != 4 || eof || !truncated {
		t.Fatalf("page = %q next=%d eof=%v truncated=%v err=%v", data, next, eof, truncated, err)
	}
	data, next, eof, _, err = m.ReadLog(rpc.RunID, next, 3)
	if err != nil || string(data) != "e" || next != 5 || !eof {
		t.Fatalf("last page = %q next=%d eof=%v err=%v", data, next, eof, err)
	}
	if _, err := m.Status("../../escape"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unsafe task ID error = %v", err)
	}
	logInfo, err := os.Stat(filepath.Join(cfg.Dir, "logs", rpc.RunID+".log"))
	if err != nil || logInfo.Mode().Perm() != 0600 || logInfo.Size() != 5 {
		t.Fatalf("log info = %+v, err = %v", logInfo, err)
	}
}

func TestManagerDuplicateRaceExecutesOnceAndRejectsConflict(t *testing.T) {
	m, err := New(testConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	rpc := backgroundRPC("2222222222222222")
	var executions atomic.Int32
	execute := func(context.Context, executor.ProgressFunc) *executor.ToolResult {
		executions.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &executor.ToolResult{Success: true}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := m.Start(rpc, "same-digest", execute)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("exact duplicate failed: %v", err)
		}
	}
	waitTerminal(t, m, rpc.RunID)
	if executions.Load() != 1 {
		t.Fatalf("executions = %d", executions.Load())
	}
	conflict := rpc
	conflict.Args = map[string]any{"different": true}
	if _, _, err := m.Start(conflict, "other-digest", execute); !errors.Is(err, ErrConflictingDuplicate) {
		t.Fatalf("conflicting duplicate error = %v", err)
	}
}

func TestManagerTimeoutCancelRaceAndIdempotency(t *testing.T) {
	m, err := New(testConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	rpc := backgroundRPC("3333333333333333")
	rpc.Background.MaxRuntimeMS = 20
	_, _, err = m.Start(rpc, "digest", func(ctx context.Context, _ executor.ProgressFunc) *executor.ToolResult {
		<-ctx.Done()
		return &executor.ToolResult{Error: ctx.Err().Error()}
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Millisecond)
	status, err := m.Cancel(rpc.RunID)
	if err != nil || (status != protocol.TaskCancelCancelled && status != protocol.TaskCancelAlreadyDone) {
		t.Fatalf("cancel = %q, %v", status, err)
	}
	task := waitTerminal(t, m, rpc.RunID)
	if task.Status != protocol.TaskCancelled && task.Status != protocol.TaskTimedOut {
		t.Fatalf("terminal status = %q", task.Status)
	}
	status, err = m.Cancel(rpc.RunID)
	if err != nil || (status != protocol.TaskCancelCancelled && status != protocol.TaskCancelAlreadyDone) {
		t.Fatalf("idempotent cancel = %q, %v", status, err)
	}
}

func TestManagerReopenMarksNonterminalLostWithoutRerun(t *testing.T) {
	dir := t.TempDir()
	m, err := New(testConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	id := "4444444444444444"
	now := time.Now().UnixMilli()
	_, err = m.db.Exec(`INSERT INTO tasks (id, tool, approval_digest, args_digest, max_runtime_ms, status, created_at) VALUES (?, 'test', 'a', 'b', 1000, ?, ?)`, id, protocol.TaskRunning, now)
	if err != nil {
		t.Fatal(err)
	}
	m.db.Close()
	m.cancel()
	m.closed = true
	reopened, err := New(testConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	task, err := reopened.Status(id)
	if err != nil || task.Status != protocol.TaskLost || task.StartedAt == 0 || task.FinishedAt == 0 {
		t.Fatalf("recovered task = %+v, err = %v", task, err)
	}
}

func TestManagerRetentionAndQuota(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.MaxRetained = 1
	cfg.Retention = time.Hour
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	oldID := "5555555555555555"
	old := time.Now().Add(-2 * time.Hour).UnixMilli()
	oldRPC := backgroundRPC(oldID)
	oldArgsDigest, err := digestArgs(oldRPC.Args)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.db.Exec(`INSERT INTO tasks (id, tool, approval_digest, args_digest, max_runtime_ms, status, created_at, started_at, finished_at) VALUES (?, 'test', 'a', ?, 1000, ?, ?, ?, ?)`, oldID, oldArgsDigest, protocol.TaskSucceeded, old, old, old)
	if err != nil {
		t.Fatal(err)
	}
	rpc := backgroundRPC("6666666666666666")
	if _, _, err := m.Start(rpc, "digest", func(context.Context, executor.ProgressFunc) *executor.ToolResult {
		return &executor.ToolResult{Success: true}
	}); err != nil {
		t.Fatalf("retention did not free quota: %v", err)
	}
	waitTerminal(t, m, rpc.RunID)
	if task, err := m.Status(oldID); err != nil || task.LogBytes != 0 || !task.Truncated {
		t.Fatalf("expired task tombstone = %+v, %v", task, err)
	}
	var replayed atomic.Bool
	if _, started, err := m.Start(oldRPC, "a", func(context.Context, executor.ProgressFunc) *executor.ToolResult { replayed.Store(true); return nil }); err != nil || started {
		t.Fatalf("pruned duplicate start = %v, %v", started, err)
	}
	if replayed.Load() {
		t.Fatal("pruned task replay executed")
	}
	second := backgroundRPC("7777777777777777")
	block := make(chan struct{})
	if _, _, err := m.Start(second, "digest-2", func(context.Context, executor.ProgressFunc) *executor.ToolResult {
		<-block
		return &executor.ToolResult{Success: true}
	}); err != nil {
		t.Fatalf("oldest terminal task was not pruned: %v", err)
	}
	third := backgroundRPC("8888888888888888")
	if _, _, err := m.Start(third, "digest-3", func(context.Context, executor.ProgressFunc) *executor.ToolResult { return nil }); !errors.Is(err, ErrTaskQuota) {
		t.Fatalf("active-task quota error = %v", err)
	}
	close(block)
}

func TestManagerQueuedCancelNeverExecutes(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.MaxRunning = 1
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	block := make(chan struct{})
	first := backgroundRPC("9999999999999999")
	if _, _, err := m.Start(first, "first", func(context.Context, executor.ProgressFunc) *executor.ToolResult {
		<-block
		return &executor.ToolResult{Success: true}
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for task, _ := m.Status(first.RunID); task.Status != protocol.TaskRunning && time.Now().Before(deadline); task, _ = m.Status(first.RunID) {
		time.Sleep(time.Millisecond)
	}
	var executions atomic.Int32
	queued := backgroundRPC("aaaaaaaaaaaaaaaa")
	if _, _, err := m.Start(queued, "queued", func(context.Context, executor.ProgressFunc) *executor.ToolResult {
		executions.Add(1)
		return &executor.ToolResult{Success: true}
	}); err != nil {
		t.Fatal(err)
	}
	status, err := m.Cancel(queued.RunID)
	if err != nil || status != protocol.TaskCancelCancelled {
		t.Fatalf("cancel = %v, %v", status, err)
	}
	close(block)
	waitTerminal(t, m, first.RunID)
	task := waitTerminal(t, m, queued.RunID)
	if task.Status != protocol.TaskCancelled || executions.Load() != 0 {
		t.Fatalf("queued task = %+v executions=%d", task, executions.Load())
	}
}

func TestManagerCloseIsBoundedForIgnoringTool(t *testing.T) {
	m, err := New(testConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	rpc := backgroundRPC("bbbbbbbbbbbbbbbb")
	if _, _, err := m.Start(rpc, "digest", func(context.Context, executor.ProgressFunc) *executor.ToolResult {
		<-release
		return &executor.ToolResult{Success: true}
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for task, _ := m.Status(rpc.RunID); task.Status != protocol.TaskRunning && time.Now().Before(deadline); task, _ = m.Status(rpc.RunID) {
		time.Sleep(time.Millisecond)
	}
	started := time.Now()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > closeWait+time.Second {
		t.Fatalf("Close took %v", elapsed)
	}
	task, err := m.Status(rpc.RunID)
	if err != nil || task.Status != protocol.TaskLost {
		t.Fatalf("shutdown task = %+v, %v", task, err)
	}
	close(release)
}

func TestManagerRejectsSymlinkStorageAndLog(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, "tasks")); err != nil {
		t.Fatal(err)
	}
	if _, err := New(testConfig(filepath.Join(root, "tasks"))); err == nil {
		t.Fatal("symlink task directory accepted")
	}
	dir := filepath.Join(root, "real")
	m, err := New(testConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	id := "cccccccccccccccc"
	if err := os.Symlink(filepath.Join(target, "stolen"), filepath.Join(dir, "logs", id+".log")); err != nil {
		t.Fatal(err)
	}
	if _, err := New(testConfig(dir)); err == nil {
		t.Fatal("symlink log accepted")
	}
}

func TestManagerStartupReconcilesLogLengthAndOrphans(t *testing.T) {
	dir := t.TempDir()
	m, err := New(testConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	id := "dddddddddddddddd"
	now := time.Now().UnixMilli()
	_, err = m.db.Exec(`INSERT INTO tasks (id, tool, approval_digest, args_digest, max_runtime_ms, status, created_at, started_at, finished_at, log_bytes) VALUES (?, 'test', 'a', 'b', 1000, ?, ?, ?, ?, 99)`, id, protocol.TaskSucceeded, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logs", id+".log"), []byte("actual"), 0644); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "logs", "eeeeeeeeeeeeeeee.log")
	if err := os.WriteFile(orphan, []byte("orphan"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(testConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	task, err := reopened.Status(id)
	if err != nil || task.LogBytes != 6 || !task.Truncated {
		t.Fatalf("reconciled task = %+v, %v", task, err)
	}
	info, err := os.Stat(filepath.Join(dir, "logs", id+".log"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("reconciled log mode = %v, %v", info.Mode().Perm(), err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan log remains: %v", err)
	}
}
