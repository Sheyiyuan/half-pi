package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
)

func testApprovalRecord(id string, now time.Time) approval.AuditRecord {
	return approval.AuditRecord{
		Request: protocol.ApprovalRequest{
			ApprovalID: id, ConversationID: "conversation-1", RequestID: "request-1", RunID: "run-1",
			Tool: "exec_command", Reason: "requires approval", ArgsDigest: "sha256:redacted",
			ExpiresAt: now.Add(time.Minute),
		},
		Status: approval.StatusPending, CreatedAt: now,
	}
}

func TestApprovalAuditLifecycleStoresIdentityWithoutRawArgs(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	record := testApprovalRecord("approval-1", now)
	const rawSecret = "top-secret-command-argument"
	record.Request.ArgsDigest = approval.ArgsDigest(json.RawMessage(`{"command":"` + rawSecret + `"}`))
	if err := s.CreateApproval(record); err != nil {
		t.Fatal(err)
	}
	resolution := approval.Resolution{
		ApprovalID: record.Request.ApprovalID, ConversationID: record.Request.ConversationID,
		Decision: protocol.FaceApprovalAllowOnce,
		Actor:    approval.Actor{ID: "face-id-1", Label: "Operator", Source: "face"},
		Reason:   "approved remotely", ResolvedAt: now.Add(time.Second),
	}
	if err := s.FinishApproval(record.Request.ApprovalID, approval.StatusResolved, resolution); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.LookupApproval(record.Request.ApprovalID)
	if err != nil || !found {
		t.Fatalf("LookupApproval = %+v, %t, %v", got, found, err)
	}
	if got.Status != approval.StatusResolved || got.Decision != resolution.Decision || got.Actor != resolution.Actor ||
		got.ResolutionReason != resolution.Reason || !got.ResolvedAt.Equal(resolution.ResolvedAt) {
		t.Fatalf("resolved audit = %+v", got)
	}
	if err := s.FinishApproval(record.Request.ApprovalID, approval.StatusResolved, resolution); err == nil {
		t.Fatal("duplicate terminal approval update succeeded")
	}

	var persisted string
	if err := s.db.QueryRow(`SELECT approval_id || conversation_id || request_id || run_id || tool || reason ||
		args_digest || status || decision || actor_id || actor_label || source || resolution_reason
		FROM approval_audits WHERE approval_id = ?`, record.Request.ApprovalID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, rawSecret) {
		t.Fatal("approval audit contains raw tool arguments")
	}
}

func TestRecoverApprovalsCancelsPendingAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approval-recovery.db")
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	first, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.CreateApproval(testApprovalRecord("approval-recover", now)); err != nil {
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
	recoveredAt := now.Add(2 * time.Minute)
	if count, err := second.RecoverApprovals(recoveredAt); err != nil || count != 1 {
		t.Fatalf("RecoverApprovals = %d, %v", count, err)
	}
	if count, err := second.RecoverApprovals(recoveredAt.Add(time.Second)); err != nil || count != 0 {
		t.Fatalf("second RecoverApprovals = %d, %v", count, err)
	}
	record, found, err := second.LookupApproval("approval-recover")
	if err != nil || !found || record.Status != approval.StatusCancelled || !record.ResolvedAt.Equal(recoveredAt) {
		t.Fatalf("recovered approval = %+v, %t, %v", record, found, err)
	}
}

func TestFinishApprovalValidatesTerminalShape(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	if err := s.CreateApproval(testApprovalRecord("approval-shape", now)); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishApproval("approval-shape", approval.StatusResolved, approval.Resolution{ResolvedAt: now}); err == nil {
		t.Fatal("resolved approval without actor and decision succeeded")
	}
	invalidDecision := approval.Resolution{
		Decision: "always", Actor: approval.Actor{ID: "face-1", Source: "face"}, ResolvedAt: now,
	}
	if err := s.FinishApproval("approval-shape", approval.StatusResolved, invalidDecision); err == nil {
		t.Fatal("resolved approval with unknown decision succeeded")
	}
	invalidCancelled := approval.Resolution{
		Decision: protocol.FaceApprovalDenyOnce, Actor: approval.Actor{ID: "face-1", Source: "face"}, ResolvedAt: now,
	}
	if err := s.FinishApproval("approval-shape", approval.StatusCancelled, invalidCancelled); err == nil {
		t.Fatal("cancelled approval with actor and decision succeeded")
	}
}
