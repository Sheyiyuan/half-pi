package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestIMEEmojiAndAltEnterRemainInComposer(t *testing.T) {
	model, _ := readyModel(t)
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("中文\U0001F642e\u0301")})
	model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("next")})
	if got := model.composer.Value(); got != "中文\U0001F642e\u0301\nnext" {
		t.Fatalf("composer value = %q", got)
	}
}

func TestChatByteLimitRejectsWithoutClearingDraft(t *testing.T) {
	model, connection := readyModel(t)
	model.limits.MaxChatContentBytes = 8
	model.composer.SetValue(strings.Repeat("界", 3))
	if _, err := model.submitComposer(); err == nil || !strings.Contains(err.Error(), "1 bytes") {
		t.Fatalf("byte limit error = %v", err)
	}
	if model.composer.Value() == "" || len(connection.sent) != 0 {
		t.Fatal("oversized draft was cleared or sent")
	}
}

func TestResizePreservesFocusDraftSelectionAndScroll(t *testing.T) {
	model, _ := readyModel(t)
	model.localDraft.Draft = "draft"
	model.composer.SetValue("draft")
	model.focus = focusChat
	model.selectedConversation = 2
	model.localDraft.ScrollOffset = 3
	model.resize(160, 45)
	model.resize(50, 16)
	if model.focus != focusChat || model.composer.Value() != "draft" || model.selectedConversation != 2 {
		t.Fatalf("resize lost state: focus=%v draft=%q selected=%d", model.focus, model.composer.Value(), model.selectedConversation)
	}
	if model.layout.Mode != layoutCompact || !model.layout.Short {
		t.Fatalf("compact short layout = %+v", model.layout)
	}
}

func TestInvalidTaskLogArgumentsAreRejectedBeforeSend(t *testing.T) {
	model, connection := readyModel(t)
	model.activeID = "conversation-1"
	model.localDraft = nil
	model.conversations[model.activeID] = newConversation(model.activeID)
	model.idSource = sequenceIDSource(t, "task-log")
	if _, err := model.requestTaskLog("task-1", -1, 0); err == nil {
		t.Fatal("invalid task log request was accepted")
	}
	if len(connection.sent) != 0 || len(model.pending) != 0 {
		t.Fatalf("invalid request reached connection: sent=%d pending=%d", len(connection.sent), len(model.pending))
	}
}

func TestPickerAndHistorySearchUseUnicodeFuzzyMatching(t *testing.T) {
	model, _ := readyModel(t)
	conversation := newConversation("conversation-1")
	conversation.Summary = validSummary("conversation-1")
	conversation.Summary.Name = "项目记录"
	conversation.History.replace([]string{"检查项目状态", "other"})
	model.conversations["conversation-1"] = conversation
	model.conversationOrder = []string{"conversation-1"}
	model.pickerQuery = "项目"
	if ids := model.filteredConversationIDs(); len(ids) != 1 || ids[0] != "conversation-1" {
		t.Fatalf("picker result = %#v", ids)
	}
	model.localDraft = conversation
	model.historyQuery = "检查状态"
	model.filterHistory()
	if len(model.completion) != 1 || model.completion[0].Insert != "检查项目状态" {
		t.Fatalf("history result = %#v", model.completion)
	}
}
