package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCompactMigrationInitializesLegacySessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-compact.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE session_groups (
			id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', work_dir TEXT NOT NULL,
			soul_path TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY, group_id TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '', soul_path TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (group_id) REFERENCES session_groups(id)
		)`,
		`CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, role TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '', tool_id TEXT NOT NULL DEFAULT '',
			tool_calls TEXT NOT NULL DEFAULT '', seq INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`INSERT INTO session_groups (id, work_dir) VALUES ('group-1', '/legacy')`,
		`INSERT INTO sessions (id, group_id) VALUES ('with-history', 'group-1'), ('empty', 'group-1')`,
		`INSERT INTO messages (session_id, role, content, seq) VALUES
			('with-history', 'user', 'one', 1), ('with-history', 'assistant', 'two', 2)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	withHistory, err := store.GetSessionRuntime(ctx, "with-history")
	if err != nil {
		t.Fatal(err)
	}
	if withHistory.HistoryGeneration != 2 || withHistory.HistoryViewGeneration != 2 ||
		withHistory.CompactGeneration != 0 || withHistory.PendingCompact || withHistory.PendingCompactID != "" ||
		withHistory.PendingAttempt != 0 || withHistory.PendingNotBefore != 0 {
		t.Fatalf("migrated runtime = %+v", withHistory)
	}
	empty, err := store.GetSessionRuntime(ctx, "empty")
	if err != nil || empty.HistoryGeneration != 0 || empty.HistoryViewGeneration != 0 || empty.PendingCompact {
		t.Fatalf("empty runtime = %+v, err=%v", empty, err)
	}
	messages, err := store.GetMessages("with-history")
	if err != nil || len(messages) != 2 || messages[0].CompactProjection != "" || messages[1].CompactProjection != "" {
		t.Fatalf("migrated messages = %+v, err=%v", messages, err)
	}
}

func TestCompactProjectionIsAppendOnlyAndAtomic(t *testing.T) {
	store, sessionID := newCompactSession(t)
	validProjection := `{"facts":{"count":2},"output_bytes":4,"version":"compact-tool-v1"}`
	if err := store.AppendMessages(sessionID, 0, []Message{{
		Role: "user", Content: "not a tool", CompactProjection: validProjection, Seq: 1,
	}}); err == nil {
		t.Fatal("non-tool compact projection was accepted")
	}
	if count, _ := store.GetMessageCount(sessionID); count != 0 {
		t.Fatalf("invalid projection partially appended %d messages", count)
	}
	if err := store.AppendMessages(sessionID, 0, []Message{{
		Role: "tool", Content: "raw", CompactProjection: `{ "version":"compact-tool-v1"}`, Seq: 1,
	}}); err == nil {
		t.Fatal("non-canonical compact projection was accepted")
	}
	if err := store.AppendMessages(sessionID, 0, []Message{{
		Role: "tool", Content: "raw", CompactProjection: `{"value":"` + strings.Repeat("x", 4096) + `"}`, Seq: 1,
	}}); err == nil {
		t.Fatal("oversized compact projection was accepted")
	}
	if err := store.AppendMessages(sessionID, 0, []Message{{
		Role: "tool", Content: "raw", ToolID: "call-1", CompactProjection: validProjection, Seq: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	messages, err := store.GetMessages(sessionID)
	if err != nil || len(messages) != 1 || messages[0].CompactProjection != validProjection {
		t.Fatalf("stored messages = %+v, err=%v", messages, err)
	}
	runtime, err := store.GetSessionRuntime(context.Background(), sessionID)
	if err != nil || runtime.HistoryGeneration != 1 || runtime.HistoryViewGeneration != 1 || runtime.SnapshotVersion != 1 {
		t.Fatalf("runtime after append = %+v, err=%v", runtime, err)
	}
}

func TestSessionRuntimeGenerationsFollowContextMutations(t *testing.T) {
	store, sessionID := newCompactSession(t)
	ctx := context.Background()
	initial, err := store.GetSessionRuntime(ctx, sessionID)
	if err != nil || initial.HistoryGeneration != 0 || initial.HistoryViewGeneration != 0 || initial.SnapshotVersion != 0 {
		t.Fatalf("initial runtime = %+v, err=%v", initial, err)
	}
	if err := store.AppendMessages(sessionID, 0, []Message{
		{Role: "user", Content: "one", Seq: 1},
		{Role: "assistant", Content: "two", Seq: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionMode(sessionID, "strict"); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetSessionRuntime(ctx, sessionID)
	if err != nil || after.HistoryGeneration != 2 || after.HistoryViewGeneration != 3 ||
		after.CompactGeneration != 0 || after.SnapshotVersion != 3 {
		t.Fatalf("runtime after mutations = %+v, err=%v", after, err)
	}
	if err := store.SetSessionMode(sessionID, "strict"); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := store.GetSessionRuntime(ctx, sessionID)
	if unchanged.HistoryViewGeneration != after.HistoryViewGeneration || unchanged.SnapshotVersion != after.SnapshotVersion {
		t.Fatalf("idempotent mode update advanced runtime: before=%+v after=%+v", after, unchanged)
	}
}

func TestCompactPendingAttemptCASAndCooldown(t *testing.T) {
	store, sessionID := newCompactSession(t)
	ctx := context.Background()
	pendingID := newUUIDv7(t)
	created, err := store.EnsureCompactPending(ctx, sessionID, pendingID, compactEvent(t, "compact.requested", sessionID))
	if err != nil || !created.Created || !created.Runtime.PendingCompact || created.Runtime.PendingCompactID != pendingID {
		t.Fatalf("created pending = %+v, err=%v", created, err)
	}
	merged, err := store.EnsureCompactPending(ctx, sessionID, newUUIDv7(t), compactEvent(t, "compact.requested", sessionID))
	if err != nil || merged.Created || merged.Runtime.PendingCompactID != pendingID {
		t.Fatalf("merged pending = %+v, err=%v", merged, err)
	}
	admitted, err := store.AdmitCompactAttempt(ctx, sessionID, pendingID, 0)
	if err != nil || admitted.PendingAttempt != 1 || admitted.SnapshotVersion != 2 {
		t.Fatalf("admitted pending = %+v, err=%v", admitted, err)
	}
	if _, err := store.AdmitCompactAttempt(ctx, sessionID, pendingID, 0); err == nil {
		t.Fatal("stale attempt admission succeeded")
	}
	stale, err := store.FinishCompactFailure(ctx, CompactFailure{
		SessionID: sessionID, Automatic: true,
		ExpectedPending: PendingExpectation{ID: pendingID, Attempt: 0, Required: true},
		FailedEvent:     compactEvent(t, "compact.failed", sessionID),
	})
	if err != nil || stale.StateChanged || !stale.Runtime.PendingCompact || stale.Runtime.PendingAttempt != 1 {
		t.Fatalf("stale failure = %+v, err=%v", stale, err)
	}
	notBefore := time.Now().Add(time.Minute).UnixMilli()
	limited, err := store.FinishCompactFailure(ctx, CompactFailure{
		SessionID: sessionID, Automatic: true, RateLimited: true, RetryNotBefore: notBefore,
		ExpectedPending: PendingExpectation{ID: pendingID, Attempt: 1, Required: true},
		FailedEvent:     compactEvent(t, "compact.failed", sessionID),
	})
	if err != nil || !limited.StateChanged || limited.Runtime.PendingNotBefore != notBefore || limited.Runtime.PendingAttempt != 1 {
		t.Fatalf("rate limited failure = %+v, err=%v", limited, err)
	}
	cleared, err := store.ClearCompactPending(ctx, sessionID, pendingID, 1)
	if err != nil || cleared.PendingCompact || cleared.PendingCompactID != "" ||
		cleared.PendingAttempt != 0 || cleared.PendingNotBefore != 0 {
		t.Fatalf("cleared pending = %+v, err=%v", cleared, err)
	}
}

func TestCompactAttemptAndStartedOutboxCommitTogether(t *testing.T) {
	store, sessionID := newCompactSession(t)
	ctx := context.Background()
	pendingID := newUUIDv7(t)
	if _, err := store.EnsureCompactPending(ctx, sessionID, pendingID, compactEvent(t, "compact.requested", sessionID)); err != nil {
		t.Fatal(err)
	}
	duplicate := compactEvent(t, "compact.started", sessionID)
	if err := store.AppendLifecycleEvent(ctx, duplicate); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdmitCompactAttemptWithEvent(ctx, sessionID, pendingID, 0, duplicate); err == nil {
		t.Fatal("duplicate started event did not fail attempt transaction")
	}
	runtime, err := store.GetSessionRuntime(ctx, sessionID)
	if err != nil || runtime.PendingAttempt != 0 {
		t.Fatalf("attempt escaped rollback: %+v err=%v", runtime, err)
	}
	admitted, err := store.AdmitCompactAttemptWithEvent(ctx, sessionID, pendingID, 0,
		compactEvent(t, "compact.started", sessionID))
	if err != nil || admitted.PendingAttempt != 1 {
		t.Fatalf("admitted = %+v err=%v", admitted, err)
	}
}

func TestContextSummaryCommitReuseRebaseAndCoverage(t *testing.T) {
	store, sessionID := newCompactSessionWithMessages(t, 4)
	ctx := context.Background()
	initial, _ := store.GetSessionRuntime(ctx, sessionID)
	first := compactSummaryForRange(t, store, sessionID, 2, "full", "", "", "first summary")
	firstResult, err := store.CommitContextSummary(ctx, CompactCommit{
		Summary: first, ExpectedHistoryGeneration: initial.HistoryGeneration,
		ExpectedHistoryViewGeneration: initial.HistoryViewGeneration,
		CompletedEvent:                compactEvent(t, "compact.completed", sessionID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.Reused || firstResult.Runtime.ActiveSummaryID != first.ID ||
		firstResult.Runtime.CompactGeneration != 1 || firstResult.Runtime.HistoryViewGeneration != initial.HistoryViewGeneration+1 {
		t.Fatalf("first commit = %+v", firstResult)
	}

	concurrentCandidate := compactSummaryForRange(t, store, sessionID, 2, "full", "", "", "different concurrent response")
	reused, err := store.CommitContextSummary(ctx, CompactCommit{
		Summary: concurrentCandidate,
		// Deliberately stale: strict fast path must recognize the already-active contract.
		ExpectedHistoryGeneration: initial.HistoryGeneration, ExpectedActiveSummaryID: "",
		ExpectedHistoryViewGeneration: initial.HistoryViewGeneration,
		CompletedEvent:                compactEvent(t, "compact.completed", sessionID),
	})
	if err != nil || !reused.Reused || reused.Summary.ID != first.ID || reused.Summary.Summary != first.Summary ||
		reused.Runtime.CompactGeneration != 1 || reused.Runtime.HistoryViewGeneration != firstResult.Runtime.HistoryViewGeneration {
		t.Fatalf("fast-path reuse = %+v, err=%v", reused, err)
	}

	rebaseKey := ContextSummaryDigest("principal/request-2")
	rebase := compactSummaryForRange(t, store, sessionID, 2, "rebase", "", rebaseKey, "rebased summary")
	rebase.SupersedesSummaryID = first.ID
	rebased, err := store.CommitContextSummary(ctx, CompactCommit{
		Summary: rebase, ExpectedHistoryGeneration: reused.Runtime.HistoryGeneration,
		ExpectedActiveSummaryID: first.ID, ExpectedHistoryViewGeneration: reused.Runtime.HistoryViewGeneration,
		AllowSameRange: true, CompletedEvent: compactEvent(t, "compact.completed", sessionID),
	})
	if err != nil || rebased.Runtime.ActiveSummaryID != rebase.ID || rebased.Runtime.CompactGeneration != 2 {
		t.Fatalf("rebase commit = %+v, err=%v", rebased, err)
	}
	if _, err := store.CommitContextSummary(ctx, CompactCommit{
		Summary: first, ExpectedHistoryGeneration: rebased.Runtime.HistoryGeneration,
		ExpectedActiveSummaryID: rebase.ID, ExpectedHistoryViewGeneration: rebased.Runtime.HistoryViewGeneration,
		CompletedEvent: compactEvent(t, "compact.completed", sessionID),
	}); err == nil {
		t.Fatal("same-range inactive summary was reactivated without explicit allowance")
	}

	incremental := compactSummaryForRange(t, store, sessionID, 4, "incremental", rebase.ID, "", "incremental summary")
	incremental.SupersedesSummaryID = rebase.ID
	incremented, err := store.CommitContextSummary(ctx, CompactCommit{
		Summary: incremental, ExpectedHistoryGeneration: rebased.Runtime.HistoryGeneration,
		ExpectedActiveSummaryID: rebase.ID, ExpectedHistoryViewGeneration: rebased.Runtime.HistoryViewGeneration,
		CompletedEvent: compactEvent(t, "compact.completed", sessionID),
	})
	if err != nil || incremented.Runtime.ActiveSummaryID != incremental.ID || incremented.Runtime.CompactGeneration != 3 {
		t.Fatalf("incremental commit = %+v, err=%v", incremented, err)
	}
	snapshot, err := store.GetCompactSnapshot(ctx, sessionID)
	if err != nil || snapshot.SummaryNodeCount != 3 || snapshot.SummaryStorageBytes != int64(len(first.Summary)+len(rebase.Summary)+len(incremental.Summary)) {
		t.Fatalf("compact snapshot = %+v, err=%v", snapshot, err)
	}
}

func TestContextSummaryDegradedRepairReplacesCorruptActive(t *testing.T) {
	store, sessionID := newCompactSessionWithMessages(t, 4)
	ctx := context.Background()
	runtime, _ := store.GetSessionRuntime(ctx, sessionID)
	active := compactSummaryForRange(t, store, sessionID, 2, "full", "", "", "active summary")
	committed, err := store.CommitContextSummary(ctx, CompactCommit{
		Summary: active, ExpectedHistoryGeneration: runtime.HistoryGeneration,
		ExpectedHistoryViewGeneration: runtime.HistoryViewGeneration,
		CompletedEvent:                compactEvent(t, "compact.completed", sessionID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE context_summaries SET summary = 'tampered' WHERE id = ?`, active.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.GetCompactSnapshot(ctx, sessionID)
	if err != nil || !snapshot.ActiveIntegrityError || snapshot.ActiveReferenceUsable {
		t.Fatalf("corrupt snapshot = %+v, err=%v", snapshot, err)
	}
	repair := compactSummaryForRange(t, store, sessionID, 2, "rebase", "", ContextSummaryDigest("repair"), "repaired")
	repaired, err := store.CommitContextSummary(ctx, CompactCommit{
		Summary: repair, ExpectedHistoryGeneration: committed.Runtime.HistoryGeneration,
		ExpectedActiveSummaryID: active.ID, ExpectedHistoryViewGeneration: committed.Runtime.HistoryViewGeneration,
		AllowSameRange: true, AllowDegradedRepair: true,
		CompletedEvent: compactEvent(t, "compact.completed", sessionID),
	})
	if err != nil || repaired.Runtime.ActiveSummaryID != repair.ID {
		t.Fatalf("repair = %+v, err=%v", repaired, err)
	}
	snapshot, err = store.GetCompactSnapshot(ctx, sessionID)
	if err != nil || snapshot.ActiveIntegrityError || snapshot.ActiveSummary == nil || snapshot.ActiveSummary.ID != repair.ID {
		t.Fatalf("repaired snapshot = %+v, err=%v", snapshot, err)
	}
}

