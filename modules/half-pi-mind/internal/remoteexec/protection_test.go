package remoteexec

import (
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestProtectionIndexTracksSemanticProtectedSet(t *testing.T) {
	index := NewProtectionIndex()
	run := Run{
		ID: "run-1", SessionID: "session-1", Status: protocol.RunCreated,
		CreatedAt: time.UnixMilli(100), Metadata: AuditMetadata{RequestID: "request-1"},
	}
	index.observeRun(run)
	created := index.Snapshot("session-1")
	if created.Revision != 1 || len(created.Records) != 1 || created.Records[0].RequestID != "request-1" {
		t.Fatalf("created snapshot = %+v", created)
	}
	run.Status = protocol.RunRunning
	index.observeRun(run)
	running := index.Snapshot("session-1")
	if running.Revision != created.Revision || running.Digest != created.Digest {
		t.Fatalf("non-terminal status changed protected set: %+v", running)
	}
	run.Status = protocol.RunSucceeded
	index.observeRun(run)
	terminal := index.Snapshot("session-1")
	if terminal.Revision != created.Revision+1 || len(terminal.Records) != 0 || terminal.Digest == created.Digest {
		t.Fatalf("terminal snapshot = %+v", terminal)
	}
}

func TestProtectionIndexPreservesTaskBindingAndTracksStale(t *testing.T) {
	index := NewProtectionIndex()
	run := Run{
		ID: "task-1", SessionID: "session-1", Status: protocol.RunCreated, DurableTask: true,
		CreatedAt: time.UnixMilli(100), Metadata: AuditMetadata{RequestID: "request-1"},
	}
	index.observeRun(run)
	index.observeTask(Task{
		TaskID: "task-1", SessionID: "session-1", Status: protocol.TaskRunning,
		CreatedAt: time.UnixMilli(100), Stale: true,
	})
	snapshot := index.Snapshot("session-1")
	if len(snapshot.Records) != 2 {
		t.Fatalf("snapshot records = %+v", snapshot.Records)
	}
	for _, record := range snapshot.Records {
		if record.RequestID != "request-1" {
			t.Fatalf("record lost request binding: %+v", record)
		}
	}
	index.observeTask(Task{
		TaskID: "task-1", SessionID: "session-1", Status: protocol.TaskSucceeded,
		CreatedAt: time.UnixMilli(100), Stale: false,
	})
	for _, record := range index.Snapshot("session-1").Records {
		if record.Kind == "task" {
			t.Fatalf("terminal task remains protected: %+v", record)
		}
	}
}
