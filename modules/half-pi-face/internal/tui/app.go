package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-face/internal/client"
)

// NewModel 创建以本地新对话草稿启动的全屏 Face model。
func NewModel(connector client.Connector) *Model {
	composer := textarea.New()
	composer.Placeholder = "Message Half-Pi"
	composer.Prompt = ""
	composer.ShowLineNumbers = false
	composer.CharLimit = protocol.MaxFaceChatContentBytes
	composer.SetWidth(76)
	composer.SetHeight(3)
	_ = composer.Focus()
	view := viewport.New(76, 12)
	view.MouseWheelEnabled = false
	local := newConversation("")
	local.Summary.Name = "New chat"
	return &Model{
		ctx: context.Background(), connector: connector, generation: 1, state: stateConnecting,
		focus: focusComposer, composer: composer, chatViewport: view,
		conversations: make(map[string]*conversationState), localDraft: local,
		hands: make(map[string]protocol.HandSummary), pending: make(map[string]pendingRequest),
		outgoingChats: make(map[string]*sendFlow), features: make(map[protocol.FaceFeature]struct{}),
		compactRequests: make(map[string]protocol.FaceConversationCompact),
		scopes:          make(map[protocol.FaceScope]struct{}), limits: protocol.FaceProtocolLimits{
			MaxChatContentBytes: protocol.MaxFaceChatContentBytes, MaxMessageListLimit: protocol.MaxFaceMessageListLimit,
		},
		commands: NewCommandRegistry(), idSource: randomID,
	}
}

// Init 开始第一代连接的认证阶段。
func (m *Model) Init() tea.Cmd {
	return func() tea.Msg { return authenticateMsg{generation: m.generation} }
}