func TestContextSummaryAndOutboxRollbackTogether(t *testing.T) {
	store, sessionID := newCompactSessionWithMessages(t, 2)
	ctx := context.Background()
	duplicate := compactEvent(t, "compact.completed", sessionID)
	if err := store.AppendLifecycleEvent(ctx, duplicate); err != nil {
		t.Fatal(err)
	}
	runtime, _ := store.GetSessionRuntime(ctx, sessionID)
	summary := compactSummaryForRange(t, store, sessionID, 2, "full", "", "", "must roll back")
	if _, err := store.CommitContextSummary(ctx, CompactCommit{
		Summary: summary, ExpectedHistoryGeneration: runtime.HistoryGeneration,
		ExpectedHistoryViewGeneration: runtime.HistoryViewGeneration, CompletedEvent: duplicate,
	}); err == nil {
		t.Fatal("duplicate outbox event did not fail summary commit")
	}
	after, _ := store.GetSessionRuntime(ctx, sessionID)
	if after.ActiveSummaryID != "" || after.CompactGeneration != 0 || after.HistoryViewGeneration != runtime.HistoryViewGeneration {
		t.Fatalf("runtime changed despite rollback: before=%+v after=%+v", runtime, after)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM context_summaries WHERE session_id = ?`, sessionID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("summary count=%d err=%v", count, err)
	}

	pendingID := newUUIDv7(t)
	if _, err := store.EnsureCompactPending(ctx, sessionID, pendingID, compactEvent(t, "compact.requested", sessionID)); err != nil {
		t.Fatal(err)
	}
	failedDuplicate := compactEvent(t, "compact.failed", sessionID)
	if err := store.AppendLifecycleEvent(ctx, failedDuplicate); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishCompactFailure(ctx, CompactFailure{
		SessionID: sessionID, Automatic: true,
		ExpectedPending: PendingExpectation{ID: pendingID, Attempt: 0, Required: true},
		FailedEvent:     failedDuplicate,
	}); err == nil {
		t.Fatal("duplicate outbox event did not fail pending cleanup")
	}
	pending, _ := store.GetSessionRuntime(ctx, sessionID)
	if !pending.PendingCompact || pending.PendingCompactID != pendingID {
		t.Fatalf("pending cleanup escaped rollback: %+v", pending)
	}
}

func TestCompactRowsCascadeWithSession(t *testing.T) {
	store, sessionID := newCompactSessionWithMessages(t, 2)
	runtime, _ := store.GetSessionRuntime(context.Background(), sessionID)
	summary := compactSummaryForRange(t, store, sessionID, 2, "full", "", "", "cascade")
	if _, err := store.CommitContextSummary(context.Background(), CompactCommit{
		Summary: summary, ExpectedHistoryGeneration: runtime.HistoryGeneration,
		ExpectedHistoryViewGeneration: runtime.HistoryViewGeneration,
		CompletedEvent:                compactEvent(t, "compact.completed", sessionID),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM sessions WHERE id = ?`, sessionID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"messages", "context_summaries", "session_runtime"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE session_id = ?`, sessionID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows after delete = %d, err=%v", table, count, err)
		}
	}
}

func newCompactSession(t *testing.T) (*Store, string) {
	t.Helper()
	store := newTestStore(t)
	group, err := store.UpsertGroup(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "compact-session"
	if err := store.CreateSession(group.ID, sessionID); err != nil {
		t.Fatal(err)
	}
	return store, sessionID
}

func newCompactSessionWithMessages(t *testing.T, count int) (*Store, string) {
	t.Helper()
	store, sessionID := newCompactSession(t)
	messages := make([]Message, count)
	for index := range messages {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		messages[index] = Message{Role: role, Content: "message-" + string(rune('a'+index)), Seq: index + 1}
	}
	if err := store.AppendMessages(sessionID, 0, messages); err != nil {
		t.Fatal(err)
	}
	return store, sessionID
}

func compactSummaryForRange(t *testing.T, store *Store, sessionID string, toSeq int, mode, parentID, generationKey, body string) ContextSummary {
	t.Helper()
	messages, err := store.GetMessages(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if toSeq > len(messages) {
		t.Fatalf("summary range %d exceeds %d messages", toSeq, len(messages))
	}
	sourceDigest, err := ContextSourceDigest(sessionID, 1, toSeq, messages[:toSeq])
	if err != nil {
		t.Fatal(err)
	}
	projectionVersion := "compact-source-v1"
	policyVersion := "compact-v1"
	profile := "default"
	return ContextSummary{
		ID: newUUIDv7(t), SessionID: sessionID, ParentSummaryID: parentID,
		FromSeq: 1, ToSeq: toSeq, Summary: body, SummaryDigest: ContextSummaryDigest(body),
		SourceDigest: sourceDigest,
		ContractDigest: ContextContractDigest(sourceDigest, projectionVersion, policyVersion,
			profile, mode, parentID, generationKey),
		ProviderID: "compact-provider", ModelID: "compact-model", Profile: profile,
		PolicyVersion: policyVersion, ProjectionVersion: projectionVersion,
		GenerationMode: mode, GenerationKey: generationKey,
		SourceEstimatedTokens: int64(toSeq * 5), SummaryEstimatedTokens: 2,
		InputTokens: 10, OutputTokens: 2,
	}
}

func compactEvent(t *testing.T, eventType, sessionID string) LifecycleEvent {
	t.Helper()
	return LifecycleEvent{
		ID: newUUIDv7(t), EventType: eventType, SchemaVersion: 1,
		TraceID: newUUIDv7(t), SpanID: newUUIDv7(t), SubjectID: sessionID,
		Payload: json.RawMessage(`{}`), OccurredAt: time.Now().UTC(),
	}
}

func newUUIDv7(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}
