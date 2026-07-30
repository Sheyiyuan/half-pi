package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestApprovalModalDefaultsToDenyAndLocksWhileResolving(t *testing.T) {
	model, connection := readyModel(t)
	model.localDraft = nil
	model.activeID = "conversation-1"
	conversation := newConversation(model.activeID)
	conversation.Approvals["approval-1"] = protocol.ApprovalSummary{
		ApprovalID: "approval-1", ConversationID: model.activeID, Tool: "exec_command",
		Reason: "sensitive", ArgsDigest: "digest", ExpiresAt: time.Now().Add(time.Minute),
	}
	model.conversations[model.activeID] = conversation
	model.idSource = sequenceIDSource(t, "approval-request")
	model.chooseApprovalModal()
	if model.modal == nil || model.modal.Choice != 0 {
		t.Fatalf("approval modal = %+v", model.modal)
	}
	_, cmd := model.handleModalKey("enter")
	runCommand(t, cmd)
	if !model.modal.Resolving || len(connection.sent) != 1 {
		t.Fatalf("approval submit = modal %+v sent %#v", model.modal, connection.sent)
	}
	request, err := protocol.DecodePayload[protocol.FaceApprovalResolve](&connection.sent[0])
	if err != nil || request.Decision != protocol.FaceApprovalDenyOnce {
		t.Fatalf("approval request = %+v, %v", request, err)
	}
	model.handleModalKey("right")
	if model.modal.Choice != 0 {
		t.Fatal("resolving approval changed selection")
	}
}

func TestRunProgressAndTaskLogsStaySeparate(t *testing.T) {
	model, _ := readyModel(t)
	model.localDraft = nil
	model.activeID = "conversation-1"
	conversation := newConversation(model.activeID)
	model.conversations[model.activeID] = conversation
	model.applyRunProgress(protocol.FaceRunProgress{
		ConversationID: model.activeID, RunID: "run-1", Seq: 1, Kind: protocol.ProgressStdout, Data: "one",
	})
	model.applyRunProgress(protocol.FaceRunProgress{
		ConversationID: model.activeID, RunID: "run-1", Seq: 3, Kind: protocol.ProgressStderr, Data: "three", Gap: true,
	})
	model.installTaskLog(model.activeID, protocol.TaskLogResult{
		TaskID: "task-1", Data: []byte("durable"), NextOffset: 7, EOF: true, Truncated: true,
	})
	output := conversation.RunOutput["run-1"]
	log := conversation.TaskLogs["task-1"]
	if output == nil || !output.Gap || len(output.Chunks) != 2 {
		t.Fatalf("run output = %+v", output)
	}
	if log == nil || log.Data != "durable" || !log.EOF || !log.Truncated {
		t.Fatalf("task log = %+v", log)
	}
	if len(output.Chunks) > 0 && output.Chunks[0].Data == log.Data {
		t.Fatal("foreground progress and durable log were combined")
	}
}

func TestToolProgressDetectsSequenceGap(t *testing.T) {
	model, _ := readyModel(t)
	model.localDraft = nil
	model.activeID = "conversation-1"
	conversation := newConversation(model.activeID)
	chat := ensureChat(conversation, "chat-1")
	chat.Tools = append(chat.Tools, toolActivity{Tool: "exec_command"})
	model.conversations[model.activeID] = conversation
	model.applyToolProgress(protocol.FaceChatToolProgress{
		ConversationID: model.activeID, RequestID: "chat-1", Tool: "exec_command", Seq: 1, Kind: "stdout", Data: "one",
	})
	model.applyToolProgress(protocol.FaceChatToolProgress{
		ConversationID: model.activeID, RequestID: "chat-1", Tool: "exec_command", Seq: 3, Kind: "stdout", Data: "three",
	})
	tool := chat.Tools[0]
	if tool.ProgressSeq != 3 || !strings.Contains(tool.Progress, "[progress gap]") {
		t.Fatalf("tool progress = %+v", tool)
	}
}

func TestToolProgressLimitPreservesUTF8Tail(t *testing.T) {
	model, _ := readyModel(t)
	model.localDraft = nil
	model.activeID = "conversation-1"
	conversation := newConversation(model.activeID)
	chat := ensureChat(conversation, "chat-1")
	chat.Tools = append(chat.Tools, toolActivity{Tool: "exec_command"})
	model.conversations[model.activeID] = conversation
	model.applyToolProgress(protocol.FaceChatToolProgress{
		ConversationID: model.activeID, RequestID: "chat-1", Tool: "exec_command", Seq: 1,
		Kind: "stdout", Data: strings.Repeat("界", protocol.MaxFaceToolOutputBytes/3+1),
	})
	if !utf8.ValidString(chat.Tools[0].Progress) || len(chat.Tools[0].Progress) > protocol.MaxFaceToolOutputBytes {
		t.Fatalf("tool progress is not bounded UTF-8: bytes=%d valid=%t", len(chat.Tools[0].Progress), utf8.ValidString(chat.Tools[0].Progress))
	}
}

func TestMouseRoutesSendAndTargetedChatWheel(t *testing.T) {
	model, connection := readyModel(t)
	model.idSource = sequenceIDSource(t, "chat-request", "create-request")
	model.composer.SetValue("mouse send")
	_, cmd := model.handleMouse(tea.MouseMsg{
		X: model.layout.Send.X, Y: model.layout.Send.Y,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress,
	})
	runCommand(t, cmd)
	if len(connection.sent) != 1 || connection.sent[0].Type != protocol.TypeFaceConversationCreate {
		t.Fatalf("mouse send = %#v", connection.sent)
	}
	model.chatViewport.SetContent(strings.Repeat("line\n", 60))
	model.chatViewport.GotoBottom()
	before := model.chatViewport.YOffset
	model.handleMouse(tea.MouseMsg{
		X: model.layout.Chat.X + 2, Y: model.layout.Chat.Y + 2,
		Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress,
	})
	if model.chatViewport.YOffset >= before {
		t.Fatalf("chat wheel offset = %d, before %d", model.chatViewport.YOffset, before)
	}
}
