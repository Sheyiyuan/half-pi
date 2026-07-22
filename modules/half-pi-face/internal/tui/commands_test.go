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

func TestCompactCommandSendsTypedMutation(t *testing.T) {
	model, connection := readyModel(t)
	model.features[protocol.FaceFeatureContextCompaction] = struct{}{}
	model.activeID = "conversation-1"
	model.localDraft = nil
	model.conversations[model.activeID] = newConversation(model.activeID)
	model.idSource = sequenceIDSource(t, "compact-request")
	model.composer.SetValue("/compact keep 40")
	command, err := model.submitComposer()
	if err != nil {
		t.Fatal(err)
	}
	runCommand(t, command)
	if len(connection.sent) != 1 || connection.sent[0].Type != protocol.TypeFaceConversationCompact {
		t.Fatalf("Compact command sent %#v", connection.sent)
	}
	request, err := protocol.DecodePayload[protocol.FaceConversationCompact](&connection.sent[0])
	if err != nil || request.Target.Mode != protocol.FaceCompactTargetKeep || request.Target.KeepMessages == nil || *request.Target.KeepMessages != 40 {
		t.Fatalf("Compact request = %+v, %v", request, err)
	}
	if _, ok := model.compactRequests["compact-request"]; !ok {
		t.Fatal("Compact mutation was not retained for replay")
	}
}

func TestCapabilitiesFallsBackToLegacyOnce(t *testing.T) {
	model, connection := readyModel(t)
	model.capabilitiesKnown = false
	model.legacyCapabilities = false
	model.idSource = sequenceIDSource(t, "legacy-capabilities")
	model.pending["modern-capabilities"] = pendingRequest{Operation: protocol.FaceOperationCapabilitiesGet}
	command := model.applyError(protocol.FaceError{
		RequestID: "modern-capabilities", Code: protocol.FaceErrorInvalidRequest, Message: "invalid", Retryable: false,
	})
	runCommand(t, command)
	if len(connection.sent) != 1 || connection.sent[0].Type != protocol.TypeFaceCapabilitiesGet {
		t.Fatalf("fallback sent %#v", connection.sent)
	}
	request, err := protocol.DecodePayload[protocol.FaceCapabilitiesGet](&connection.sent[0])
	if err != nil || len(request.AcceptFeatures) != 0 {
		t.Fatalf("legacy capabilities request = %+v, %v", request, err)
	}
	model.applyError(protocol.FaceError{
		RequestID: "legacy-capabilities", Code: protocol.FaceErrorInvalidRequest, Message: "invalid", Retryable: false,
	})
	if len(connection.sent) != 1 || !model.capabilityFallback {
		t.Fatalf("legacy fallback looped: sent=%d attempted=%t", len(connection.sent), model.capabilityFallback)
	}
}
