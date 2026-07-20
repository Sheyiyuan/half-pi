package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func (m *Model) applyEnvelope(env protocol.Envelope) tea.Cmd {
	if !protocol.IsFaceServerMessageType(env.Type) {
		return m.disconnect(fmt.Errorf("Mind sent an invalid Face message type"))
	}
	if err := protocol.ValidateFacePayload(env.Type, env.Payload); err != nil {
		return m.disconnect(fmt.Errorf("Mind sent an invalid Face payload"))
	}
	switch env.Type {
	case protocol.TypeFaceAccepted:
		value, _ := protocol.DecodePayload[protocol.FaceAccepted](&env)
		return m.applyAccepted(value)
	case protocol.TypeFaceResult:
		value, _ := protocol.DecodePayload[protocol.FaceResult](&env)
		return m.applyResult(value)
	case protocol.TypeFaceError:
		value, _ := protocol.DecodePayload[protocol.FaceError](&env)
		return m.applyError(value)
	case protocol.TypeFaceSnapshot:
		value, _ := protocol.DecodePayload[protocol.FaceSnapshot](&env)
		return m.applySnapshotResponse(value)
	case protocol.TypeFaceEvent:
		value, _ := protocol.DecodePayload[protocol.FaceEvent](&env)
		return m.applyEvent(value)
	case protocol.TypeFaceChatDelta:
		value, _ := protocol.DecodePayload[protocol.FaceChatDelta](&env)
		return m.applyDelta(value)
	case protocol.TypeFaceChatStreamEnd:
		value, _ := protocol.DecodePayload[protocol.FaceChatStreamEnd](&env)
		return m.applyStreamEnd(value)
	case protocol.TypeFaceRunProgress:
		value, _ := protocol.DecodePayload[protocol.FaceRunProgress](&env)
		m.applyRunProgress(value)
	}
	return nil
}

func (m *Model) correlated(requestID string, operation protocol.FaceOperation, conversationID string) (pendingRequest, bool) {
	pending, ok := m.pending[requestID]
	if !ok || pending.Operation != operation {
		return pendingRequest{}, false
	}
	if operation == protocol.FaceOperationSubscribe {
		return pending, conversationID == ""
	}
	if pending.ConversationID != conversationID {
		return pendingRequest{}, false
	}
	return pending, true
}

func (m *Model) applyAccepted(value protocol.FaceAccepted) tea.Cmd {
	pending, ok := m.correlated(value.RequestID, value.Operation, value.ConversationID)
	if !ok {
		return m.disconnect(fmt.Errorf("Mind sent an uncorrelated accepted response"))
	}
	if value.Operation == protocol.FaceOperationChat {
		if conversation := m.conversations[value.ConversationID]; conversation != nil {
			if chat := conversation.Chats[value.RequestID]; chat != nil {
				chat.Accepted = true
				chat.Sending = false
			}
		}
		return nil
	}
	if value.Operation != protocol.FaceOperationSubscribe {
		return nil
	}
	delete(m.pending, value.RequestID)
	cmd, err := m.requestSnapshot(pending.ConversationID, pending.TargetID)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	return cmd
}

