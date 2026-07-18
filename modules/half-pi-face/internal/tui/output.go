package tui

import (
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func (t *terminal) handleEnvelope(env protocol.Envelope) error {
	if !protocol.IsFaceServerMessageType(env.Type) {
		return fmt.Errorf("Mind sent invalid Face message type %q", safeText(env.Type))
	}
	if err := protocol.ValidateFacePayload(env.Type, env.Payload); err != nil {
		return fmt.Errorf("Mind sent invalid Face payload: %s", safeText(err.Error()))
	}
	switch env.Type {
	case protocol.TypeFaceAccepted:
		accepted, _ := protocol.DecodePayload[protocol.FaceAccepted](&env)
		pending, ok := t.pending[accepted.RequestID]
		if !ok || pending.operation != accepted.Operation || pending.conversationID != accepted.ConversationID {
			return fmt.Errorf("Mind sent an uncorrelated accepted response")
		}
		if accepted.Operation == protocol.FaceOperationSubscribe {
			delete(t.pending, accepted.RequestID)
		}
		t.line("accepted %s (%s)", accepted.Operation, safeText(accepted.RequestID))
	case protocol.TypeFaceError:
		faceError, _ := protocol.DecodePayload[protocol.FaceError](&env)
		pending, ok := t.pending[faceError.RequestID]
		if !ok || pending.conversationID != faceError.ConversationID {
			return fmt.Errorf("Mind sent an uncorrelated error response")
		}
		delete(t.pending, faceError.RequestID)
		t.line("request %s: %s (%s)", safeText(faceError.RequestID), safeText(faceError.Message), faceError.Code)
	case protocol.TypeFaceResult:
		result, _ := protocol.DecodePayload[protocol.FaceResult](&env)
		pending, ok := t.pending[result.RequestID]
		if !ok || pending.conversationID != result.ConversationID {
			return fmt.Errorf("Mind sent an uncorrelated result response")
		}
		delete(t.pending, result.RequestID)
		if result.Status == protocol.FaceResultSucceeded && len(result.Data) > 0 {
			if err := protocol.ValidateFaceResultData(pending.operation, result.Data); err != nil {
				return fmt.Errorf("Mind sent invalid result data: %s", safeText(err.Error()))
			}
		}
		return t.renderResult(pending.operation, result)
	case protocol.TypeFaceSnapshot:
		snapshot, _ := protocol.DecodePayload[protocol.FaceSnapshot](&env)
		pending, ok := t.pending[snapshot.RequestID]
		if !ok || pending.operation != protocol.FaceOperationConversationSnapshot ||
			pending.conversationID != snapshot.Snapshot.ConversationID {
			return fmt.Errorf("Mind sent an uncorrelated snapshot response")
		}
		delete(t.pending, snapshot.RequestID)
		t.active = snapshot.Snapshot.ConversationID
		for _, chat := range snapshot.Snapshot.PendingChats {
			t.lastChat[t.active] = chat.RequestID
		}
		t.renderSnapshot(snapshot.Snapshot)
		return t.subscribe(t.active)
	case protocol.TypeFaceEvent:
		event, _ := protocol.DecodePayload[protocol.FaceEvent](&env)
		if event.ConversationID != "" && event.ConversationID != t.active {
			return nil
		}
		return t.renderEvent(event)
	}
	return nil
}

func (t *terminal) renderResult(operation protocol.FaceOperation, result protocol.FaceResult) error {
	if result.Status != protocol.FaceResultSucceeded {
		t.line("request %s %s: %s (%s)", safeText(result.RequestID), result.Status, safeText(result.Error), result.ErrorCode)
		return nil
	}
	switch operation {
	case protocol.FaceOperationChat:
		t.line("assistant: %s", safeText(result.Content))
	case protocol.FaceOperationConversationList:
		data, err := decodeData[protocol.ConversationListResult](result.Data)
		if err != nil {
			return err
		}
		t.line("conversations:")
		if len(data.Conversations) == 0 {
			t.line("  none")
		}
		for _, conversation := range data.Conversations {
			t.line("  %s  %s  messages=%d", safeText(conversation.ConversationID), safeText(conversation.Name), conversation.MessageCount)
		}
	case protocol.FaceOperationConversationCreate:
		data, err := decodeData[protocol.ConversationCreateResult](result.Data)
		if err != nil {
			return err
		}
		t.line("created %s (%s)", safeText(data.Conversation.Name), safeText(data.Conversation.ConversationID))
		return t.openConversation(data.Conversation.ConversationID)
	case protocol.FaceOperationConversationRename:
		data, err := decodeData[protocol.ConversationRenameResult](result.Data)
		if err != nil {
			return err
		}
		t.line("renamed conversation to %s", safeText(data.Conversation.Name))
	case protocol.FaceOperationHandList:
		data, err := decodeData[protocol.HandListResult](result.Data)
		if err != nil {
			return err
		}
		if len(data.Hands) == 0 {
			t.line("hands: none")
		}
		for _, hand := range data.Hands {
			t.renderHand(hand)
		}
	case protocol.FaceOperationHandGet:
		data, err := decodeData[protocol.HandGetResult](result.Data)
		if err != nil {
			return err
		}
		t.renderHand(data.Hand)
	case protocol.FaceOperationRunGet:
		data, err := decodeData[protocol.RunGetResult](result.Data)
		if err != nil {
			return err
		}
		t.renderRun(data.Run)
	case protocol.FaceOperationTaskList:
		data, err := decodeData[protocol.TaskListResult](result.Data)
		if err != nil {
			return err
		}
		if len(data.Tasks) == 0 {
			t.line("tasks: none")
		}
		for _, task := range data.Tasks {
			t.renderTask(task)
		}
	case protocol.FaceOperationTaskGet:
		data, err := decodeData[protocol.TaskGetResult](result.Data)
		if err != nil {
			return err
		}
		t.renderTask(data.Task)
	case protocol.FaceOperationTaskLog:
		data, err := decodeData[protocol.TaskLogResult](result.Data)
		if err != nil {
			return err
		}
		t.line("task %s log [%d,%d): %s", safeText(data.TaskID), data.Offset, data.NextOffset, safeText(string(data.Data)))
	case protocol.FaceOperationTaskCancel:
		data, err := decodeData[protocol.FaceTaskCancelResult](result.Data)
		if err != nil {
			return err
		}
		t.line("task cancel outcome: %s", safeText(data.Outcome))
		t.renderTask(data.Task)
	default:
		t.line("request %s succeeded: %s", safeText(result.RequestID), safeText(result.Content))
	}
	return nil
}

func (t *terminal) renderSnapshot(snapshot protocol.ConversationSnapshot) {
	t.line("conversation %s  %s  mode=%s  hand=%s", safeText(snapshot.ConversationID), safeText(snapshot.Name),
		safeText(snapshot.Mode), safeText(snapshot.ActiveHand))
	for _, message := range snapshot.Messages {
		t.line("%s: %s", safeText(message.Role), safeText(message.Content))
	}
	for _, chat := range snapshot.PendingChats {
		t.line("chat %s  pending", safeText(chat.RequestID))
	}
	for _, approval := range snapshot.PendingApprovals {
		t.line("approval %s  tool=%s  reason=%s", safeText(approval.ApprovalID), safeText(approval.Tool), safeText(approval.Reason))
	}
	for _, run := range snapshot.ActiveRuns {
		t.renderRun(run)
	}
	for _, task := range snapshot.Tasks {
		t.renderTask(task)
	}
	if snapshot.TaskHistoryTruncated {
		t.line("task history truncated at %d terminal tasks", snapshot.TaskHistoryLimit)
	}
}

func (t *terminal) renderEvent(event protocol.FaceEvent) error {
	switch event.Type {
	case protocol.FaceEventChatStarted:
		data, err := decodeData[protocol.ChatStartedEventData](event.Data)
		if err != nil {
			return err
		}
		t.line("chat %s started", safeText(data.RequestID))
	case protocol.FaceEventChatToolCalled:
		data, err := decodeData[protocol.ChatToolCalledEventData](event.Data)
		if err != nil {
			return err
		}
		t.line("chat %s called %s  args=%s", safeText(data.RequestID), safeText(data.Tool), safeText(data.ArgsDigest))
	case protocol.FaceEventChatToolCompleted:
		data, err := decodeData[protocol.ChatToolCompletedEventData](event.Data)
		if err != nil {
			return err
		}
		t.line("chat %s completed %s  success=%t", safeText(data.RequestID), safeText(data.Tool), data.Success)
	case protocol.FaceEventChatCompleted:
		data, err := decodeData[protocol.ChatCompletedEventData](event.Data)
		if err != nil {
			return err
		}
		t.line("chat %s completed", safeText(data.RequestID))
	case protocol.FaceEventChatFailed:
		data, err := decodeData[protocol.ChatFailedEventData](event.Data)
		if err != nil {
			return err
		}
		t.line("chat %s failed  code=%s", safeText(data.RequestID), data.Code)
	case protocol.FaceEventChatCancelled:
		data, err := decodeData[protocol.ChatCancelledEventData](event.Data)
		if err != nil {
			return err
		}
		t.line("chat %s cancelled  reason=%s", safeText(data.RequestID), safeText(data.Reason))
	case protocol.FaceEventApprovalRequested:
		data, err := decodeData[protocol.ApprovalRequestedEventData](event.Data)
		if err != nil {
			return err
		}
		t.line("approval %s  tool=%s  reason=%s", safeText(data.ApprovalID), safeText(data.Tool), safeText(data.Reason))
	case protocol.FaceEventApprovalResolved:
		data, err := decodeData[protocol.ApprovalResolvedEventData](event.Data)
		if err != nil {
			return err
		}
		t.line("approval %s resolved %s by %s", safeText(data.ApprovalID), data.Decision, safeText(data.Actor))
	case protocol.FaceEventRemoteRunChanged:
		data, err := decodeData[protocol.RemoteRunChangedEventData](event.Data)
		if err != nil {
			return err
		}
		t.line("run %s  %s  tool=%s", safeText(data.RunID), data.Status, safeText(data.Tool))
	case protocol.FaceEventHandConnected:
		data, err := decodeData[protocol.HandConnectedEventData](event.Data)
		if err != nil {
			return err
		}
		t.line("hand %s connected  %s/%s  %s", safeText(data.HandID), safeText(data.OS), safeText(data.Arch), safeText(data.Hostname))
	case protocol.FaceEventHandDisconnected:
		data, err := decodeData[protocol.HandDisconnectedEventData](event.Data)
		if err != nil {
			return err
		}
		t.line("hand %s disconnected", safeText(data.HandID))
	case protocol.FaceEventConversationChanged:
		data, err := decodeData[protocol.ConversationChangedEventData](event.Data)
		if err != nil {
			return err
		}
		t.line("conversation %s changed  version=%d", safeText(data.ConversationID), data.SnapshotVersion)
	case protocol.FaceEventTaskChanged:
		data, err := decodeData[protocol.TaskChangedEventData](event.Data)
		if err != nil {
			return err
		}
		t.renderTask(data)
	default:
		t.line("event %s: %s", event.Type, safeText(event.Message))
	}
	return nil
}

func (t *terminal) renderHand(hand protocol.HandSummary) {
	t.line("hand %s  connected=%t  %s/%s  %s", safeText(hand.HandID), hand.Connected, safeText(hand.OS), safeText(hand.Arch), safeText(hand.Hostname))
	for _, tool := range hand.Tools {
		t.line("  tool %s  %s", safeText(tool.Name), safeText(tool.Description))
	}
}

func (t *terminal) renderRun(run protocol.RemoteRunSummary) {
	t.line("run %s  %s  hand=%s  tool=%s  duration=%dms", safeText(run.RunID), run.Status,
		safeText(run.HandID), safeText(run.Tool), run.DurationMs)
}

func (t *terminal) renderTask(task protocol.TaskSummary) {
	t.line("task %s  %s  hand=%s  tool=%s  stale=%t", safeText(task.TaskID), task.Status, safeText(task.HandID), safeText(task.Tool), task.Stale)
	if task.Error != "" {
		t.line("  error: %s (%s)", safeText(task.Error), task.ErrorCode)
	}
}