// Update 处理所有终端与网络消息，唯一地修改可见状态。
func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) { //nolint:gocyclo
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case authenticateMsg:
		if msg.generation != m.generation {
			return m, nil
		}
		m.state = stateAuthenticating
		m.status = "Authenticating"
		return m, connectCmd(m.ctx, m.connector, m.generation)
	case connectedMsg:
		if msg.generation != m.generation {
			_ = msg.conn.Close()
			return m, nil
		}
		ready := msg.conn.RegisteredEnvelope()
		registered, err := protocol.DecodePayload[protocol.Registered](&ready)
		if err != nil || ready.Type != protocol.TypeRegistered || registered.ProtocolVersion != protocol.ProtocolVersion {
			_ = msg.conn.Close()
			return m, m.disconnect(fmt.Errorf("Mind returned an invalid registered message"))
		}
		if m.conn != nil {
			_ = m.conn.Close()
		}
		m.conn = msg.conn
		m.state = stateSynchronizing
		m.status = "Synchronizing"
		m.permanentError = false
		m.syncCapabilities, m.syncConversations = false, false
		m.capabilitiesKnown, m.legacyCapabilities, m.capabilityFallback = false, false, false
		m.features = make(map[protocol.FaceFeature]struct{})
		m.scopes = make(map[protocol.FaceScope]struct{})
		capabilities, capErr := m.requestCapabilities()
		conversations, listErr := m.requestConversationList()
		if capErr != nil || listErr != nil {
			return m, m.disconnect(fmt.Errorf("start Face synchronization"))
		}
		return m, tea.Batch(readCmd(m.conn, m.generation), capabilities, conversations)
	case connectionFailedMsg:
		if msg.generation != m.generation {
			return m, nil
		}
		m.permanentError = msg.permanent
		m.state = stateOffline
		m.status = sanitizeRemoteText(msg.err.Error())
		if msg.permanent {
			return m, nil
		}
		cmd, retryAt := retryCmd(m.generation, m.retryAttempt)
		m.retryAttempt++
		m.retryAt = retryAt
		return m, cmd
	case envelopeMsg:
		if msg.generation != m.generation || m.conn == nil {
			return m, nil
		}
		cmd := m.applyEnvelope(msg.env)
		if m.conn == nil {
			return m, cmd
		}
		return m, tea.Batch(cmd, readCmd(m.conn, m.generation))
	case connectionLostMsg:
		if msg.generation != m.generation {
			return m, nil
		}
		return m, m.disconnect(msg.err)
	case sendDoneMsg:
		if msg.generation != m.generation {
			return m, nil
		}
		if pending, ok := m.pending[msg.requestID]; ok {
			pending.Sent = true
			m.pending[msg.requestID] = pending
		}
		if msg.err != nil {
			return m, m.disconnect(msg.err)
		}
		return m, nil
	case retryMsg:
		if msg.generation != m.generation || m.state != stateOffline || m.permanentError {
			return m, nil
		}
		return m, m.startConnection()
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

func (m *Model) startConnection() tea.Cmd {
	if m.conn != nil {
		_ = m.conn.Close()
		m.conn = nil
	}
	m.generation++
	m.state = stateConnecting
	m.status = "Connecting"
	m.retryAt = time.Time{}
	return func() tea.Msg { return authenticateMsg{generation: m.generation} }
}

func (m *Model) disconnect(cause error) tea.Cmd {
	if m.conn != nil {
		_ = m.conn.Close()
		m.conn = nil
	}
	for requestID, pending := range m.pending {
		if pending.Operation == protocol.FaceOperationConversationCompact {
			pending.Sent = false
			m.pending[requestID] = pending
			continue
		}
		if pending.Mutation && pending.Sent {
			if conversation := m.conversations[pending.ConversationID]; conversation != nil {
				conversation.Notice = "An operation may have completed while disconnected; state will be reconciled."
			} else if m.localDraft != nil {
				m.localDraft.Notice = "Conversation creation outcome is uncertain; review the refreshed list before retrying."
			}
		}
		if pending.Operation == protocol.FaceOperationConversationMessages {
			if conversation := m.conversations[pending.ConversationID]; conversation != nil {
				conversation.Paging = false
			}
		}
		delete(m.pending, requestID)
	}
	m.flow = nil
	m.state = stateOffline
	m.status = "Disconnected"
	if cause != nil && !strings.Contains(strings.ToLower(cause.Error()), "closed") {
		m.status = sanitizeRemoteText(cause.Error())
	}
	cmd, retryAt := retryCmd(m.generation, m.retryAttempt)
	m.retryAttempt++
	m.retryAt = retryAt
	return cmd
}

func (m *Model) resize(width, height int) {
	m.width, m.height = width, height
	result := calculateLayout(width, height)
	if !result.TooSmall && !result.Short {
		lineCount := max(3, m.composer.LineCount())
		maxLines := min(10, max(3, height/3-2))
		lines := min(lineCount, maxLines)
		delta := lines - 3
		result.Composer.Y -= delta
		result.Composer.H += delta
		result.Chat.H -= delta
		result.Conversations.H -= delta
		if result.Mode == layoutWide {
			result.Activity.H -= delta
		} else {
			result.Overlay.H -= delta
			result.Activity.H -= delta
		}
		result.Send.Y = result.Composer.Y + 1
	}
	m.layout = result
	if result.TooSmall {
		return
	}
	m.composer.SetWidth(max(20, result.Composer.W-4))
	m.composer.SetHeight(max(1, result.Composer.H-3))
	m.chatViewport.Width = max(1, result.Chat.W-4)
	m.chatViewport.Height = max(1, result.Chat.H-3)
	m.refreshViewport(false)
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) { //nolint:gocyclo
	key := msg.String()
	if key == "ctrl+l" && m.state == stateOffline {
		m.permanentError = false
		return m, m.startConnection()
	}
	if msg.Paste {
		return m.updateComposer(msg)
	}
	if m.modal != nil {
		return m.handleModalKey(key)
	}
	if m.overlay != overlayNone {
		if model, cmd, consumed := m.handleOverlayKey(msg); consumed {
			return model, cmd
		}
	}
	if len(m.completion) > 0 {
		switch key {
		case "up":
			m.completionIndex = (m.completionIndex - 1 + len(m.completion)) % len(m.completion)
			return m, nil
		case "down":
			m.completionIndex = (m.completionIndex + 1) % len(m.completion)
			return m, nil
		case "tab":
			m.acceptCompletion()
			return m, nil
		case "esc":
			m.completion = nil
			return m, nil
		}
	}
	switch key {
	case "ctrl+c":
		if m.hasActiveChat() {
			cmd, err := m.cancelChat("")
			m.setCommandError(err)
			return m, cmd
		}
		m.overlay, m.focus = overlayExit, focusOverlay
		return m, nil
	case "ctrl+n":
		m.newDraftConversation()
		m.refreshViewport(false)
		return m, nil
	case "ctrl+p":
		m.overlay, m.focus, m.selectedOverlay = overlayConversations, focusOverlay, 0
		m.pickerQuery = ""
		return m, nil
	case "ctrl+k":
		m.openPalette()
		return m, nil
	case "ctrl+r":
		m.openHistorySearch()
		return m, nil
	case "ctrl+o":
		m.overlay, m.focus = overlayActivity, focusOverlay
		m.normalizeActivityTab()
		return m, nil
	case "tab", "shift+tab":
		m.cycleFocus(key == "shift+tab")
		return m, nil
	case "pgup":
		m.chatViewport.PageUp()
		m.markScrollState()
		return m, m.maybePageHistory()
	case "pgdown":
		m.chatViewport.PageDown()
		m.markScrollState()
		return m, nil
	case "esc":
		m.overlay, m.completion = overlayNone, nil
		m.focus = focusComposer
		return m, nil
	case "alt+enter", "shift+enter":
		m.composer.InsertString("\n")
		m.resize(m.width, m.height)
		return m, nil
	case "enter":
		if m.focus == focusComposer {
			cmd, err := m.submitComposer()
			m.setCommandError(err)
			m.resize(m.width, m.height)
			m.refreshViewport(true)
			return m, cmd
		}
	case "up":
		if m.focus == focusComposer && m.composer.Line() == 0 {
			if value, ok := m.activeConversation().History.previous(m.composer.Value()); ok {
				m.composer.SetValue(value)
				return m, nil
			}
		}
	case "down":
		if m.focus == focusComposer && m.composer.Line() == m.composer.LineCount()-1 {
			if value, ok := m.activeConversation().History.next(m.composer.Value()); ok {
				m.composer.SetValue(value)
				return m, nil
			}
		}
	}
	if m.focus == focusComposer {
		return m.updateComposer(msg)
	}
	if m.focus == focusChat {
		updated, cmd := m.chatViewport.Update(msg)
		m.chatViewport = updated
		m.markScrollState()
		return m, cmd
	}
	return m.handleFocusedListKey(key)
}

func (m *Model) updateComposer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	before := m.composer.Value()
	updated, cmd := m.composer.Update(msg)
	m.composer = updated
	value := sanitizeInput(m.composer.Value())
	limit := m.limits.MaxChatContentBytes
	if limit <= 0 {
		limit = protocol.MaxFaceChatContentBytes
	}
	if len(value) > limit {
		value = truncateUTF8Bytes(value, limit)
		m.status = fmt.Sprintf("Input is limited to %d bytes", limit)
	}
	if value != m.composer.Value() {
		m.composer.SetValue(value)
	}
	if before != m.composer.Value() {
		if conversation := m.activeConversation(); conversation != nil {
			conversation.History.edit()
			conversation.Draft = m.composer.Value()
		}
		m.completion = m.commands.Complete(m, m.composer.Value())
		m.completionIndex = 0
		m.resize(m.width, m.height)
	}
	return m, cmd
}

