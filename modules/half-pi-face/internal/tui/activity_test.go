package tui

import (
	"strings"
	"testing"
	"time"

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
