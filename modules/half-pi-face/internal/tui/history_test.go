package tui

import "testing"

func TestInputHistoryRestoresDraftAndDoesNotMutateEntries(t *testing.T) {
	history := newInputHistory()
	history.replace([]string{"first", "second"})
	if value, ok := history.previous("draft"); !ok || value != "second" {
		t.Fatalf("first previous = %q, %t", value, ok)
	}
	if value, ok := history.previous("second"); !ok || value != "first" {
		t.Fatalf("second previous = %q, %t", value, ok)
	}
	if value, ok := history.next("first"); !ok || value != "second" {
		t.Fatalf("first next = %q, %t", value, ok)
	}
	if value, ok := history.next("second"); !ok || value != "draft" {
		t.Fatalf("draft restore = %q, %t", value, ok)
	}
	if history.entries[0] != "first" || history.entries[1] != "second" {
		t.Fatalf("history mutated = %#v", history.entries)
	}
}

func TestConversationDraftsRemainIsolated(t *testing.T) {
	model, _ := readyModel(t)
	model.localDraft.Draft = "local"
	model.composer.SetValue("local")
	first := newConversation("first")
	first.Summary = validSummary("first")
	first.Draft = "persisted"
	model.conversations["first"] = first
	model.conversationOrder = []string{"first"}
	cmd, err := model.openConversation("first")
	if err != nil || cmd == nil || model.composer.Value() != "persisted" || model.localDraft != nil {
		t.Fatalf("open state = value %q local=%v err=%v", model.composer.Value(), model.localDraft, err)
	}
	model.composer.SetValue("changed")
	model.newDraftConversation()
	if first.Draft != "changed" || model.composer.Value() != "" {
		t.Fatalf("draft isolation = first %q active %q", first.Draft, model.composer.Value())
	}
}