func (m *Model) handleModalKey(key string) (tea.Model, tea.Cmd) {
	if m.modal.Resolving {
		if key == "esc" {
			m.status = "Waiting for the authoritative approval result"
		}
		return m, nil
	}
	switch key {
	case "left", "up", "shift+tab":
		m.modal.Choice = (m.modal.Choice + 3) % 4
	case "right", "down", "tab":
		m.modal.Choice = (m.modal.Choice + 1) % 4
	case "enter":
		decisions := []protocol.FaceApprovalDecision{
			protocol.FaceApprovalDenyOnce, protocol.FaceApprovalAllowOnce,
			protocol.FaceApprovalDenySession, protocol.FaceApprovalAllowSession,
		}
		cmd, err := m.resolveApproval(m.modal.Approval.ApprovalID, decisions[m.modal.Choice], "")
		m.setCommandError(err)
		return m, cmd
	case "esc":
		m.modal = nil
		m.focus = focusComposer
	}
	return m, nil
}

func (m *Model) handleOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) { //nolint:gocyclo
	key := msg.String()
	if m.overlay == overlayExit {
		switch key {
		case "enter", "y":
			return m, tea.Quit, true
		case "esc", "n":
			m.overlay, m.focus = overlayNone, focusComposer
			return m, nil, true
		default:
			return m, nil, true
		}
	}
	if key == "esc" {
		m.closeOverlay(true)
		return m, nil, true
	}
	if m.overlay == overlayPalette {
		if key == "up" || key == "down" || key == "enter" || key == "tab" {
			return m, nil, false
		}
		model, cmd := m.updateComposer(msg)
		return model, cmd, true
	}
	if m.overlay == overlayHistory {
		switch key {
		case "backspace":
			runes := []rune(m.historyQuery)
			if len(runes) > 0 {
				m.historyQuery = string(runes[:len(runes)-1])
			}
			m.filterHistory()
			return m, nil, true
		default:
			if msg.Type == tea.KeyRunes {
				m.historyQuery += string(msg.Runes)
				m.filterHistory()
				return m, nil, true
			}
		}
	}
	if m.overlay == overlayConversations {
		switch key {
		case "backspace":
			runes := []rune(m.pickerQuery)
			if len(runes) > 0 {
				m.pickerQuery = string(runes[:len(runes)-1])
			}
			m.selectedOverlay = 0
			return m, nil, true
		default:
			if msg.Type == tea.KeyRunes {
				m.pickerQuery += string(msg.Runes)
				m.selectedOverlay = 0
				return m, nil, true
			}
		}
	}
	if m.overlay == overlayActivity && key == "c" {
		cmd, err := m.cancelActivitySelection()
		m.setCommandError(err)
		return m, cmd, true
	}
	items := m.overlayItemCount()
	switch key {
	case "up":
		if items > 0 {
			if m.overlay == overlayActivity {
				m.selectedActivity = (m.selectedActivity - 1 + items) % items
			} else {
				m.selectedOverlay = (m.selectedOverlay - 1 + items) % items
			}
		}
		return m, nil, true
	case "down":
		if items > 0 {
			if m.overlay == overlayActivity {
				m.selectedActivity = (m.selectedActivity + 1) % items
			} else {
				m.selectedOverlay = (m.selectedOverlay + 1) % items
			}
		}
		return m, nil, true
	case "left", "right":
		if m.overlay == overlayActivity {
			m.moveActivityTab(key == "right")
			return m, nil, true
		}
	case "enter":
		switch m.overlay {
		case overlayConversations:
			cmd, err := m.selectConversationOverlay()
			m.setCommandError(err)
			return m, cmd, true
		case overlayHistory:
			if m.selectedOverlay < len(m.completion) {
				m.composer.SetValue(m.completion[m.selectedOverlay].Insert)
				m.overlay, m.completion, m.focus = overlayNone, nil, focusComposer
			}
			return m, nil, true
		case overlayActivity:
			cmd, err := m.activateActivitySelection()
			m.setCommandError(err)
			return m, cmd, true
		}
	}
	return m, nil, true
}

