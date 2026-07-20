package store

import "testing"

func TestAppendMessagesPreservesHistoryAndRequestBinding(t *testing.T) {
	db := newTestStore(t)
	group, err := db.UpsertGroup(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(group.ID, "append-session"); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendMessages("append-session", 0, []Message{
		{Role: "user", Content: "first", RequestID: "request-1", Seq: 1},
		{Role: "assistant", Content: "answer", RequestID: "request-1", Seq: 2},
	}); err != nil {
		t.Fatal(err)
	}
	first, err := db.GetMessages("append-session")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AppendMessages("append-session", 2, []Message{
		{Role: "user", Content: "second", RequestID: "request-2", Seq: 3},
	}); err != nil {
		t.Fatal(err)
	}
	all, err := db.GetMessages("append-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[2].RequestID != "request-2" {
		t.Fatalf("messages = %#v", all)
	}
	for index := range first {
		if all[index].ID != first[index].ID || !all[index].CreatedAt.Equal(first[index].CreatedAt) || all[index].RequestID != "request-1" {
			t.Fatalf("history changed at %d: before %#v after %#v", index, first[index], all[index])
		}
	}
	if err := db.AppendMessages("append-session", 1, []Message{{Role: "assistant", Content: "conflict", Seq: 2}}); err == nil {
		t.Fatal("stale expected sequence was accepted")
	}
	afterConflict, _ := db.GetMessages("append-session")
	if len(afterConflict) != 3 {
		t.Fatalf("conflicting append changed history: %#v", afterConflict)
	}
}

func TestGetMessagesBeforePagesWithoutOverlap(t *testing.T) {
	db := newTestStore(t)
	group, _ := db.UpsertGroup(t.TempDir())
	if err := db.CreateSession(group.ID, "page-session"); err != nil {
		t.Fatal(err)
	}
	messages := make([]Message, 7)
	for index := range messages {
		messages[index] = Message{Role: "user", Content: "message", Seq: index + 1}
	}
	if err := db.AppendMessages("page-session", 0, messages); err != nil {
		t.Fatal(err)
	}
	first, more, err := db.GetMessagesBefore("page-session", 0, 3)
	if err != nil || !more || len(first) != 3 || first[0].Seq != 5 || first[2].Seq != 7 {
		t.Fatalf("first page = %#v more=%t err=%v", first, more, err)
	}
	second, more, err := db.GetMessagesBefore("page-session", first[0].Seq, 3)
	if err != nil || !more || len(second) != 3 || second[0].Seq != 2 || second[2].Seq != 4 {
		t.Fatalf("second page = %#v more=%t err=%v", second, more, err)
	}
	third, more, err := db.GetMessagesBefore("page-session", second[0].Seq, 3)
	if err != nil || more || len(third) != 1 || third[0].Seq != 1 {
		t.Fatalf("third page = %#v more=%t err=%v", third, more, err)
	}
}
