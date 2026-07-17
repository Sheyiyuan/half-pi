package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

func TestRemoteRunAuditLifecycle(t *testing.T) {
	store := newTestStore(t)
	registry := remoteexec.NewRegistry(store)
	metadata := remoteexec.AuditMetadata{
		ArgsDigest: "approval:abc123", ApprovalSource: "mind", ApprovalMode: "normal", ApprovalReason: "confirmed",
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
	if run.Status != protocol.RunSucceeded || run.ArgsDigest != metadata.ArgsDigest || run.ApprovalSource != "mind" {
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
