package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
)

func TestSecurityDecisionAndOutboxCommitTogether(t *testing.T) {
	db := newTestStore(t)
	meta := corelifecycle.NewMeta(corelifecycle.SourceMind).
		WithConversation("conversation-1").WithGroup("group-1").WithNode("mind")
	mutation := corelifecycle.AuditMutation{
		Meta: meta, ActionKind: "tool", ResourceName: "exec_command",
		TargetNode: "hand-1", InputDigest: corelifecycle.HashString("redacted"),
		RiskLabels: []string{"external_side_effect"}, Decision: "allow", ReasonCode: "policy_allow",
		Details: map[string]any{
			"provider_id": "review-provider", "model_id": "review-model", "profile": "security",
			"duration_ms": int64(42), "raw_args": "must-not-be-stored", "nested": map[string]any{"secret": true},
		},
	}
	if err := db.AppendSecurityDecision(context.Background(), mutation); err != nil {
		t.Fatal(err)
	}
	records, err := db.DueOutbox(context.Background(), time.Now().Add(time.Second), 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("outbox records = %d, err=%v", len(records), err)
	}
	var payload map[string]any
	if err := json.Unmarshal(records[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["resource_name"] != "exec_command" || payload["input_digest"] != mutation.InputDigest {
		t.Fatalf("outbox payload = %+v", payload)
	}
	if payload["target_node"] != "hand-1" {
		t.Fatalf("outbox target node = %+v", payload["target_node"])
	}
	labels, ok := payload["risk_labels"].([]any)
	if !ok || len(labels) != 1 || labels[0] != "external_side_effect" {
		t.Fatalf("outbox risk labels = %+v", payload["risk_labels"])
	}
	if _, leaked := payload["args"]; leaked {
		t.Fatal("outbox leaked raw args")
	}
	encodedPayload := string(records[0].Payload)
	if strings.Contains(encodedPayload, "must-not-be-stored") || strings.Contains(encodedPayload, "raw_args") || strings.Contains(encodedPayload, "nested") {
		t.Fatalf("outbox leaked arbitrary details: %s", encodedPayload)
	}
	details, ok := payload["details"].(map[string]any)
	if !ok || details["provider_id"] != "review-provider" || details["model_id"] != "review-model" ||
		details["profile"] != "security" || details["duration_ms"] != float64(42) {
		t.Fatalf("outbox reviewer details = %+v", payload["details"])
	}
	var storedDetails string
	if err := db.db.QueryRow(`SELECT details_redacted FROM security_decisions WHERE id = ?`, mutation.EventID).Scan(&storedDetails); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedDetails, "must-not-be-stored") || strings.Contains(storedDetails, "raw_args") ||
		!strings.Contains(storedDetails, "review-provider") || !strings.Contains(storedDetails, "review-model") {
		t.Fatalf("stored reviewer details = %s", storedDetails)
	}
	var storedTarget, storedLabels string
	if err := db.db.QueryRow(`SELECT target_node, risk_labels FROM security_decisions WHERE id = ?`, mutation.EventID).
		Scan(&storedTarget, &storedLabels); err != nil {
		t.Fatal(err)
	}
	if storedTarget != "hand-1" || storedLabels != `["external_side_effect"]` {
		t.Fatalf("stored target=%q labels=%s", storedTarget, storedLabels)
	}
	if err := db.FinishOutbox(context.Background(), records[0].ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	records, err = db.DueOutbox(context.Background(), time.Now().Add(time.Second), 10)
	if err != nil || len(records) != 0 {
		t.Fatalf("delivered outbox remained pending: %d, %v", len(records), err)
	}
}

func TestPruneLifecycleOutboxKeepsPendingRecords(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	base := corelifecycle.NewMeta(corelifecycle.SourceMind).WithConversation("conversation-1")
	for _, id := range []string{"delivered", "pending"} {
		mutation := corelifecycle.AuditMutation{
			Meta: base.EventMeta(time.Now().UnixNano()), ActionKind: "tool",
			ResourceName: id, InputDigest: corelifecycle.HashString(id), Decision: "allow",
		}
		mutation.EventID = id
		if err := db.AppendSecurityDecision(ctx, mutation); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.FinishOutbox(ctx, "delivered", time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	count, err := db.PruneLifecycleOutbox(ctx, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	if err != nil || count != 1 {
		t.Fatalf("pruned=%d err=%v", count, err)
	}
	records, err := db.DueOutbox(ctx, time.Now().Add(time.Second), 10)
	if err != nil || len(records) != 1 || records[0].ID != "pending" {
		t.Fatalf("pending outbox was pruned: %#v err=%v", records, err)
	}
}