func (m *Model) handleFocusedListKey(key string) (tea.Model, tea.Cmd) {
	if m.focus == focusConversations {
		items := len(m.conversationOrder) + 1
		switch key {
		case "up":
			m.selectedConversation = (m.selectedConversation - 1 + items) % items
		case "down":
			m.selectedConversation = (m.selectedConversation + 1) % items
		case "enter":
			m.selectedOverlay = m.selectedConversation
			cmd, err := m.selectConversationOverlay()
			m.setCommandError(err)
			return m, cmd
		}
	}
	if m.focus == focusActivity {
		switch key {
		case "left", "right":
			m.moveActivityTab(key == "right")
		case "up":
			m.selectedActivity = max(0, m.selectedActivity-1)
		case "down":
			m.selectedActivity++
		case "enter":
			cmd, err := m.activateActivitySelection()
			m.setCommandError(err)
			return m, cmd
		case "c":
			cmd, err := m.cancelActivitySelection()
			m.setCommandError(err)
			return m, cmd
		}
	}
	return m, nil
}

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	event := tea.MouseEvent(msg)
	activityArea := m.layout.Activity
	conversationArea := m.layout.Conversations
	if m.overlay == overlayActivity {
		activityArea = m.layout.Overlay
	}
	if m.overlay == overlayConversations {
		conversationArea = m.layout.Overlay
	}
	if event.Shift || m.layout.TooSmall {
		return m, nil
	}
	if event.IsWheel() {
		if m.overlay == overlayConversations {
			if event.Button == tea.MouseButtonWheelUp {
				m.selectedOverlay = max(0, m.selectedOverlay-1)
			} else {
				m.selectedOverlay = min(len(m.filteredConversationIDs()), m.selectedOverlay+1)
			}
			return m, nil
		}
		if m.overlay == overlayActivity {
			if event.Button == tea.MouseButtonWheelUp {
				m.selectedActivity = max(0, m.selectedActivity-1)
			} else {
				m.selectedActivity = min(max(0, m.activityItemCount()-1), m.selectedActivity+1)
			}
			return m, nil
		}
		if m.layout.Chat.contains(event.X, event.Y) || !m.layout.Conversations.contains(event.X, event.Y) && !m.layout.Activity.contains(event.X, event.Y) {
			if event.Button == tea.MouseButtonWheelUp {
				m.chatViewport.LineUp(3)
			} else if event.Button == tea.MouseButtonWheelDown {
				m.chatViewport.LineDown(3)
			}
			m.markScrollState()
			return m, m.maybePageHistory()
		}
		if conversationArea.contains(event.X, event.Y) {
			if event.Button == tea.MouseButtonWheelUp {
				m.selectedConversation = max(0, m.selectedConversation-1)
			} else {
				m.selectedConversation = min(len(m.conversationOrder), m.selectedConversation+1)
			}
		}
		if activityArea.contains(event.X, event.Y) {
			if event.Button == tea.MouseButtonWheelUp {
				m.selectedActivity = max(0, m.selectedActivity-1)
			} else {
				m.selectedActivity = min(max(0, m.activityItemCount()-1), m.selectedActivity+1)
			}
		}
		return m, nil
	}
	if event.Action != tea.MouseActionPress || event.Button != tea.MouseButtonLeft {
		return m, nil
	}
	if m.modal != nil {
		if event.Y >= m.layout.Overlay.Y+6 {
			relative := max(0, event.X-m.layout.Overlay.X)
			m.modal.Choice = min(3, relative*4/max(1, m.layout.Overlay.W))
			decisions := []protocol.FaceApprovalDecision{
				protocol.FaceApprovalDenyOnce, protocol.FaceApprovalAllowOnce,
				protocol.FaceApprovalDenySession, protocol.FaceApprovalAllowSession,
			}
			cmd, err := m.resolveApproval(m.modal.Approval.ApprovalID, decisions[m.modal.Choice], "")
			m.setCommandError(err)
			return m, cmd
		}
		return m, nil
	}
	if m.layout.Send.contains(event.X, event.Y) {
		if m.hasActiveChat() {
			cmd, err := m.cancelChat("")
			m.setCommandError(err)
			return m, cmd
		}
		cmd, err := m.submitComposer()
		m.setCommandError(err)
		return m, cmd
	}
	if m.layout.Composer.contains(event.X, event.Y) {
		m.setFocus(focusComposer)
		return m, nil
	}
	if conversationArea.contains(event.X, event.Y) && (m.layout.Mode != layoutCompact || m.overlay == overlayConversations) {
		rowOffset := 2
		if m.overlay == overlayConversations {
			rowOffset = 4
		}
		row := event.Y - conversationArea.Y - rowOffset
		if row >= 0 && row <= len(m.filteredConversationIDs()) {
			m.selectedOverlay = row
			cmd, err := m.selectConversationOverlay()
			m.setCommandError(err)
			return m, cmd
		}
		m.setFocus(focusConversations)
	}
	if activityArea.contains(event.X, event.Y) && (m.layout.Mode == layoutWide || m.overlay == overlayActivity) {
		m.setFocus(focusActivity)
		row := event.Y - activityArea.Y - 4
		if row >= 0 && row < m.activityItemCount() {
			m.selectedActivity = row
		}
		if event.Y == activityArea.Y+2 {
			m.selectActivityTabAt(event.X - activityArea.X - 2)
		}
		return m, nil
	}
	if m.layout.Chat.contains(event.X, event.Y) {
		m.setFocus(focusChat)
	}
	return m, nil
}

