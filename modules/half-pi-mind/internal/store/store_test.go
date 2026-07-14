package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New(%q): %v", path, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNewCreatesTables(t *testing.T) {
	_ = newTestStore(t) // New already runs migrate and checks for errors
}

func TestGroupID(t *testing.T) {
	id1 := GroupID("/home/user/project")
	id2 := GroupID("/home/user/project")
	if id1 != id2 {
		t.Errorf("same work dir should yield same id: %q vs %q", id1, id2)
	}
	if len(id1) != 16 {
		t.Errorf("group id length = %d, want 16", len(id1))
	}
}

func TestUpsertGroup(t *testing.T) {
	s := newTestStore(t)

	g, err := s.UpsertGroup("/tmp/test-project")
	if err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}
	if g.WorkDir != "/tmp/test-project" {
		t.Errorf("work_dir = %q, want /tmp/test-project", g.WorkDir)
	}
	if g.Name != "" {
		t.Errorf("new group name should be empty, got %q", g.Name)
	}
	if g.ID != GroupID("/tmp/test-project") {
		t.Errorf("group id = %q", g.ID)
	}

	// GetGroup
	g2, err := s.GetGroup(g.ID)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if g2 == nil {
		t.Fatal("GetGroup returned nil")
	}

	// GetGroupByWorkDir
	g3, err := s.GetGroupByWorkDir("/tmp/test-project")
	if err != nil {
		t.Fatalf("GetGroupByWorkDir: %v", err)
	}
	if g3 == nil {
		t.Fatal("GetGroupByWorkDir returned nil")
	}
	if g3.ID != g.ID {
		t.Errorf("GetGroupByWorkDir id = %q, want %q", g3.ID, g.ID)
	}
}

func TestUpsertGroupIdempotent(t *testing.T) {
	s := newTestStore(t)

	g1, _ := s.UpsertGroup("/tmp/test-project")
	g2, _ := s.UpsertGroup("/tmp/test-project")

	if g1.ID != g2.ID {
		t.Errorf("upsert twice should yield same group: %q vs %q", g1.ID, g2.ID)
	}
}

func TestListGroups(t *testing.T) {
	s := newTestStore(t)

	s.UpsertGroup("/tmp/alpha")
	s.UpsertGroup("/tmp/beta")

	groups, err := s.ListGroups()
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
}

func TestGetGroupNotFound(t *testing.T) {
	s := newTestStore(t)

	g, err := s.GetGroup("nonexistent")
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if g != nil {
		t.Error("GetGroup should return nil for nonexistent group")
	}
}

func TestCreateSession(t *testing.T) {
	s := newTestStore(t)

	g, _ := s.UpsertGroup("/tmp/test-project")

	err := s.CreateSession(g.ID, "session-1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess, err := s.GetSession("session-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("GetSession returned nil")
	}
	if sess.GroupID != g.ID {
		t.Errorf("session group_id = %q, want %q", sess.GroupID, g.ID)
	}
	if sess.Name != "" {
		t.Errorf("new session name should be empty, got %q", sess.Name)
	}
	if sess.Description != "" {
		t.Errorf("new session description should be empty, got %q", sess.Description)
	}
}

func TestUpdateSessionName(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.UpsertGroup("/tmp/test-project")
	s.CreateSession(g.ID, "s-named")

	err := s.UpdateSessionName("s-named", "My Session")
	if err != nil {
		t.Fatalf("UpdateSessionName: %v", err)
	}

	sess, _ := s.GetSession("s-named")
	if sess.Name != "My Session" {
		t.Errorf("name = %q, want My Session", sess.Name)
	}
}

func TestGetMessageCount(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.UpsertGroup("/tmp/test-project")
	s.CreateSession(g.ID, "s-count")

	count, err := s.GetMessageCount("s-count")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 0 {
		t.Errorf("empty session count = %d, want 0", count)
	}

	s.SaveMessages("s-count", []Message{
		{Role: "user", Content: "a", Seq: 1},
		{Role: "assistant", Content: "b", Seq: 2},
	})

	count, err = s.GetMessageCount("s-count")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestReplaceMessages(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.UpsertGroup("/tmp/test-project")
	s.CreateSession(g.ID, "s-replace")

	// Initial save
	s.SaveMessages("s-replace", []Message{
		{Role: "user", Content: "old", Seq: 1},
	})

	// Replace
	err := s.ReplaceMessages("s-replace", []Message{
		{Role: "user", Content: "new-1", Seq: 1},
		{Role: "assistant", Content: "new-2", Seq: 2},
	})
	if err != nil {
		t.Fatalf("ReplaceMessages: %v", err)
	}

	msgs, _ := s.GetMessages("s-replace")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "new-1" || msgs[1].Content != "new-2" {
		t.Error("replace didn't update messages")
	}
}

func TestReplaceMessagesEmpty(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.UpsertGroup("/tmp/test-project")
	s.CreateSession(g.ID, "s-replace-empty")

	s.SaveMessages("s-replace-empty", []Message{
		{Role: "user", Content: "old", Seq: 1},
	})

	err := s.ReplaceMessages("s-replace-empty", nil)
	if err != nil {
		t.Fatalf("ReplaceMessages with nil: %v", err)
	}

	msgs, _ := s.GetMessages("s-replace-empty")
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after replace with nil, got %d", len(msgs))
	}
}

func TestCreateSessionDuplicateID(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.UpsertGroup("/tmp/test-project")

	s.CreateSession(g.ID, "dup-session")
	err := s.CreateSession(g.ID, "dup-session")
	if err == nil {
		t.Error("duplicate session id should error")
	}
}

func TestGetSessionNotFound(t *testing.T) {
	s := newTestStore(t)
	sess, err := s.GetSession("nonexistent")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess != nil {
		t.Error("GetSession should return nil for nonexistent session")
	}
}

