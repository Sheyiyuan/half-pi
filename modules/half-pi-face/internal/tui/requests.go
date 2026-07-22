package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func (m *Model) nextRequestID() (string, error) {
	if m.idSource == nil {
		m.idSource = randomID
	}
	return m.idSource()
}

func (m *Model) sendRequest(requestID, messageType string, payload any, pending pendingRequest) (tea.Cmd, error) {
	if m.conn == nil {
		return nil, fmt.Errorf("Face is not connected")
	}
	if requestID == "" {
		return nil, fmt.Errorf("request ID is required")
	}
	raw, err := json.Marshal(payload)
	if err != nil || protocol.ValidateFacePayload(messageType, raw) != nil {
		return nil, fmt.Errorf("invalid %s request", pending.Operation)
	}
	m.pending[requestID] = pending
	return sendEnvelopeCmd(m.conn, m.generation, requestID, messageType, payload), nil
}

func (m *Model) hasScope(scope protocol.FaceScope) bool {
	if !m.capabilitiesKnown {
		return m.legacyCapabilities
	}
	_, ok := m.scopes[scope]
	return ok
}

func (m *Model) hasFeature(feature protocol.FaceFeature) bool {
	_, ok := m.features[feature]
	return ok
}

func (m *Model) requestCapabilities() (tea.Cmd, error) {
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceCapabilitiesGet,
		protocol.FaceCapabilitiesGet{RequestID: requestID, AcceptFeatures: []string{string(protocol.FaceFeatureContextCompaction)}},
		pendingRequest{Operation: protocol.FaceOperationCapabilitiesGet})
}

func (m *Model) requestLegacyCapabilities() (tea.Cmd, error) {
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceCapabilitiesGet,
		protocol.FaceCapabilitiesGet{RequestID: requestID},
		pendingRequest{Operation: protocol.FaceOperationCapabilitiesGet, TargetID: "legacy"})
}

func (m *Model) requestConversationList() (tea.Cmd, error) {
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceConversationList,
		protocol.FaceConversationList{RequestID: requestID},
		pendingRequest{Operation: protocol.FaceOperationConversationList})
}

