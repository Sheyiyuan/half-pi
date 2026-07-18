package tui

import (
	"encoding/json"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestCommandsProduceValidatedOfficialPayloads(t *testing.T) {
	term, conn, _ := newTestTerminal(t)
	term.active = "conversation-1"
	commands := []struct {
		input string
		typ   string
	}{
		{"/list", protocol.TypeFaceConversationList},
		{"/create Project Alpha", protocol.TypeFaceConversationCreate},
		{"/open conversation-2", protocol.TypeFaceConversationSnapshot},
		{"/snapshot", protocol.TypeFaceConversationSnapshot},
		{"/rename Renamed", protocol.TypeFaceConversationRename},
		{"hello Mind", protocol.TypeFaceChat},
		{"/cancel", protocol.TypeFaceChatCancel},
		{"/approve approval-1 allow_once reviewed", protocol.TypeFaceApprovalResolve},
		{"/hands", protocol.TypeFaceHandList},
		{"/hand hand-1", protocol.TypeFaceHandGet},
		{"/run run-1", protocol.TypeFaceRunGet},
		{"/run-cancel run-1", protocol.TypeFaceRunCancel},
		{"/tasks", protocol.TypeFaceTaskList},
		{"/task task-1", protocol.TypeFaceTaskGet},
		{"/task-log task-1 12 128", protocol.TypeFaceTaskLog},
		{"/task-cancel task-1", protocol.TypeFaceTaskCancel},
	}
	requestIDs := make(map[string]struct{}, len(commands))
	for _, test := range commands {
		if err := term.handleInput(test.input); err != nil {
			t.Fatalf("handleInput(%q): %v", test.input, err)
		}
		env := nextSent(t, conn)
		if env.Type != test.typ || env.SessionID != "" || env.Seq != 0 {
			t.Fatalf("handleInput(%q) sent %+v", test.input, env)
		}
		if err := protocol.ValidateFacePayload(env.Type, env.Payload); err != nil {
			t.Fatalf("handleInput(%q) payload: %v", test.input, err)
		}
		var request struct {
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(env.Payload, &request); err != nil {
			t.Fatal(err)
		}
		if request.RequestID == "" {
			t.Fatalf("handleInput(%q) omitted request_id", test.input)
		}
		if _, exists := requestIDs[request.RequestID]; exists {
			t.Fatalf("duplicate request_id %q", request.RequestID)
		}
		requestIDs[request.RequestID] = struct{}{}
	}
}

func TestCancelUsesLastChatFromActiveConversation(t *testing.T) {
	term, conn, _ := newTestTerminal(t)
	term.active = "conversation-a"
	if err := term.chat("hello"); err != nil {
		t.Fatal(err)
	}
	chat := nextSent(t, conn)
	request, err := protocol.DecodePayload[protocol.FaceChat](&chat)
	if err != nil {
		t.Fatal(err)
	}
	term.active = "conversation-b"
	if err := term.cancelChat(""); err == nil {
		t.Fatal("cancel used a Chat from a different conversation")
	}
	term.active = "conversation-a"
	if err := term.cancelChat(""); err != nil {
		t.Fatal(err)
	}
	cancel := nextSent(t, conn)
	payload, err := protocol.DecodePayload[protocol.FaceChatCancel](&cancel)
	if err != nil {
		t.Fatal(err)
	}
	if payload.TargetRequestID != request.RequestID || payload.ConversationID != "conversation-a" {
		t.Fatalf("cancel payload = %+v", payload)
	}
}

func TestCommandsRejectInvalidArguments(t *testing.T) {
	term, _, _ := newTestTerminal(t)
	term.active = "conversation-1"
	for _, input := range []string{
		"/create", "/open", "/rename", "/approve approval-1 invalid", "/task-log", "/task-log task-1 -1",
		"/task-log task-1 0 0", "/unknown",
	} {
		if err := term.handleInput(input); err == nil {
			t.Fatalf("handleInput(%q) succeeded", input)
		}
	}
}
