package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

func TestRemoteTaskCRUDSessionListAndRecovery(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().Truncate(time.Millisecond)
	task := remoteexec.Task{
		TaskID: "0123456789abcdef", SessionID: "session-a", HandID: "hand-a", Tool: "exec_command",
		ArgsDigest: "digest", Status: protocol.TaskPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateRemoteTask(task); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRemoteTask(task.TaskID)
	if err != nil || got.SessionID != task.SessionID || got.ArgsDigest != "digest" || got.Stale {
		t.Fatalf("task = %+v, err=%v", got, err)
	}
	listed, err := s.ListRemoteTasksBySession("session-a")
	if err != nil || len(listed) != 1 || listed[0].TaskID != task.TaskID {
		t.Fatalf("listed = %+v, err=%v", listed, err)
	}
	recovered, err := s.RecoverRemoteTasks()
	if err != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	if recovered, err := s.RecoverRemoteTasks(); err != nil || recovered != 0 {
		t.Fatalf("second recovery=%d err=%v", recovered, err)
	}
	got, _ = s.GetRemoteTask(task.TaskID)
	if got.Status != protocol.TaskPending || !got.Stale || !got.FinishedAt.IsZero() {
		t.Fatalf("recovered task = %+v", got)
	}
	got.Status, got.Stale, got.LogBytes = protocol.TaskFailed, false, 42
	got.Error, got.UpdatedAt, got.FinishedAt = "later failure", time.Now(), time.Now()
	if err := s.UpdateRemoteTask(got); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetRemoteTask(task.TaskID)
	if got.Status != protocol.TaskFailed || got.Stale || got.Error != "later failure" || got.LogBytes != 42 {
		t.Fatalf("updated task = %+v", got)
	}
	if err := s.DeleteRemoteTask(task.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRemoteTask(task.TaskID); err == nil {
		t.Fatal("deleted task remains queryable")
	}
}

func TestRemoteTaskSchemaContainsNoRawArgsOrHandPath(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "schema.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.db.Query(`PRAGMA table_info(remote_tasks)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "args" || name == "raw_args" || name == "hand_path" || name == "work_dir" {
			t.Fatalf("forbidden remote_tasks column %q", name)
		}
	}
}

func TestCreateRemoteRunTaskRollsBackRunWhenTaskInsertFails(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "atomic.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	task := remoteexec.Task{
		TaskID: "task-1", SessionID: "session-a", HandID: "hand-a", Tool: "tool",
		ArgsDigest: "digest", Status: protocol.TaskPending, CreatedAt: now, UpdatedAt: now, Stale: true,
	}
	if err := s.CreateRemoteTask(task); err != nil {
		t.Fatal(err)
	}
	run := remoteexec.AuditRun{
		ID: task.TaskID, SessionID: task.SessionID, HandID: task.HandID, Tool: task.Tool,
		Metadata: remoteexec.AuditMetadata{ArgsDigest: task.ArgsDigest}, Status: protocol.RunCreated, CreatedAt: now,
	}
	if err := s.CreateRemoteRunTask(run, task); err == nil {
		t.Fatal("duplicate task insert unexpectedly succeeded")
	}
	if _, err := s.GetRemoteRun(run.ID); err == nil {
		t.Fatal("run remained after atomic task creation rolled back")
	}
}