func (m *Model) createNamedConversation(name string) (tea.Cmd, error) {
	if !m.hasScope(protocol.FaceScopeSessionsWrite) {
		return nil, fmt.Errorf("conversation write permission is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("conversation name is required")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceConversationCreate,
		protocol.FaceConversationCreate{RequestID: requestID, Name: name},
		pendingRequest{Operation: protocol.FaceOperationConversationCreate, Mutation: true, TargetID: "named"})
}

func (m *Model) createDraftConversation(flow *sendFlow) (tea.Cmd, error) {
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	m.flow = flow
	return m.sendRequest(requestID, protocol.TypeFaceConversationCreate,
		protocol.FaceConversationCreate{RequestID: requestID},
		pendingRequest{Operation: protocol.FaceOperationConversationCreate, Mutation: true, TargetID: "draft"})
}

func (m *Model) openConversation(value string) (tea.Cmd, error) {
	conversationID := value
	if _, ok := m.conversations[conversationID]; !ok {
		for id, conversation := range m.conversations {
			if conversation.Summary.Name == value {
				conversationID = id
				break
			}
		}
	}
	if _, ok := m.conversations[conversationID]; !ok {
		return nil, fmt.Errorf("conversation is not available")
	}
	m.saveActiveDraft()
	m.activeID = conversationID
	m.localDraft = nil
	m.installComposerDraft()
	m.overlay = overlayNone
	return m.requestSubscribe(conversationID, "open")
}

func (m *Model) requestSubscribe(conversationID, purpose string) (tea.Cmd, error) {
	if conversationID == "" {
		return nil, fmt.Errorf("conversation ID is required")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	events := make([]protocol.FaceEventType, 0, 12)
	if m.hasScope(protocol.FaceScopeChat) {
		events = append(events, protocol.FaceEventChatStarted, protocol.FaceEventChatToolCalled,
			protocol.FaceEventChatToolCompleted, protocol.FaceEventChatCompleted,
			protocol.FaceEventChatFailed, protocol.FaceEventChatCancelled)
	}
	if m.hasScope(protocol.FaceScopeApprove) {
		events = append(events, protocol.FaceEventApprovalRequested, protocol.FaceEventApprovalResolved)
	}
	if m.hasScope(protocol.FaceScopeRunsRead) {
		events = append(events, protocol.FaceEventRemoteRunChanged)
	}
	if m.hasScope(protocol.FaceScopeHandsRead) {
		events = append(events, protocol.FaceEventHandConnected, protocol.FaceEventHandDisconnected)
	}
	if m.hasScope(protocol.FaceScopeSessionsRead) {
		events = append(events, protocol.FaceEventConversationChanged)
		if m.hasFeature(protocol.FaceFeatureContextCompaction) {
			events = append(events, protocol.FaceEventCompactRequested, protocol.FaceEventCompactStarted,
				protocol.FaceEventCompactCompleted, protocol.FaceEventCompactFailed)
		}
	}
	if m.hasScope(protocol.FaceScopeTasksRead) {
		events = append(events, protocol.FaceEventTaskChanged)
	}
	var transients []protocol.FaceTransientType
	if m.hasFeature(protocol.FaceFeatureChatStream) && m.hasScope(protocol.FaceScopeSessionsRead) {
		transients = append(transients, protocol.FaceTransientChatDelta)
	}
	if m.hasFeature(protocol.FaceFeatureRunProgress) && m.hasScope(protocol.FaceScopeRunsOutput) {
		transients = append(transients, protocol.FaceTransientRunProgress)
	}
	return m.sendRequest(requestID, protocol.TypeFaceSubscribe, protocol.FaceSubscribe{
		RequestID: requestID, ConversationIDs: []string{conversationID},
		EventTypes: events, TransientTypes: transients,
	}, pendingRequest{Operation: protocol.FaceOperationSubscribe, ConversationID: conversationID, TargetID: purpose})
}

func (m *Model) requestCompact(target protocol.FaceCompactTarget) (tea.Cmd, error) {
	if m.activeID == "" || !m.hasFeature(protocol.FaceFeatureContextCompaction) {
		return nil, fmt.Errorf("context compaction is unavailable")
	}
	if !m.hasScope(protocol.FaceScopeSessionsWrite) {
		return nil, fmt.Errorf("conversation write permission is required")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	request := protocol.FaceConversationCompact{RequestID: requestID, ConversationID: m.activeID, Target: target}
	m.compactRequests[requestID] = request
	return m.sendRequest(requestID, protocol.TypeFaceConversationCompact, request,
		pendingRequest{Operation: protocol.FaceOperationConversationCompact, ConversationID: m.activeID, Mutation: true})
}

func (m *Model) requestCompactStatus(conversationID string) (tea.Cmd, error) {
	if conversationID == "" || !m.hasFeature(protocol.FaceFeatureContextCompaction) || !m.hasScope(protocol.FaceScopeSessionsRead) {
		return nil, nil
	}
	for _, pending := range m.pending {
		if pending.Operation == protocol.FaceOperationCompactStatus && pending.ConversationID == conversationID {
			return nil, nil
		}
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceCompactStatus,
		protocol.FaceCompactStatusGet{RequestID: requestID, ConversationID: conversationID},
		pendingRequest{Operation: protocol.FaceOperationCompactStatus, ConversationID: conversationID})
}

func (m *Model) replayCompacts(conversationID string) []tea.Cmd {
	var commands []tea.Cmd
	for requestID, request := range m.compactRequests {
		pending, ok := m.pending[requestID]
		if !ok || pending.ConversationID != conversationID || pending.Sent {
			continue
		}
		command, err := m.sendRequest(requestID, protocol.TypeFaceConversationCompact, request, pending)
		if err == nil && command != nil {
			commands = append(commands, command)
		}
	}
	return commands
}

func (m *Model) requestSnapshot(conversationID, purpose string) (tea.Cmd, error) {
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceConversationSnapshot,
		protocol.FaceConversationSnapshot{RequestID: requestID, ConversationID: conversationID},
		pendingRequest{Operation: protocol.FaceOperationConversationSnapshot, ConversationID: conversationID, TargetID: purpose})
}

func (m *Model) requestMessages(before, limit int) (tea.Cmd, error) {
	conversation := m.activeConversation()
	if conversation == nil || m.activeID == "" {
		return nil, fmt.Errorf("no persisted conversation is open")
	}
	if conversation.Paging {
		return nil, nil
	}
	if before == 0 {
		before = conversation.NextBeforeSeq
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	conversation.Paging = true
	return m.sendRequest(requestID, protocol.TypeFaceConversationMessages,
		protocol.FaceConversationMessages{RequestID: requestID, ConversationID: m.activeID, BeforeSeq: before, Limit: limit},
		pendingRequest{Operation: protocol.FaceOperationConversationMessages, ConversationID: m.activeID})
}

func (m *Model) renameConversation(name string) (tea.Cmd, error) {
	if m.activeID == "" || !m.hasScope(protocol.FaceScopeSessionsWrite) {
		return nil, fmt.Errorf("conversation write permission is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("conversation name is required")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceConversationRename,
		protocol.FaceConversationRename{RequestID: requestID, ConversationID: m.activeID, Name: name},
		pendingRequest{Operation: protocol.FaceOperationConversationRename, ConversationID: m.activeID, Mutation: true})
}

func (m *Model) submitComposer() (tea.Cmd, error) {
	raw := sanitizeInput(m.composer.Value())
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if strings.HasPrefix(strings.TrimLeft(raw, " \t\n"), "/") {
		parsed, err := m.commands.Parse(raw)
		if err != nil {
			return nil, err
		}
		cmd, err := parsed.Spec.Execute(m, parsed)
		if err == nil && m.overlay != overlayPalette {
			m.clearComposer()
			m.completion = nil
		}
		return cmd, err
	}
	if m.state != stateReady {
		return nil, fmt.Errorf("Face is not ready")
	}
	if !m.hasScope(protocol.FaceScopeChat) {
		return nil, fmt.Errorf("Chat permission is required")
	}
	if m.activeID == "" && (!m.hasScope(protocol.FaceScopeSessionsWrite) || !m.hasScope(protocol.FaceScopeSessionsRead)) {
		return nil, fmt.Errorf("conversation read and write permissions are required")
	}
	limit := m.limits.MaxChatContentBytes
	if limit == 0 {
		limit = protocol.MaxFaceChatContentBytes
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("message exceeds the %d byte limit by %d bytes", limit, len(raw)-limit)
	}
	if m.flow != nil {
		return nil, fmt.Errorf("the first message is still creating its conversation")
	}
	if m.hasActiveChat() {
		return nil, fmt.Errorf("the current conversation already has an active Chat")
	}
	chatRequestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	flow := &sendFlow{Text: raw, ChatRequestID: chatRequestID, ConversationID: m.activeID}
	conversation := m.activeConversation()
	if conversation == nil {
		return nil, fmt.Errorf("conversation state is unavailable")
	}
	conversation.Chats[chatRequestID] = &chatState{
		RequestID: chatRequestID, UserText: raw, Sending: true,
		Responses: make(map[int]*responseState), Buffered: make(map[int64]protocol.FaceChatDelta),
	}
	conversation.ChatOrder = append(conversation.ChatOrder, chatRequestID)
	conversation.History.add(raw)
	m.outgoingChats[chatRequestID] = flow
	m.clearComposer()
	m.completion = nil
	if m.activeID == "" {
		return m.createDraftConversation(flow)
	}
	return m.sendChat(flow)
}

func (m *Model) clearComposer() {
	m.composer.Reset()
	if conversation := m.activeConversation(); conversation != nil {
		conversation.Draft = ""
	}
}

func (m *Model) sendChat(flow *sendFlow) (tea.Cmd, error) {
	if flow == nil || flow.ConversationID == "" {
		return nil, fmt.Errorf("Chat conversation is unavailable")
	}
	return m.sendRequest(flow.ChatRequestID, protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: flow.ChatRequestID, ConversationID: flow.ConversationID, Content: flow.Text,
	}, pendingRequest{Operation: protocol.FaceOperationChat, ConversationID: flow.ConversationID})
}

func (m *Model) cancelChat(target string) (tea.Cmd, error) {
	conversation := m.activeConversation()
	if conversation == nil || m.activeID == "" {
		return nil, fmt.Errorf("no persisted conversation is open")
	}
	if target == "" {
		var ids []string
		for id, chat := range conversation.Chats {
			if !chat.Terminal {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		if len(ids) > 0 {
			target = ids[len(ids)-1]
		}
	}
	if target == "" {
		return nil, fmt.Errorf("there is no active Chat")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceChatCancel, protocol.FaceChatCancel{
		RequestID: requestID, TargetRequestID: target, ConversationID: m.activeID, Reason: "user",
	}, pendingRequest{Operation: protocol.FaceOperationChatCancel, ConversationID: m.activeID, TargetID: target, Mutation: true})
}

func (m *Model) recoverChat(conversationID, target string) (tea.Cmd, error) {
	conversation := m.conversations[conversationID]
	if conversation == nil {
		return nil, nil
	}
	chat := conversation.Chats[target]
	if chat == nil {
		chat = &chatState{RequestID: target, Responses: make(map[int]*responseState), Buffered: make(map[int64]protocol.FaceChatDelta)}
		conversation.Chats[target] = chat
	}
	if chat.Recovering || !m.hasFeature(protocol.FaceFeatureChatStreamResume) {
		return nil, nil
	}
	chat.Recovering = true
	requestID, err := m.nextRequestID()
	if err != nil {
		chat.Recovering = false
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceChatStreamGet, protocol.FaceChatStreamGet{
		RequestID: requestID, ConversationID: conversationID, TargetRequestID: target,
	}, pendingRequest{Operation: protocol.FaceOperationChatStreamGet, ConversationID: conversationID, TargetID: target})
}

func (m *Model) resolveApproval(id string, decision protocol.FaceApprovalDecision, reason string) (tea.Cmd, error) {
	if !m.hasScope(protocol.FaceScopeApprove) {
		return nil, fmt.Errorf("approval permission is required")
	}
	conversation := m.activeConversation()
	if conversation == nil || conversation.Approvals[id].ApprovalID == "" {
		return nil, fmt.Errorf("approval is not pending")
	}
	switch decision {
	case protocol.FaceApprovalAllowOnce, protocol.FaceApprovalDenyOnce,
		protocol.FaceApprovalAllowSession, protocol.FaceApprovalDenySession:
	default:
		return nil, fmt.Errorf("invalid approval decision")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	if m.modal != nil && m.modal.Approval.ApprovalID == id {
		m.modal.Resolving = true
	}
	return m.sendRequest(requestID, protocol.TypeFaceApprovalResolve, protocol.FaceApprovalResolve{
		RequestID: requestID, ApprovalID: id, Decision: decision, Reason: reason,
	}, pendingRequest{Operation: protocol.FaceOperationApprovalResolve, ConversationID: m.activeID, TargetID: id, Mutation: true})
}

func (m *Model) requestHands() (tea.Cmd, error) {
	if !m.hasScope(protocol.FaceScopeHandsRead) {
		return nil, fmt.Errorf("Hand read permission is required")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceHandList, protocol.FaceHandList{RequestID: requestID},
		pendingRequest{Operation: protocol.FaceOperationHandList})
}

func (m *Model) requestHand(id string) (tea.Cmd, error) {
	if !m.hasScope(protocol.FaceScopeHandsRead) {
		return nil, fmt.Errorf("Hand read permission is required")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceHandGet, protocol.FaceHandGet{RequestID: requestID, HandID: id},
		pendingRequest{Operation: protocol.FaceOperationHandGet, TargetID: id})
}

func (m *Model) requestRun(id string) (tea.Cmd, error) {
	if m.activeID == "" || !m.hasScope(protocol.FaceScopeRunsRead) {
		return nil, fmt.Errorf("run read permission is required")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceRunGet,
		protocol.FaceRunGet{RequestID: requestID, ConversationID: m.activeID, RunID: id},
		pendingRequest{Operation: protocol.FaceOperationRunGet, ConversationID: m.activeID, TargetID: id})
}

func (m *Model) cancelRun(id string) (tea.Cmd, error) {
	if m.activeID == "" || !m.hasScope(protocol.FaceScopeRunsCancel) {
		return nil, fmt.Errorf("run cancel permission is required")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceRunCancel,
		protocol.FaceRunCancel{RequestID: requestID, ConversationID: m.activeID, RunID: id, Reason: "user"},
		pendingRequest{Operation: protocol.FaceOperationRunCancel, ConversationID: m.activeID, TargetID: id, Mutation: true})
}

func (m *Model) requestTasks() (tea.Cmd, error) {
	if m.activeID == "" || !m.hasScope(protocol.FaceScopeTasksRead) {
		return nil, fmt.Errorf("task read permission is required")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceTaskList,
		protocol.FaceTaskList{RequestID: requestID, ConversationID: m.activeID},
		pendingRequest{Operation: protocol.FaceOperationTaskList, ConversationID: m.activeID})
}

func (m *Model) requestTask(id string) (tea.Cmd, error) {
	if m.activeID == "" || !m.hasScope(protocol.FaceScopeTasksRead) {
		return nil, fmt.Errorf("task read permission is required")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceTaskGet,
		protocol.FaceTaskGet{RequestID: requestID, ConversationID: m.activeID, TaskID: id},
		pendingRequest{Operation: protocol.FaceOperationTaskGet, ConversationID: m.activeID, TargetID: id})
}

func (m *Model) requestTaskLog(id string, offset int64, limit int) (tea.Cmd, error) {
	if m.activeID == "" || !m.hasScope(protocol.FaceScopeTasksRead) {
		return nil, fmt.Errorf("task read permission is required")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	conversation := m.activeConversation()
	log := conversation.TaskLogs[id]
	if log == nil {
		log = &taskLog{}
		conversation.TaskLogs[id] = log
	}
	log.Loading = true
	return m.sendRequest(requestID, protocol.TypeFaceTaskLog,
		protocol.FaceTaskLog{RequestID: requestID, ConversationID: m.activeID, TaskID: id, Offset: offset, Limit: limit},
		pendingRequest{Operation: protocol.FaceOperationTaskLog, ConversationID: m.activeID, TargetID: id})
}

func (m *Model) cancelTask(id string) (tea.Cmd, error) {
	if m.activeID == "" || !m.hasScope(protocol.FaceScopeTasksRead) || !m.hasScope(protocol.FaceScopeTasksCancel) {
		return nil, fmt.Errorf("task read and cancel permissions are required")
	}
	requestID, err := m.nextRequestID()
	if err != nil {
		return nil, err
	}
	return m.sendRequest(requestID, protocol.TypeFaceTaskCancel,
		protocol.FaceTaskCancel{RequestID: requestID, ConversationID: m.activeID, TaskID: id, Reason: "user"},
		pendingRequest{Operation: protocol.FaceOperationTaskCancel, ConversationID: m.activeID, TargetID: id, Mutation: true})
}

func (m *Model) saveActiveDraft() {
	if conversation := m.activeConversation(); conversation != nil {
		conversation.Draft = m.composer.Value()
		conversation.ScrollOffset = m.chatViewport.YOffset
		conversation.AtBottom = m.chatViewport.AtBottom()
	}
}

func (m *Model) installComposerDraft() {
	conversation := m.activeConversation()
	if conversation == nil {
		return
	}
	m.composer.SetValue(conversation.Draft)
	m.chatViewport.SetYOffset(conversation.ScrollOffset)
	if conversation.AtBottom {
		m.chatViewport.GotoBottom()
	}
}

func (m *Model) newDraftConversation() {
	m.saveActiveDraft()
	m.activeID = ""
	m.localDraft = newConversation("")
	m.localDraft.Summary.Name = "New chat"
	m.composer.SetValue("")
	m.overlay = overlayNone
	m.modal = nil
	m.focus = focusComposer
	_ = m.composer.Focus()
}