func (m *Model) setFocus(target focusTarget) {
	m.focus = target
	if target == focusComposer {
		_ = m.composer.Focus()
	} else {
		m.composer.Blur()
	}
}

func (m *Model) cycleFocus(reverse bool) {
	visible := []focusTarget{focusConversations, focusChat, focusComposer}
	if m.layout.Mode == layoutWide && m.activityTabAvailable(m.activityTab) {
		visible = []focusTarget{focusConversations, focusChat, focusActivity, focusComposer}
	}
	index := 0
	for i, target := range visible {
		if target == m.focus {
			index = i
			break
		}
	}
	if reverse {
		index = (index - 1 + len(visible)) % len(visible)
	} else {
		index = (index + 1) % len(visible)
	}
	m.setFocus(visible[index])
}

func (m *Model) hasActiveChat() bool {
	conversation := m.activeConversation()
	if conversation == nil {
		return false
	}
	for _, chat := range conversation.Chats {
		if !chat.Terminal {
			return true
		}
	}
	return false
}

func (m *Model) setCommandError(err error) {
	if err != nil {
		m.status = err.Error()
	}
}

func (m *Model) openPalette() {
	m.overlayDraft = m.composer.Value()
	m.overlay, m.focus = overlayPalette, focusOverlay
	m.composer.SetValue("/")
	m.completion = m.commands.palette(m, "")
	m.completionIndex = 0
	_ = m.composer.Focus()
}

