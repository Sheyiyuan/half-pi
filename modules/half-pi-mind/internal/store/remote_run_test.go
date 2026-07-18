package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

func TestRemoteRunRequestIDMigrationPreservesLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-run.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE remote_runs (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL DEFAULT '',
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
	)`)
	if err == nil {
		_, err = db.Exec(`INSERT INTO remote_runs (id, session_id, hand_id, tool, status, created_at)
			VALUES ('legacy-run', 'legacy-session', 'legacy-hand', 'read_file', 'created', ?)`, time.Now().UnixMilli())
	}
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}

	store, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run, err := store.GetRemoteRun("legacy-run")
	if err != nil || run.RequestID != "" || run.SessionID != "legacy-session" {
		t.Fatalf("migrated legacy run = %+v, %v", run, err)
	}
}

func TestRemoteRunAuditLifecycle(t *testing.T) {
	store := newTestStore(t)
	registry := remoteexec.NewRegistry(store)
	metadata := remoteexec.AuditMetadata{
		RequestID: "face-request-1", ArgsDigest: "approval:abc123",
		ApprovalSource: "mind", ApprovalMode: "normal", ApprovalReason: "confirmed",
	}
	if err := registry.CreateWithMetadata("run-1", "session-1", "hand-1", "exec_command", metadata); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("run-1", protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("run-1", protocol.RunSent); err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{RunID: "run-1", StartedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyResult("hand-1", protocol.RPCResult{RunID: "run-1", Success: true}); err != nil {
		t.Fatal(err)
	}

	run, err := store.GetRemoteRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != protocol.RunSucceeded || run.RequestID != metadata.RequestID || run.ArgsDigest != metadata.ArgsDigest || run.ApprovalSource != "mind" {
		t.Fatalf("unexpected run: %+v", run)
	}
	if run.SentAt.IsZero() || run.AcceptedAt.IsZero() || run.FinishedAt.IsZero() {
		t.Fatalf("missing lifecycle timestamps: %+v", run)
	}
	events, err := store.ListRemoteRunEvents("run-1")
	if err != nil {
		t.Fatal(err)
	}
	wantStatuses := []protocol.RunStatus{
		protocol.RunCreated, protocol.RunApproved, protocol.RunSent,
		protocol.RunAccepted, protocol.RunRunning, protocol.RunSucceeded,
	}
	if len(events) != len(wantStatuses) {
		t.Fatalf("event count = %d, want %d", len(events), len(wantStatuses))
	}
	for i, event := range events {
		if event.Seq != i+1 || event.ToStatus != wantStatuses[i] {
			t.Fatalf("event %d = %+v, want status %s", i, event, wantStatuses[i])
		}
	}
	bySession, _ := store.ListRemoteRunsBySession("session-1")
	byHand, _ := store.ListRemoteRunsByHand("hand-1")
	if len(bySession) != 1 || len(byHand) != 1 {
		t.Fatalf("query counts: session=%d hand=%d", len(bySession), len(byHand))
	}
}

func TestRecoverRemoteRunsIsIdempotentAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.db")
	first, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := remoteexec.NewRegistry(first)
	if err := registry.Create("run-recover", "session-1", "hand-1", "exec_command"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("run-recover", protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("run-recover", protocol.RunSent); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	recovered, err := second.RecoverRemoteRuns()
	if err != nil || recovered != 1 {
		t.Fatalf("RecoverRemoteRuns = %d, %v", recovered, err)
	}
	if recoveredAgain, err := second.RecoverRemoteRuns(); err != nil || recoveredAgain != 0 {
		t.Fatalf("second recovery = %d, %v", recoveredAgain, err)
	}
	run, _ := second.GetRemoteRun("run-recover")
	if run.Status != protocol.RunLost || run.Error != "startup_recovery" {
		t.Fatalf("unexpected recovered run: %+v", run)
	}
	events, _ := second.ListRemoteRunEvents("run-recover")
	if events[len(events)-1].Type != "startup_recovery" {
		t.Fatalf("last event = %+v", events[len(events)-1])
	}
}

func TestAcceptedAfterCancelIsAudited(t *testing.T) {
	store := newTestStore(t)
	registry := remoteexec.NewRegistry(store)
	if err := registry.Create("run-cancel", "session-1", "hand-1", "exec_command"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("run-cancel", protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("run-cancel", protocol.RunSent); err != nil {
		t.Fatal(err)
	}
	if requested, err := registry.RequestCancel("run-cancel", "user"); err != nil || !requested {
		t.Fatalf("RequestCancel = %v, %v", requested, err)
	}
	if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{RunID: "run-cancel", StartedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	run, _ := store.GetRemoteRun("run-cancel")
	if run.AcceptedAt.IsZero() || run.Status != protocol.RunCancelRequested {
		t.Fatalf("accepted-after-cancel not persisted: %+v", run)
	}
	events, _ := store.ListRemoteRunEvents("run-cancel")
	if events[len(events)-1].Type != "accepted_after_cancel" {
		t.Fatalf("last event = %+v", events[len(events)-1])
	}
}

func TestRemoteRunAuditStoresNoRawArgs(t *testing.T) {
	store := newTestStore(t)
	secret := "super-secret-value"
	metadata := remoteexec.AuditMetadata{ArgsDigest: "sha256:only-digest"}
	if err := store.CreateRemoteRun(remoteexec.AuditRun{
		ID: "run-secret", HandID: "hand-1", Tool: "exec_command", Metadata: metadata,
		Status: protocol.RunCreated, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM remote_runs WHERE instr(CAST(id || session_id || hand_id || tool || args_digest || approval_reason AS TEXT), ?) > 0`, secret).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("raw secret appeared in remote run audit")
	}
}

func TestRemoteRunProgressAuditIsSeparateBoundedAndMigrationSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.db")
	first, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := remoteexec.NewRegistry(first)
	if err := registry.Create("progress-run", "session-1", "hand-1", "exec_command"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("progress-run", protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("progress-run", protocol.RunSent); err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{
		RunID: "progress-run", StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	admission, err := registry.ApplyProgressFrom("hand-1", "", protocol.RPCProgress{
		RunID: "progress-run", Seq: 2, Kind: protocol.ProgressStderr, Data: "bounded",
	})
	if err != nil || !admission.Accepted || !admission.Gap {
		t.Fatalf("admission = %+v, %v", admission, err)
	}
	run, err := first.GetRemoteRun("progress-run")
	if err != nil || run.Status != protocol.RunRunning {
		t.Fatalf("progress changed status: %+v, %v", run, err)
	}
	events, err := first.ListRemoteRunEvents("progress-run")
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Type != "progress" || last.ProgressSeq != 2 || last.Kind != protocol.ProgressStderr || !last.Gap || last.Message != "bounded" {
		t.Fatalf("progress event = %+v", last)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := New(path)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer second.Close()
	if err := second.AppendRemoteRunProgress(remoteexec.AuditProgress{
		RunID: "progress-run", Seq: 2, Kind: protocol.ProgressStderr, Data: "duplicate", At: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	events, _ = second.ListRemoteRunEvents("progress-run")
	if events[len(events)-1].Message != "bounded" {
		t.Fatalf("duplicate progress was persisted: %+v", events)
	}
	if err := second.AppendRemoteRunProgress(remoteexec.AuditProgress{
		RunID: "progress-run", Seq: 3, Kind: protocol.ProgressStdout,
		Data: strings.Repeat("x", protocol.MaxRPCProgressChunkBytes+1), At: time.Now(),
	}); err == nil {
		t.Fatal("oversized audit data accepted")
	}
}