func TestListSessions(t *testing.T) {
	s := newTestStore(t)

	g1, _ := s.UpsertGroup("/tmp/one")
	g2, _ := s.UpsertGroup("/tmp/two")
	s.CreateSession(g1.ID, "s1")
	s.CreateSession(g1.ID, "s2")
	s.CreateSession(g2.ID, "s3")

	list, err := s.ListSessions(g1.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("group 1 should have 2 sessions, got %d", len(list))
	}

	list2, _ := s.ListSessions(g2.ID)
	if len(list2) != 1 {
		t.Errorf("group 2 should have 1 session, got %d", len(list2))
	}
}

func TestSaveAndGetMessages(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.UpsertGroup("/tmp/test-project")
	s.CreateSession(g.ID, "session-1")

	msgs := []Message{
		{Role: "user", Content: "hello", Seq: 1},
		{Role: "assistant", Content: "hi there", Seq: 2},
		{Role: "tool", Content: "result", ToolID: "call_1", Seq: 3},
	}
	if err := s.SaveMessages("session-1", msgs); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	got, err := s.GetMessages("session-1")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	if got[0].Role != "user" || got[0].Content != "hello" {
		t.Errorf("msg[0] = %+v", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "hi there" {
		t.Errorf("msg[1] = %+v", got[1])
	}
	if got[2].Role != "tool" || got[2].ToolID != "call_1" {
		t.Errorf("msg[2] = %+v", got[2])
	}
}

func TestGetMessagesEmpty(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.UpsertGroup("/tmp/test-project")
	s.CreateSession(g.ID, "empty-session")

	got, err := s.GetMessages("empty-session")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 messages, got %d", len(got))
	}
}

func TestGetLastSeq(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.UpsertGroup("/tmp/test-project")
	s.CreateSession(g.ID, "session-1")

	// Empty session
	seq, err := s.GetLastSeq("session-1")
	if err != nil {
		t.Fatalf("GetLastSeq: %v", err)
	}
	if seq != 0 {
		t.Errorf("empty session seq = %d, want 0", seq)
	}

	// With messages
	s.SaveMessages("session-1", []Message{
		{Role: "user", Content: "a", Seq: 1},
		{Role: "user", Content: "b", Seq: 5},
	})
	seq, err = s.GetLastSeq("session-1")
	if err != nil {
		t.Fatalf("GetLastSeq: %v", err)
	}
	if seq != 5 {
		t.Errorf("last seq = %d, want 5", seq)
	}
}

func TestMessagesIsolatedBySession(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.UpsertGroup("/tmp/test-project")
	s.CreateSession(g.ID, "session-1")
	s.CreateSession(g.ID, "session-2")

	s.SaveMessages("session-1", []Message{
		{Role: "user", Content: "session 1 msg", Seq: 1},
	})
	s.SaveMessages("session-2", []Message{
		{Role: "user", Content: "session 2 msg", Seq: 1},
	})

	got1, _ := s.GetMessages("session-1")
	if len(got1) != 1 || got1[0].Content != "session 1 msg" {
		t.Error("session-1 messages leaked or missing")
	}

	got2, _ := s.GetMessages("session-2")
	if len(got2) != 1 || got2[0].Content != "session 2 msg" {
		t.Error("session-2 messages leaked or missing")
	}
}

func TestDateTimeParsing(t *testing.T) {
	s := newTestStore(t)

	g, err := s.UpsertGroup("/tmp/test-project")
	if err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}
	if g.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if g.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}

	s.CreateSession(g.ID, "ts-session")
	sess, _ := s.GetSession("ts-session")
	if sess.CreatedAt.IsZero() {
		t.Error("session CreatedAt should not be zero")
	}

	s.SaveMessages("ts-session", []Message{
		{Role: "user", Content: "test", Seq: 1},
	})
	msgs, _ := s.GetMessages("ts-session")
	if len(msgs) > 0 && msgs[0].CreatedAt.IsZero() {
		t.Error("message CreatedAt should not be zero")
	}
}

func TestFindSessionsByPrefix(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.UpsertGroup("/tmp/test-prefix")

	// UUID-style IDs with dashes
	s.CreateSession(g.ID, "550e8400-e29b-41d4-a716-446655440000")
	s.CreateSession(g.ID, "550e8400-f3a8-4b12-c829-557766330011")
	s.CreateSession(g.ID, "9e8f7a6b-3c4d-5e6f-7890-abcd12345678")

	// Exact match
	sessions, err := s.FindSessionsByPrefix(g.ID, "550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatalf("FindSessionsByPrefix: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("exact match: expected 1, got %d", len(sessions))
	}

	// Prefix match (no dashes)
	sessions, err = s.FindSessionsByPrefix(g.ID, "550e8400")
	if err != nil {
		t.Fatalf("FindSessionsByPrefix: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("prefix match: expected 2, got %d", len(sessions))
	}

	// Prefix match with dashes
	sessions, err = s.FindSessionsByPrefix(g.ID, "550e8400-e29b")
	if err != nil {
		t.Fatalf("FindSessionsByPrefix: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("dashed prefix: expected 1, got %d", len(sessions))
	}
	if sessions[0].Name != "" {
		t.Error("new session should have empty name")
	}

	// No match
	sessions, err = s.FindSessionsByPrefix(g.ID, "deadbeef")
	if err != nil {
		t.Fatalf("FindSessionsByPrefix: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("no match: expected 0, got %d", len(sessions))
	}

	// Single match
	sessions, err = s.FindSessionsByPrefix(g.ID, "9e8f7a6b")
	if err != nil {
		t.Fatalf("FindSessionsByPrefix: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("single match: expected 1, got %d", len(sessions))
	}
}
