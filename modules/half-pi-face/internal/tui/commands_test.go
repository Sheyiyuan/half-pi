package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestCommandRegistryParsesQuotesAndDrivesCompletion(t *testing.T) {
	model, _ := readyModel(t)
	parsed, err := model.commands.Parse(`/rename "Project Alpha"`)
	if err != nil || parsed.Spec.Name != "rename" || len(parsed.Args) != 1 || parsed.Args[0] != "Project Alpha" {
		t.Fatalf("parsed = %+v, %v", parsed, err)
	}
	completion := model.commands.Complete(model, "/tas")
	if len(completion) == 0 || completion[0].Insert != "/task " {
		t.Fatalf("completion = %#v", completion)
	}
	if _, err := model.commands.Parse(`/rename "unterminated`); err == nil {
		t.Fatal("unterminated command was accepted")
	}
}

func TestBracketedPasteNeverExecutesCommand(t *testing.T) {
	model, connection := readyModel(t)
	message := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/quit"), Paste: true}
	updated, _ := model.Update(message)
	if updated.(*Model).composer.Value() != "/quit" || len(connection.sent) != 0 {
		t.Fatalf("paste executed: value=%q sent=%d", updated.(*Model).composer.Value(), len(connection.sent))
	}
}

func TestEnterExecutesCompleteCommandWithCompletionVisible(t *testing.T) {
	model, connection := readyModel(t)
	conversationID := "conversation-1"
	conversation := newConversation(conversationID)
	conversation.Summary = validSummary(conversationID)
	model.conversations[conversationID] = conversation
	model.conversationOrder = []string{conversationID}
	model.idSource = sequenceIDSource(t, "subscribe-open", "snapshot-open")
	model.composer.SetValue("/open " + conversationID)
	model.completion = []Completion{{Label: conversationID, Insert: conversationID}}

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	runCommand(t, cmd)
	if len(connection.sent) != 1 || connection.sent[0].Type != protocol.TypeFaceSubscribe {
		t.Fatalf("Enter sent %#v", connection.sent)
	}
	if model.composer.Value() != "" {
		t.Fatalf("composer was not cleared: %q", model.composer.Value())
	}

	cmd = model.applyAccepted(protocol.FaceAccepted{
		RequestID: "subscribe-open", Operation: protocol.FaceOperationSubscribe,
	})
	runCommand(t, cmd)
	if len(connection.sent) != 2 || connection.sent[1].Type != protocol.TypeFaceConversationSnapshot {
		t.Fatalf("subscribe acceptance sent %#v", connection.sent)
	}
	snapshot, err := protocol.DecodePayload[protocol.FaceConversationSnapshot](&connection.sent[1])
	if err != nil || snapshot.ConversationID != conversationID {
		t.Fatalf("snapshot request = %+v, %v", snapshot, err)
	}
}
