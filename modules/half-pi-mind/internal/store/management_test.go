package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func managementAuditTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := New(filepath.Join(t.TempDir(), "management.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestManagementAuditMutationIsAtomicAndSecretFree(t *testing.T) {
	db := managementAuditTestStore(t)
	audit := ManagementAudit{
		RequestID: "req-success", Source: "offline_cli", Actor: "uid:test",
		Operation: "hand.add", TargetType: "hand", Status: "", CreatedAt: time.Now().UTC(),
	}
	credential, err := db.AddHandCredentialAudited("office", audit)
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM management_audits WHERE request_id = ? AND status = 'succeeded'`, audit.RequestID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("success audit count = %d", rows)
	}
	var message string
	if err := db.db.QueryRow(`SELECT message FROM management_audits WHERE request_id = ?`, audit.RequestID).Scan(&message); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(message, credential.Token) || strings.Contains(message, credential.ApplicationKey) {
		t.Fatalf("audit leaked secret: %q", message)
	}

	failed := ManagementAudit{
		RequestID: "req-failure", Source: "offline_cli", Actor: "uid:test",
		Operation: "hand.add", TargetType: "hand", CreatedAt: time.Now().UTC(),
	}
	if _, err := db.AddHandCredentialAudited("office", failed); err == nil {
		t.Fatal("duplicate credential accepted")
	}
	if err := db.AddManagementAudit(ManagementAudit{
		RequestID: failed.RequestID, Source: failed.Source, Actor: failed.Actor,
		Operation: failed.Operation, TargetType: failed.TargetType, Status: "failed",
		Code: "conflict", Message: "duplicate", CreatedAt: failed.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM hand_credentials WHERE label = 'office'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("credential count after duplicate = %d", rows)
	}
}

func TestManagementAuditInsertFailureRollsBackMutation(t *testing.T) {
	db := managementAuditTestStore(t)
	badAudit := ManagementAudit{RequestID: "", Source: "offline_cli", Actor: "uid:test", Operation: "hand.add", TargetType: "hand", CreatedAt: time.Now().UTC()}
	if _, err := db.AddHandCredentialAudited("office", badAudit); err == nil {
		t.Fatal("invalid audit accepted")
	}
	var count int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM hand_credentials`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("credential mutation was not rolled back: %d", count)
	}
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM management_audits`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unexpected audit rows after rollback: %d", count)
	}
}

func TestManagementAuditFaceScopesAndMigrationAreStable(t *testing.T) {
	db := managementAuditTestStore(t)
	audit := ManagementAudit{
		RequestID: "req-face", Source: "offline_cli", Actor: "uid:test", Operation: "face.add", TargetType: "face", CreatedAt: time.Now().UTC(),
	}
	credential, err := db.AddFaceTokenAudited("desktop", []protocol.FaceScope{protocol.FaceScopeRunsRead, protocol.FaceScopeChat}, audit)
	if err != nil {
		t.Fatal(err)
	}
	if len(credential.Scopes) != 2 || credential.Scopes[0] != protocol.FaceScopeChat {
		t.Fatalf("canonical scopes = %v", credential.Scopes)
	}
	if err := db.migrate(); err != nil {
		t.Fatal(err)
	}
}
