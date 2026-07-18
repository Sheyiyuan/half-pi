package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func (t *terminal) handleInput(raw string) error {
	input := strings.TrimSpace(raw)
	if input == "" {
		return nil
	}
	if !strings.HasPrefix(input, "/") {
		return t.chat(input)
	}
	command, argument, _ := strings.Cut(input, " ")
	argument = strings.TrimSpace(argument)
	switch command {
	case "/quit", "/exit":
		return errQuit
	case "/help":
		t.renderHelp()
		return nil
	case "/list":
		return t.listConversations()
	case "/create":
		if argument == "" {
			return fmt.Errorf("usage: /create <name>")
		}
		return t.createConversation(argument)
	case "/open":
		if argument == "" {
			return fmt.Errorf("usage: /open <conversation_id>")
		}
		return t.openConversation(argument)
	case "/snapshot":
		return t.openConversation(t.active)
	case "/rename":
		if argument == "" {
			return fmt.Errorf("usage: /rename <name>")
		}
		return t.renameConversation(argument)
	case "/cancel":
		return t.cancelChat(argument)
	case "/approve":
		return t.approve(argument)
	case "/hands":
		return t.listHands()
	case "/hand":
		if argument == "" {
			return fmt.Errorf("usage: /hand <hand_id>")
		}
		return t.getHand(argument)
	case "/run":
		if argument == "" {
			return fmt.Errorf("usage: /run <run_id>")
		}
		return t.getRun(argument)
	case "/run-cancel":
		if argument == "" {
			return fmt.Errorf("usage: /run-cancel <run_id>")
		}
		return t.cancelRun(argument)
	case "/tasks":
		return t.listTasks()
	case "/task":
		if argument == "" {
			return fmt.Errorf("usage: /task <task_id>")
		}
		return t.getTask(argument)
	case "/task-log":
		return t.readTaskLog(argument)
	case "/task-cancel":
		if argument == "" {
			return fmt.Errorf("usage: /task-cancel <task_id>")
		}
		return t.cancelTask(argument)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func (t *terminal) listConversations() error {
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceConversationList,
		protocol.FaceConversationList{RequestID: requestID}, protocol.FaceOperationConversationList)
}

func (t *terminal) createConversation(name string) error {
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceConversationCreate,
		protocol.FaceConversationCreate{RequestID: requestID, Name: name}, protocol.FaceOperationConversationCreate)
}

func (t *terminal) openConversation(conversationID string) error {
	if conversationID == "" {
		return fmt.Errorf("open a conversation first")
	}
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceConversationSnapshot,
		protocol.FaceConversationSnapshot{RequestID: requestID, ConversationID: conversationID},
		protocol.FaceOperationConversationSnapshot)
}

func (t *terminal) subscribe(conversationID string) error {
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceSubscribe,
		protocol.FaceSubscribe{RequestID: requestID, ConversationIDs: []string{conversationID}},
		protocol.FaceOperationSubscribe)
}

func (t *terminal) renameConversation(name string) error {
	if t.active == "" {
		return fmt.Errorf("open a conversation first")
	}
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceConversationRename,
		protocol.FaceConversationRename{RequestID: requestID, ConversationID: t.active, Name: name},
		protocol.FaceOperationConversationRename)
}

func (t *terminal) chat(content string) error {
	if t.active == "" {
		return fmt.Errorf("open a conversation before sending Chat")
	}
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	if err := t.send(requestID, protocol.TypeFaceChat,
		protocol.FaceChat{RequestID: requestID, ConversationID: t.active, Content: content},
		protocol.FaceOperationChat); err != nil {
		return err
	}
	t.lastChat[t.active] = requestID
	return nil
}

func (t *terminal) cancelChat(target string) error {
	if t.active == "" {
		return fmt.Errorf("open a conversation first")
	}
	if target == "" {
		target = t.lastChat[t.active]
	}
	if target == "" {
		return fmt.Errorf("no Chat request is available to cancel")
	}
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceChatCancel, protocol.FaceChatCancel{
		RequestID: requestID, TargetRequestID: target, ConversationID: t.active, Reason: "user",
	}, protocol.FaceOperationChatCancel)
}