func (m *Model) closeOverlay(restorePaletteDraft bool) {
	if m.overlay == overlayPalette {
		if restorePaletteDraft {
			m.composer.SetValue(m.overlayDraft)
			if conversation := m.activeConversation(); conversation != nil {
				conversation.Draft = m.overlayDraft
			}
		}
		m.overlayDraft = ""
	}
	m.overlay, m.completion = overlayNone, nil
	m.focus = focusComposer
	_ = m.composer.Focus()
}

func (m *Model) openHistorySearch() {
	conversation := m.activeConversation()
	if conversation == nil {
		return
	}
	m.overlay, m.focus = overlayHistory, focusOverlay
	m.historyQuery = ""
	m.filterHistory()
	m.selectedOverlay = 0
}

func (m *Model) filterHistory() {
	conversation := m.activeConversation()
	if conversation == nil {
		m.completion = nil
		return
	}
	m.completion = m.completion[:0]
	for index := len(conversation.History.entries) - 1; index >= 0; index-- {
		entry := conversation.History.entries[index]
		if fuzzyMatch(entry, m.historyQuery) {
			m.completion = append(m.completion, Completion{Label: entry, Insert: entry})
		}
	}
	if m.selectedOverlay >= len(m.completion) {
		m.selectedOverlay = max(0, len(m.completion)-1)
	}
}

func (m *Model) acceptCompletion() {
	if m.completionIndex < 0 || m.completionIndex >= len(m.completion) {
		return
	}
	completion := m.completion[m.completionIndex]
	value := m.composer.Value()
	if strings.Count(value, " ") == 0 || m.overlay == overlayPalette {
		m.composer.SetValue(completion.Insert)
	} else {
		index := strings.LastIndexAny(value, " \t\n")
		m.composer.SetValue(value[:index+1] + completion.Insert)
	}
	m.completion = nil
	if m.overlay == overlayPalette {
		m.overlayDraft = ""
		m.overlay, m.focus = overlayNone, focusComposer
		if conversation := m.activeConversation(); conversation != nil {
			conversation.Draft = m.composer.Value()
		}
	}
}

func (m *Model) overlayItemCount() int {
	switch m.overlay {
	case overlayConversations:
		return len(m.filteredConversationIDs()) + 1
	case overlayHistory, overlayPalette:
		return len(m.completion)
	case overlayActivity:
		return m.activityItemCount()
	default:
		return 0
	}
}

func (m *Model) selectConversationOverlay() (tea.Cmd, error) {
	if m.selectedOverlay == 0 {
		m.newDraftConversation()
		m.refreshViewport(false)
		return nil, nil
	}
	index := m.selectedOverlay - 1
	ids := m.filteredConversationIDs()
	if index < 0 || index >= len(ids) {
		return nil, nil
	}
	return m.openConversation(ids[index])
}

func (m *Model) filteredConversationIDs() []string {
	if m.pickerQuery == "" {
		return m.conversationOrder
	}
	result := make([]string, 0, len(m.conversationOrder))
	for _, id := range m.conversationOrder {
		conversation := m.conversations[id]
		if conversation != nil && fuzzyMatch(conversation.Summary.Name+" "+id, m.pickerQuery) {
			result = append(result, id)
		}
	}
	return result
}

func (m *Model) markScrollState() {
	if conversation := m.activeConversation(); conversation != nil {
		conversation.AtBottom = m.chatViewport.AtBottom()
		conversation.ScrollOffset = m.chatViewport.YOffset
		if conversation.AtBottom {
			conversation.NewContent = false
		}
	}
}

func (m *Model) maybePageHistory() tea.Cmd {
	conversation := m.activeConversation()
	if conversation == nil || m.activeID == "" || m.chatViewport.YOffset > 2 || !conversation.HasMore || conversation.Paging || !m.hasFeature(protocol.FaceFeatureMessagePaging) {
		return nil
	}
	cmd, err := m.requestMessages(conversation.NextBeforeSeq, 0)
	m.setCommandError(err)
	return cmd
}