func (m *Model) applyResult(value protocol.FaceResult) tea.Cmd {
	pending, ok := m.pending[value.RequestID]
	if !ok || pending.ConversationID != value.ConversationID {
		return m.disconnect(fmt.Errorf("Mind sent an uncorrelated result response"))
	}
	delete(m.pending, value.RequestID)
	if value.Status == protocol.FaceResultSucceeded && len(value.Data) > 0 {
		if err := protocol.ValidateFaceResultData(pending.Operation, value.Data); err != nil {
			return m.disconnect(fmt.Errorf("Mind sent invalid result data"))
		}
	}
	if value.Status != protocol.FaceResultSucceeded {
		return m.applyFailedResult(pending, value)
	}
	switch pending.Operation {
	case protocol.FaceOperationCapabilitiesGet:
		data, err := decodeResult[protocol.FaceCapabilitiesResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		m.installCapabilities(data)
		m.syncCapabilities = true
		return m.finishBaseSync()
	case protocol.FaceOperationConversationList:
		data, err := decodeResult[protocol.ConversationListResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		m.installConversationList(data.Conversations)
		m.syncConversations = true
		return m.finishBaseSync()
	case protocol.FaceOperationConversationCreate:
		data, err := decodeResult[protocol.ConversationCreateResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		return m.installCreatedConversation(data.Conversation, pending.TargetID)
	case protocol.FaceOperationConversationRename:
		data, err := decodeResult[protocol.ConversationRenameResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		m.upsertConversationSummary(data.Conversation)
		m.status = "Conversation renamed"
	case protocol.FaceOperationConversationMessages:
		data, err := decodeResult[protocol.ConversationMessagesResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		oldLineCount, oldOffset := m.chatViewport.TotalLineCount(), m.chatViewport.YOffset
		m.installMessagePage(pending.ConversationID, data)
		if pending.ConversationID == m.activeID {
			m.refreshViewport(false)
			m.chatViewport.SetYOffset(oldOffset + max(0, m.chatViewport.TotalLineCount()-oldLineCount))
			m.markScrollState()
		}
		return nil
	case protocol.FaceOperationChat:
		return m.finishChat(pending.ConversationID, value)
	case protocol.FaceOperationChatCancel:
		m.status = "Chat cancellation requested"
	case protocol.FaceOperationChatStreamGet:
		data, err := decodeResult[protocol.ChatStreamGetResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		m.installStream(pending.ConversationID, data)
	case protocol.FaceOperationApprovalResolve:
		m.status = "Approval decision submitted"
	case protocol.FaceOperationHandList:
		data, err := decodeResult[protocol.HandListResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		m.hands = make(map[string]protocol.HandSummary, len(data.Hands))
		for _, hand := range data.Hands {
			hand.Hostname = sanitizeRemoteText(hand.Hostname)
			m.hands[hand.HandID] = hand
		}
	case protocol.FaceOperationHandGet:
		data, err := decodeResult[protocol.HandGetResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		data.Hand.Hostname = sanitizeRemoteText(data.Hand.Hostname)
		m.hands[data.Hand.HandID] = data.Hand
	case protocol.FaceOperationRunGet:
		data, err := decodeResult[protocol.RunGetResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		if conversation := m.conversations[pending.ConversationID]; conversation != nil {
			conversation.Runs[data.Run.RunID] = data.Run
		}
	case protocol.FaceOperationRunCancel:
		m.status = "Run cancellation requested"
	case protocol.FaceOperationTaskList:
		data, err := decodeResult[protocol.TaskListResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		if conversation := m.conversations[pending.ConversationID]; conversation != nil {
			for _, task := range data.Tasks {
				conversation.Tasks[task.TaskID] = sanitizeTask(task)
			}
		}
	case protocol.FaceOperationTaskGet:
		data, err := decodeResult[protocol.TaskGetResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		if conversation := m.conversations[pending.ConversationID]; conversation != nil {
			conversation.Tasks[data.Task.TaskID] = sanitizeTask(data.Task)
		}
	case protocol.FaceOperationTaskLog:
		data, err := decodeResult[protocol.TaskLogResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		m.installTaskLog(pending.ConversationID, data)
	case protocol.FaceOperationTaskCancel:
		data, err := decodeResult[protocol.FaceTaskCancelResult](value.Data)
		if err != nil {
			return m.disconnect(err)
		}
		if conversation := m.conversations[pending.ConversationID]; conversation != nil {
			conversation.Tasks[data.Task.TaskID] = sanitizeTask(data.Task)
		}
		m.status = "Task cancellation reconciled"
	}
	m.refreshViewport(false)
	return nil
}

func (m *Model) applyFailedResult(pending pendingRequest, value protocol.FaceResult) tea.Cmd {
	message := sanitizeRemoteText(value.Error)
	if message == "" {
		message = string(value.Status)
	}
	m.status = message
	if pending.Operation == protocol.FaceOperationChat {
		if conversation := m.conversations[pending.ConversationID]; conversation != nil {
			if chat := conversation.Chats[value.RequestID]; chat != nil {
				chat.Terminal, chat.Sending, chat.Status, chat.Error = true, false, value.Status, message
			}
		}
		delete(m.outgoingChats, value.RequestID)
	}
	if pending.Operation == protocol.FaceOperationChatStreamGet {
		m.stopRecovering(pending.ConversationID, pending.TargetID)
	}
	if pending.Operation == protocol.FaceOperationConversationMessages {
		if conversation := m.conversations[pending.ConversationID]; conversation != nil {
			conversation.Paging = false
		}
	}
	if pending.Operation == protocol.FaceOperationTaskLog {
		if conversation := m.conversations[pending.ConversationID]; conversation != nil && conversation.TaskLogs[pending.TargetID] != nil {
			conversation.TaskLogs[pending.TargetID].Loading = false
		}
	}
	if pending.Operation == protocol.FaceOperationConversationCreate && pending.TargetID == "draft" {
		m.flow = nil
	}
	if pending.Operation == protocol.FaceOperationCapabilitiesGet {
		m.legacyCapabilities = true
		m.syncCapabilities = true
		return m.finishBaseSync()
	}
	if pending.Operation == protocol.FaceOperationConversationList {
		m.syncConversations = true
		return m.finishBaseSync()
	}
	if (pending.Operation == protocol.FaceOperationSubscribe || pending.Operation == protocol.FaceOperationConversationSnapshot) &&
		pending.TargetID == "sync" {
		m.state = stateReady
	}
	return nil
}

func (m *Model) applyError(value protocol.FaceError) tea.Cmd {
	pending, ok := m.pending[value.RequestID]
	if !ok || pending.ConversationID != value.ConversationID {
		m.status = sanitizeRemoteText(value.Message)
		return nil
	}
	delete(m.pending, value.RequestID)
	if pending.Operation == protocol.FaceOperationCapabilitiesGet && value.Code == protocol.FaceErrorInvalidRequest {
		m.capabilitiesKnown = false
		m.legacyCapabilities = true
		m.syncCapabilities = true
		return m.finishBaseSync()
	}
	if pending.Operation == protocol.FaceOperationConversationList && value.Code == protocol.FaceErrorForbidden {
		m.syncConversations = true
		return m.finishBaseSync()
	}
	if pending.Operation == protocol.FaceOperationCapabilitiesGet {
		m.legacyCapabilities = true
		m.syncCapabilities = true
		return m.finishBaseSync()
	}
	if pending.Operation == protocol.FaceOperationConversationList {
		m.syncConversations = true
		return m.finishBaseSync()
	}
	if pending.Operation == protocol.FaceOperationChatStreamGet {
		m.stopRecovering(pending.ConversationID, pending.TargetID)
	}
	if pending.Operation == protocol.FaceOperationConversationMessages {
		if conversation := m.conversations[pending.ConversationID]; conversation != nil {
			conversation.Paging = false
		}
	}
	if pending.Operation == protocol.FaceOperationConversationCreate && pending.TargetID == "draft" {
		m.flow = nil
	}
	if (pending.Operation == protocol.FaceOperationSubscribe || pending.Operation == protocol.FaceOperationConversationSnapshot) &&
		pending.TargetID == "flow" {
		m.flow = nil
	}
	if (pending.Operation == protocol.FaceOperationSubscribe || pending.Operation == protocol.FaceOperationConversationSnapshot) &&
		pending.TargetID == "sync" {
		m.state = stateReady
	}
	m.status = sanitizeRemoteText(value.Message)
	return nil
}

func (m *Model) applySnapshotResponse(value protocol.FaceSnapshot) tea.Cmd {
	pending, ok := m.correlated(value.RequestID, protocol.FaceOperationConversationSnapshot, value.Snapshot.ConversationID)
	if !ok {
		return m.disconnect(fmt.Errorf("Mind sent an uncorrelated snapshot response"))
	}
	delete(m.pending, value.RequestID)
	m.installSnapshot(value.Snapshot)
	var commands []tea.Cmd
	for _, chat := range value.Snapshot.PendingChats {
		cmd, err := m.recoverChat(value.Snapshot.ConversationID, chat.RequestID)
		if err == nil && cmd != nil {
			commands = append(commands, cmd)
		}
	}
	if pending.TargetID == "flow" && m.flow != nil {
		m.flow.ConversationID = value.Snapshot.ConversationID
		cmd, err := m.sendChat(m.flow)
		if err != nil {
			m.status = err.Error()
		} else {
			commands = append(commands, cmd)
			m.flow = nil
		}
	}
	if pending.TargetID == "sync" || pending.TargetID == "open" {
		for _, flow := range m.outgoingChats {
			if flow.ConversationID != value.Snapshot.ConversationID {
				continue
			}
			chat := m.conversations[flow.ConversationID].Chats[flow.ChatRequestID]
			if chat != nil && !chat.Terminal {
				cmd, err := m.sendChat(flow)
				if err == nil {
					commands = append(commands, cmd)
				}
			}
		}
	}
	if pending.TargetID == "sync" {
		m.state = stateReady
		m.retryAttempt = 0
		m.status = "Ready"
	}
	m.refreshViewport(false)
	return tea.Batch(commands...)
}

func (m *Model) installCapabilities(data protocol.FaceCapabilitiesResult) {
	m.capabilitiesKnown = true
	m.legacyCapabilities = false
	m.features = make(map[protocol.FaceFeature]struct{}, len(data.Features))
	m.scopes = make(map[protocol.FaceScope]struct{}, len(data.Identity.Scopes))
	for _, feature := range data.Features {
		m.features[feature] = struct{}{}
	}
	for _, scope := range data.Identity.Scopes {
		m.scopes[scope] = struct{}{}
	}
	m.limits = data.Limits
	if !m.hasScope(protocol.FaceScopeApprove) {
		m.modal = nil
	}
}

func (m *Model) finishBaseSync() tea.Cmd {
	if !m.syncCapabilities || !m.syncConversations {
		return nil
	}
	var commands []tea.Cmd
	if m.activeID != "" && m.conversations[m.activeID] != nil && m.hasScope(protocol.FaceScopeSessionsRead) {
		cmd, err := m.requestSubscribe(m.activeID, "sync")
		if err == nil {
			commands = append(commands, cmd)
		} else {
			m.status = err.Error()
		}
	} else {
		m.state = stateReady
		m.retryAttempt = 0
		m.status = "Ready"
	}
	if m.hasScope(protocol.FaceScopeHandsRead) {
		cmd, err := m.requestHands()
		if err == nil {
			commands = append(commands, cmd)
		}
	}
	return tea.Batch(commands...)
}

func (m *Model) installConversationList(summaries []protocol.ConversationSummary) {
	seen := make(map[string]struct{}, len(summaries))
	for _, summary := range summaries {
		summary.Name = sanitizeRemoteText(summary.Name)
		seen[summary.ConversationID] = struct{}{}
		m.upsertConversationSummary(summary)
	}
	for id := range m.conversations {
		if _, ok := seen[id]; !ok && id != m.activeID {
			delete(m.conversations, id)
		}
	}
	m.sortConversationOrder()
}

func (m *Model) upsertConversationSummary(summary protocol.ConversationSummary) {
	conversation := m.conversations[summary.ConversationID]
	if conversation == nil {
		conversation = newConversation(summary.ConversationID)
		m.conversations[summary.ConversationID] = conversation
	}
	summary.Name = sanitizeRemoteText(summary.Name)
	conversation.Summary = summary
	m.sortConversationOrder()
}

func (m *Model) sortConversationOrder() {
	m.conversationOrder = m.conversationOrder[:0]
	for id := range m.conversations {
		m.conversationOrder = append(m.conversationOrder, id)
	}
	sort.SliceStable(m.conversationOrder, func(i, j int) bool {
		left, right := m.conversations[m.conversationOrder[i]].Summary, m.conversations[m.conversationOrder[j]].Summary
		if left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.ConversationID < right.ConversationID
		}
		return left.UpdatedAt.After(right.UpdatedAt)
	})
}

func (m *Model) installCreatedConversation(summary protocol.ConversationSummary, purpose string) tea.Cmd {
	summary.Name = sanitizeRemoteText(summary.Name)
	if purpose == "draft" && m.localDraft != nil {
		conversation := m.localDraft
		conversation.Summary = summary
		m.conversations[summary.ConversationID] = conversation
		m.localDraft = nil
		m.activeID = summary.ConversationID
		if m.flow != nil {
			m.flow.ConversationID = summary.ConversationID
		}
	} else {
		m.upsertConversationSummary(summary)
		m.activeID = summary.ConversationID
		m.localDraft = nil
	}
	m.sortConversationOrder()
	cmd, err := m.requestSubscribe(summary.ConversationID, map[bool]string{true: "flow", false: "open"}[purpose == "draft"])
	if err != nil {
		m.status = err.Error()
		return nil
	}
	return cmd
}

func (m *Model) installSnapshot(snapshot protocol.ConversationSnapshot) {
	conversation := m.conversations[snapshot.ConversationID]
	if conversation == nil {
		conversation = newConversation(snapshot.ConversationID)
		m.conversations[snapshot.ConversationID] = conversation
	}
	conversation.Summary.ConversationID = snapshot.ConversationID
	conversation.Summary.Name = sanitizeRemoteText(snapshot.Name)
	conversation.Summary.Mode = sanitizeRemoteText(snapshot.Mode)
	conversation.Summary.ActiveHand = snapshot.ActiveHand
	for _, message := range snapshot.Messages {
		message.Content = sanitizeRemoteText(message.Content)
		conversation.Messages[message.Seq] = message
	}
	entries := make([]string, 0)
	minimumSeq := 0
	for _, message := range conversation.sortedMessages() {
		if minimumSeq == 0 || message.Seq < minimumSeq {
			minimumSeq = message.Seq
		}
		if message.Role == "user" {
			entries = append(entries, message.Content)
		}
		if message.RequestID != "" && message.Role == "assistant" {
			if chat := conversation.Chats[message.RequestID]; chat != nil && chat.Terminal {
				delete(conversation.Chats, message.RequestID)
			}
		}
	}
	conversation.History.replace(entries)
	for _, flow := range m.outgoingChats {
		if flow.ConversationID == snapshot.ConversationID {
			conversation.History.add(flow.Text)
		}
	}
	conversation.HasMore = minimumSeq > 1
	conversation.NextBeforeSeq = minimumSeq
	if m.hasScope(protocol.FaceScopeApprove) {
		conversation.Approvals = make(map[string]protocol.ApprovalSummary, len(snapshot.PendingApprovals))
		for _, approval := range snapshot.PendingApprovals {
			approval.Tool = sanitizeRemoteText(approval.Tool)
			approval.Reason = sanitizeRemoteText(approval.Reason)
			conversation.Approvals[approval.ApprovalID] = approval
		}
	}
	if m.hasScope(protocol.FaceScopeRunsRead) {
		for _, run := range snapshot.ActiveRuns {
			conversation.Runs[run.RunID] = run
		}
	}
	if m.hasScope(protocol.FaceScopeTasksRead) {
		for _, task := range snapshot.Tasks {
			conversation.Tasks[task.TaskID] = sanitizeTask(task)
		}
	}
	for _, pendingChat := range snapshot.PendingChats {
		if conversation.Chats[pendingChat.RequestID] == nil {
			conversation.Chats[pendingChat.RequestID] = &chatState{
				RequestID: pendingChat.RequestID, Accepted: true,
				Responses: make(map[int]*responseState), Buffered: make(map[int64]protocol.FaceChatDelta),
			}
			conversation.ChatOrder = append(conversation.ChatOrder, pendingChat.RequestID)
		}
	}
	if m.activeID == snapshot.ConversationID {
		m.chooseApprovalModal()
	}
}

func (m *Model) installMessagePage(conversationID string, data protocol.ConversationMessagesResult) {
	conversation := m.conversations[conversationID]
	if conversation == nil {
		return
	}
	for _, message := range data.Messages {
		message.Content = sanitizeRemoteText(message.Content)
		conversation.Messages[message.Seq] = message
	}
	conversation.HasMore = data.HasMore
	conversation.NextBeforeSeq = data.NextBeforeSeq
	conversation.Paging = false
}

func (m *Model) finishChat(conversationID string, result protocol.FaceResult) tea.Cmd {
	conversation := m.conversations[conversationID]
	if conversation == nil {
		return nil
	}
	chat := conversation.Chats[result.RequestID]
	if chat == nil {
		chat = &chatState{RequestID: result.RequestID, Responses: make(map[int]*responseState), Buffered: make(map[int64]protocol.FaceChatDelta)}
		conversation.Chats[result.RequestID] = chat
		conversation.ChatOrder = append(conversation.ChatOrder, result.RequestID)
	}
	chat.Terminal, chat.Sending, chat.Status = true, false, result.Status
	content := sanitizeRemoteText(result.Content)
	lastIndex := 1
	for index := range chat.Responses {
		if index > lastIndex {
			lastIndex = index
		}
	}
	response := chat.Responses[lastIndex]
	if response == nil {
		response = &responseState{Index: lastIndex}
		chat.Responses[lastIndex] = response
	}
	response.Content, response.Complete, response.Rendered = content, true, ""
	delete(m.outgoingChats, result.RequestID)
	m.refreshViewport(true)
	cmd, err := m.requestSnapshot(conversationID, "refresh")
	if err != nil {
		return nil
	}
	return cmd
}

func (m *Model) applyDelta(delta protocol.FaceChatDelta) tea.Cmd {
	conversation := m.conversations[delta.ConversationID]
	if conversation == nil {
		return nil
	}
	chat := conversation.Chats[delta.RequestID]
	if chat == nil {
		chat = &chatState{RequestID: delta.RequestID, Accepted: true, Responses: make(map[int]*responseState), Buffered: make(map[int64]protocol.FaceChatDelta)}
		conversation.Chats[delta.RequestID] = chat
		conversation.ChatOrder = append(conversation.ChatOrder, delta.RequestID)
	}
	if chat.Terminal {
		return nil
	}
	response := chat.Responses[delta.ResponseIndex]
	if response == nil {
		response = &responseState{Index: delta.ResponseIndex}
		chat.Responses[delta.ResponseIndex] = response
	}
	clean := sanitizeRemoteText(delta.Delta)
	if delta.Seq != chat.LastSeq+1 || delta.Offset != response.NextOffset {
		chat.Buffered[delta.Seq] = delta
		cmd, err := m.recoverChat(delta.ConversationID, delta.RequestID)
		if err != nil {
			m.status = err.Error()
		}
		return cmd
	}
	response.Content += clean
	response.NextOffset += int64(len(delta.Delta))
	response.Rendered = ""
	chat.LastSeq = delta.Seq
	m.refreshViewport(true)
	return nil
}

func (m *Model) installStream(conversationID string, data protocol.ChatStreamGetResult) {
	conversation := m.conversations[conversationID]
	if conversation == nil {
		return
	}
	chat := conversation.Chats[data.TargetRequestID]
	if chat == nil {
		chat = &chatState{RequestID: data.TargetRequestID, Responses: make(map[int]*responseState), Buffered: make(map[int64]protocol.FaceChatDelta)}
		conversation.Chats[data.TargetRequestID] = chat
		conversation.ChatOrder = append(conversation.ChatOrder, data.TargetRequestID)
	}
	chat.Responses = make(map[int]*responseState, len(data.Responses))
	for _, response := range data.Responses {
		chat.Responses[response.ResponseIndex] = &responseState{
			Index: response.ResponseIndex, Content: sanitizeRemoteText(response.Content),
			NextOffset: int64(len(response.Content)), Complete: response.Complete,
		}
	}
	chat.LastSeq, chat.Recovering, chat.Terminal, chat.Status = data.LastSeq, false, data.Terminal, data.Status
	var sequence []int64
	for seq := range chat.Buffered {
		if seq > chat.LastSeq {
			sequence = append(sequence, seq)
		}
	}
	sort.Slice(sequence, func(i, j int) bool { return sequence[i] < sequence[j] })
	buffered := chat.Buffered
	chat.Buffered = make(map[int64]protocol.FaceChatDelta)
	for _, seq := range sequence {
		delta := buffered[seq]
		response := chat.Responses[delta.ResponseIndex]
		if response != nil && delta.Seq == chat.LastSeq+1 && delta.Offset == response.NextOffset {
			response.Content += sanitizeRemoteText(delta.Delta)
			response.NextOffset += int64(len(delta.Delta))
			chat.LastSeq = delta.Seq
		} else {
			chat.Buffered[seq] = delta
		}
	}
	m.refreshViewport(true)
}

func (m *Model) applyStreamEnd(end protocol.FaceChatStreamEnd) tea.Cmd {
	conversation := m.conversations[end.ConversationID]
	if conversation == nil {
		return nil
	}
	chat := conversation.Chats[end.RequestID]
	if chat == nil {
		chat = &chatState{RequestID: end.RequestID, Responses: make(map[int]*responseState), Buffered: make(map[int64]protocol.FaceChatDelta)}
		conversation.Chats[end.RequestID] = chat
		conversation.ChatOrder = append(conversation.ChatOrder, end.RequestID)
	}
	chat.Terminal, chat.Status = true, end.Status
	for _, response := range chat.Responses {
		response.Complete = true
	}
	if end.LastSeq != chat.LastSeq {
		cmd, err := m.recoverChat(end.ConversationID, end.RequestID)
		if err == nil {
			return cmd
		}
	}
	m.refreshViewport(true)
	return nil
}

func (m *Model) stopRecovering(conversationID, requestID string) {
	if conversation := m.conversations[conversationID]; conversation != nil {
		if chat := conversation.Chats[requestID]; chat != nil {
			chat.Recovering = false
		}
	}
}

func (m *Model) applyEvent(event protocol.FaceEvent) tea.Cmd {
	conversation := m.conversations[event.ConversationID]
	switch event.Type {
	case protocol.FaceEventChatToolCalled:
		data, err := protocol.StrictDecode[protocol.ChatToolCalledEventData](event.Data)
		if err == nil && conversation != nil {
			chat := ensureChat(conversation, data.RequestID)
			chat.Tools = append(chat.Tools, toolActivity{
				Tool: sanitizeRemoteText(data.Tool), ArgsDigest: data.ArgsDigest, AfterResponse: highestResponseIndex(chat),
			})
		}
	case protocol.FaceEventChatToolCompleted:
		data, err := protocol.StrictDecode[protocol.ChatToolCompletedEventData](event.Data)
		if err == nil && conversation != nil {
			chat := ensureChat(conversation, data.RequestID)
			for index := len(chat.Tools) - 1; index >= 0; index-- {
				if chat.Tools[index].Tool == sanitizeRemoteText(data.Tool) && !chat.Tools[index].Complete {
					chat.Tools[index].Complete, chat.Tools[index].Success = true, data.Success
					break
				}
			}
		}
	case protocol.FaceEventApprovalRequested:
		data, err := protocol.StrictDecode[protocol.ApprovalRequestedEventData](event.Data)
		if err == nil && conversation != nil && m.hasScope(protocol.FaceScopeApprove) {
			data.Tool, data.Reason = sanitizeRemoteText(data.Tool), sanitizeRemoteText(data.Reason)
			conversation.Approvals[data.ApprovalID] = data
			if event.ConversationID == m.activeID {
				m.chooseApprovalModal()
			}
		}
	case protocol.FaceEventApprovalResolved:
		data, err := protocol.StrictDecode[protocol.ApprovalResolvedEventData](event.Data)
		if err == nil && conversation != nil {
			delete(conversation.Approvals, data.ApprovalID)
			if m.modal != nil && m.modal.Approval.ApprovalID == data.ApprovalID {
				m.modal = nil
				m.focus = focusComposer
			}
		}
	case protocol.FaceEventRemoteRunChanged:
		data, err := protocol.StrictDecode[protocol.RemoteRunChangedEventData](event.Data)
		if err == nil && conversation != nil && m.hasScope(protocol.FaceScopeRunsRead) {
			run := conversation.Runs[data.RunID]
			run.RunID, run.HandID, run.Tool, run.Status, run.DurationMs = data.RunID, data.HandID, sanitizeRemoteText(data.Tool), data.Status, data.DurationMs
			conversation.Runs[data.RunID] = run
		}
	case protocol.FaceEventTaskChanged:
		data, err := protocol.StrictDecode[protocol.TaskChangedEventData](event.Data)
		if err == nil && conversation != nil && m.hasScope(protocol.FaceScopeTasksRead) {
			conversation.Tasks[data.TaskID] = sanitizeTask(data)
		}
	case protocol.FaceEventHandConnected:
		data, err := protocol.StrictDecode[protocol.HandConnectedEventData](event.Data)
		if err == nil && m.hasScope(protocol.FaceScopeHandsRead) {
			m.hands[data.HandID] = protocol.HandSummary{HandID: data.HandID, Hostname: sanitizeRemoteText(data.Hostname), OS: data.OS, Arch: data.Arch, Connected: true}
		}
	case protocol.FaceEventHandDisconnected:
		data, err := protocol.StrictDecode[protocol.HandDisconnectedEventData](event.Data)
		if err == nil {
			hand := m.hands[data.HandID]
			hand.Connected = false
			m.hands[data.HandID] = hand
		}
	case protocol.FaceEventConversationChanged:
		cmd, err := m.requestConversationList()
		if err == nil {
			return cmd
		}
	}
	m.refreshViewport(true)
	return nil
}

func ensureChat(conversation *conversationState, requestID string) *chatState {
	chat := conversation.Chats[requestID]
	if chat == nil {
		chat = &chatState{RequestID: requestID, Responses: make(map[int]*responseState), Buffered: make(map[int64]protocol.FaceChatDelta)}
		conversation.Chats[requestID] = chat
		conversation.ChatOrder = append(conversation.ChatOrder, requestID)
	}
	return chat
}

func highestResponseIndex(chat *chatState) int {
	highest := 0
	for index := range chat.Responses {
		if index > highest {
			highest = index
		}
	}
	return highest
}

func (m *Model) applyRunProgress(progress protocol.FaceRunProgress) {
	conversation := m.conversations[progress.ConversationID]
	if conversation == nil || !m.hasScope(protocol.FaceScopeRunsOutput) {
		return
	}
	output := conversation.RunOutput[progress.RunID]
	if output == nil {
		output = &runOutput{}
		conversation.RunOutput[progress.RunID] = output
	}
	if progress.Seq != output.LastSeq+1 || progress.Gap {
		output.Gap = true
	}
	progress.Data = sanitizeRemoteText(progress.Data)
	output.LastSeq = progress.Seq
	output.Chunks = append(output.Chunks, progress)
	output.Bytes += len(progress.Data)
	for output.Bytes > 1<<20 && len(output.Chunks) > 1 {
		output.Bytes -= len(output.Chunks[0].Data)
		output.Chunks = output.Chunks[1:]
		output.Gap = true
	}
}

func (m *Model) installTaskLog(conversationID string, data protocol.TaskLogResult) {
	conversation := m.conversations[conversationID]
	if conversation == nil {
		return
	}
	log := conversation.TaskLogs[data.TaskID]
	if log == nil {
		log = &taskLog{}
		conversation.TaskLogs[data.TaskID] = log
	}
	if data.Offset == 0 || data.Offset != log.Offset {
		log.Data = ""
	}
	log.Data += sanitizeRemoteText(string(data.Data))
	log.Offset, log.EOF, log.Truncated, log.Loading = data.NextOffset, data.EOF, data.Truncated, false
}

func sanitizeTask(task protocol.TaskSummary) protocol.TaskSummary {
	task.Tool = sanitizeRemoteText(task.Tool)
	task.Error = sanitizeRemoteText(task.Error)
	return task
}

func (m *Model) chooseApprovalModal() {
	if !m.hasScope(protocol.FaceScopeApprove) {
		m.modal = nil
		return
	}
	conversation := m.activeConversation()
	if conversation == nil || len(conversation.Approvals) == 0 {
		m.modal = nil
		return
	}
	if m.modal != nil && conversation.Approvals[m.modal.Approval.ApprovalID].ApprovalID != "" {
		return
	}
	ids := make([]string, 0, len(conversation.Approvals))
	for id := range conversation.Approvals {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	m.modal = &approvalModal{Approval: conversation.Approvals[ids[0]], Choice: 0}
	m.focus = focusModal
}

func decodeResult[T any](raw json.RawMessage) (T, error) {
	result, err := protocol.StrictDecode[T](raw)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("Mind sent invalid Face result data")
	}
	return result, nil
}

func (m *Model) activeConversation() *conversationState {
	if m.activeID == "" {
		return m.localDraft
	}
	return m.conversations[m.activeID]
}

func conversationTitle(summary protocol.ConversationSummary) string {
	name := strings.TrimSpace(sanitizeRemoteText(summary.Name))
	if name == "" || name == summary.ConversationID {
		return "Untitled"
	}
	return name
}