func (t *terminal) approve(argument string) error {
	parts := strings.SplitN(argument, " ", 3)
	if len(parts) < 2 {
		return fmt.Errorf("usage: /approve <approval_id> <decision> [reason]")
	}
	decision := protocol.FaceApprovalDecision(parts[1])
	reason := ""
	if len(parts) == 3 {
		reason = parts[2]
	}
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceApprovalResolve, protocol.FaceApprovalResolve{
		RequestID: requestID, ApprovalID: parts[0], Decision: decision, Reason: reason,
	}, protocol.FaceOperationApprovalResolve)
}

func (t *terminal) listHands() error {
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceHandList,
		protocol.FaceHandList{RequestID: requestID}, protocol.FaceOperationHandList)
}

func (t *terminal) getHand(handID string) error {
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceHandGet,
		protocol.FaceHandGet{RequestID: requestID, HandID: handID}, protocol.FaceOperationHandGet)
}

func (t *terminal) getRun(runID string) error {
	if t.active == "" {
		return fmt.Errorf("open a conversation first")
	}
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceRunGet,
		protocol.FaceRunGet{RequestID: requestID, ConversationID: t.active, RunID: runID},
		protocol.FaceOperationRunGet)
}

func (t *terminal) cancelRun(runID string) error {
	if t.active == "" {
		return fmt.Errorf("open a conversation first")
	}
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceRunCancel,
		protocol.FaceRunCancel{RequestID: requestID, ConversationID: t.active, RunID: runID, Reason: "user"},
		protocol.FaceOperationRunCancel)
}

func (t *terminal) listTasks() error {
	if t.active == "" {
		return fmt.Errorf("open a conversation first")
	}
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceTaskList,
		protocol.FaceTaskList{RequestID: requestID, ConversationID: t.active}, protocol.FaceOperationTaskList)
}

func (t *terminal) getTask(taskID string) error {
	if t.active == "" {
		return fmt.Errorf("open a conversation first")
	}
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceTaskGet,
		protocol.FaceTaskGet{RequestID: requestID, ConversationID: t.active, TaskID: taskID},
		protocol.FaceOperationTaskGet)
}

func (t *terminal) readTaskLog(argument string) error {
	if t.active == "" {
		return fmt.Errorf("open a conversation first")
	}
	parts := strings.Fields(argument)
	if len(parts) == 0 || len(parts) > 3 {
		return fmt.Errorf("usage: /task-log <task_id> [offset] [limit]")
	}
	offset, limit := int64(0), 4096
	var err error
	if len(parts) >= 2 {
		offset, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid task log offset")
		}
	}
	if len(parts) == 3 {
		limit, err = strconv.Atoi(parts[2])
		if err != nil {
			return fmt.Errorf("invalid task log limit")
		}
	}
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceTaskLog, protocol.FaceTaskLog{
		RequestID: requestID, ConversationID: t.active, TaskID: parts[0], Offset: offset, Limit: limit,
	}, protocol.FaceOperationTaskLog)
}

func (t *terminal) cancelTask(taskID string) error {
	if t.active == "" {
		return fmt.Errorf("open a conversation first")
	}
	requestID, err := t.nextRequestID()
	if err != nil {
		return err
	}
	return t.send(requestID, protocol.TypeFaceTaskCancel,
		protocol.FaceTaskCancel{RequestID: requestID, ConversationID: t.active, TaskID: taskID, Reason: "user"},
		protocol.FaceOperationTaskCancel)
}

func (t *terminal) renderHelp() {
	t.line("/list  /create <name>  /open <conversation_id>  /snapshot  /rename <name>")
	t.line("/hands  /hand <id>  /run <id>  /run-cancel <id>")
	t.line("/tasks  /task <id>  /task-log <id> [offset] [limit]  /task-cancel <id>")
	t.line("/approve <id> <allow_once|deny_once|allow_session|deny_session> [reason]")
	t.line("/cancel [chat_request_id]  /quit")
}
