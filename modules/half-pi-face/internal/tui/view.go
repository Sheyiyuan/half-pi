package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

var (
	colorPrimary = lipgloss.AdaptiveColor{Light: "24", Dark: "81"}
	colorMuted   = lipgloss.AdaptiveColor{Light: "242", Dark: "245"}
	colorSuccess = lipgloss.AdaptiveColor{Light: "28", Dark: "42"}
	colorWarning = lipgloss.AdaptiveColor{Light: "130", Dark: "214"}
	colorDanger  = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	colorAccent  = lipgloss.AdaptiveColor{Light: "90", Dark: "177"}
	colorBorder  = lipgloss.AdaptiveColor{Light: "250", Dark: "238"}
)

// View 渲染当前全屏工作台。
func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.layout.TooSmall {
		message := lipgloss.NewStyle().Bold(true).Foreground(colorWarning).Render("Terminal size is too small")
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, message)
	}
	header := m.renderHeader()
	content := m.renderContent()
	composer := m.renderComposer()
	footer := m.renderFooter()
	return lipgloss.JoinVertical(lipgloss.Left, header, content, composer, footer)
}

func (m *Model) renderHeader() string {
	title := "New chat"
	if conversation := m.activeConversation(); conversation != nil {
		title = conversationTitle(conversation.Summary)
	}
	left := lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).Render("Half-Pi") + "  " + title
	right := string(m.state)
	if m.state == stateReady {
		right = lipgloss.NewStyle().Foreground(colorSuccess).Render("ready")
	}
	space := max(1, m.width-ansi.StringWidth(left)-ansi.StringWidth(right)-2)
	return fitLine(left+strings.Repeat(" ", space)+right, m.width)
}

func (m *Model) renderContent() string {
	if m.modal != nil {
		return renderPanel("Approval", m.renderApprovalModal(), m.layout.Overlay, true)
	}
	if m.overlay != overlayNone {
		switch m.overlay {
		case overlayConversations:
			return renderPanel("Conversations", m.renderConversations(true), m.layout.Overlay, true)
		case overlayActivity:
			return renderPanel("Activity", m.renderActivity(), m.layout.Overlay, true)
		case overlayPalette:
			return renderPanel("Command palette", m.renderCompletions(), m.layout.Overlay, true)
		case overlayHistory:
			return renderPanel("Input history", m.renderHistorySearch(), m.layout.Overlay, true)
		case overlayExit:
			return renderPanel("Exit Face", "Exit the full-screen Face?\n\n  Yes     No", m.layout.Overlay, true)
		}
	}
	if len(m.completion) > 0 {
		if m.layout.Mode == layoutCompact {
			return renderPanel("Completions", m.renderCompletions(), m.layout.Chat, true)
		}
	}
	chat := renderPanel("Chat", m.chatViewport.View(), m.layout.Chat, m.focus == focusChat || m.focus == focusComposer)
	if m.layout.Mode == layoutCompact {
		return chat
	}
	conversations := renderPanel("Conversations", m.renderConversations(false), m.layout.Conversations, m.focus == focusConversations)
	if m.layout.Mode == layoutStandard {
		return lipgloss.JoinHorizontal(lipgloss.Top, conversations, chat)
	}
	activity := renderPanel("Activity", m.renderActivity(), m.layout.Activity, m.focus == focusActivity)
	return lipgloss.JoinHorizontal(lipgloss.Top, conversations, chat, activity)
}

func (m *Model) renderComposer() string {
	action := "Send"
	actionStyle := lipgloss.NewStyle().Bold(true).Foreground(colorSuccess)
	if m.hasActiveChat() {
		action = "Cancel"
		actionStyle = actionStyle.Foreground(colorDanger)
	} else if m.state != stateReady {
		action = "Waiting"
		actionStyle = actionStyle.Foreground(colorMuted)
	} else if strings.HasPrefix(strings.TrimLeft(m.composer.Value(), " \t\n"), "/") {
		action = "Run"
	} else if !m.hasScope(protocol.FaceScopeChat) ||
		m.activeID == "" && (!m.hasScope(protocol.FaceScopeSessionsRead) || !m.hasScope(protocol.FaceScopeSessionsWrite)) {
		action = "No access"
		actionStyle = actionStyle.Foreground(colorMuted)
	}
	innerWidth := max(1, m.layout.Composer.W-2)
	label := lipgloss.NewStyle().Foreground(colorMuted).Render("Compose")
	button := actionStyle.Render(action)
	line := label + strings.Repeat(" ", max(1, innerWidth-ansi.StringWidth(label)-ansi.StringWidth(button))) + button
	body := line + "\n" + m.composer.View()
	return renderPanel("", body, m.layout.Composer, m.focus == focusComposer)
}

func (m *Model) renderFooter() string {
	status := sanitizeRemoteText(m.status)
	if status == "" {
		status = string(m.state)
	}
	conversation := m.activeConversation()
	if conversation != nil && conversation.Notice != "" {
		status = conversation.Notice
	}
	left := status
	right := "normal"
	if conversation != nil {
		if conversation.Summary.Mode != "" {
			right = conversation.Summary.Mode
		}
		if conversation.Summary.ActiveHand != "" {
			right += "  Hand: " + conversation.Summary.ActiveHand
		}
		if conversation.NewContent {
			right += "  New content"
		}
	}
	space := max(1, m.width-ansi.StringWidth(left)-ansi.StringWidth(right)-2)
	return lipgloss.NewStyle().Foreground(colorMuted).Render(fitLine(left+strings.Repeat(" ", space)+right, m.width))
}

func (m *Model) renderConversations(overlay bool) string {
	if m.capabilitiesKnown && !m.hasScope("face:sessions:read") {
		return "Conversation access is unavailable."
	}
	selected := m.selectedConversation
	if overlay {
		selected = m.selectedOverlay
	}
	lines := make([]string, 0)
	ids := m.conversationOrder
	if overlay {
		lines = append(lines, "Search: "+m.pickerQuery, "")
		ids = m.filteredConversationIDs()
	}
	lines = append(lines, selectableLine("+ New chat", selected == 0))
	for index, id := range ids {
		conversation := m.conversations[id]
		if conversation == nil {
			continue
		}
		label := conversationTitle(conversation.Summary)
		if id == m.activeID {
			label = "* " + label
		}
		for _, chat := range conversation.Chats {
			if !chat.Terminal {
				label += " [active]"
				break
			}
		}
		if len(conversation.Approvals) > 0 {
			label += " [approval]"
		}
		lines = append(lines, selectableLine(label, selected == index+1))
	}
	if len(ids) == 0 && m.state == stateSynchronizing {
		lines = append(lines, "Loading...")
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderActivity() string {
	tabs := m.availableActivityTabs()
	if len(tabs) == 0 {
		return "No activity permissions."
	}
	m.normalizeActivityTab()
	var tabLabels []string
	for _, tab := range tabs {
		label := activityTabName(tab)
		if tab == m.activityTab {
			label = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("[" + label + "]")
		}
		tabLabels = append(tabLabels, label)
	}
	lines := []string{strings.Join(tabLabels, " "), ""}
	ids := m.activityIDs()
	conversation := m.activeConversation()
	for index, id := range ids {
		line := id
		switch m.activityTab {
		case tabApprovals:
			approval := conversation.Approvals[id]
			line = "pending  " + approval.Tool
		case tabRuns:
			run := conversation.Runs[id]
			line = string(run.Status) + "  " + run.Tool
			if output := conversation.RunOutput[id]; output != nil && output.Gap {
				line += "  [gap]"
			}
		case tabTasks:
			task := conversation.Tasks[id]
			line = string(task.Status) + "  " + task.Tool
			if task.Stale {
				line += "  [stale]"
			}
			if task.Truncated {
				line += "  [truncated]"
			}
		case tabHands:
			hand := m.hands[id]
			state := "offline"
			if hand.Connected {
				state = "online"
			}
			line = state + "  " + hand.Hostname
		}
		lines = append(lines, selectableLine(line, index == m.selectedActivity))
	}
	if len(ids) == 0 {
		lines = append(lines, "No items")
	}
	if len(ids) > 0 && m.selectedActivity < len(ids) {
		lines = append(lines, "", m.renderActivityDetail(ids[m.selectedActivity]))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderActivityDetail(id string) string {
	conversation := m.activeConversation()
	switch m.activityTab {
	case tabApprovals:
		approval := conversation.Approvals[id]
		return fmt.Sprintf("Tool: %s\nReason: %s\nExpires: %s", approval.Tool, approval.Reason, approval.ExpiresAt.Local().Format(time.DateTime))
	case tabRuns:
		output := conversation.RunOutput[id]
		if output == nil || !m.hasScope("face:runs:output") {
			return "No foreground output"
		}
		var lines []string
		if output.Gap {
			lines = append(lines, "Output may be incomplete")
		}
		for _, chunk := range output.Chunks {
			lines = append(lines, string(chunk.Kind)+": "+chunk.Data)
		}
		return strings.Join(lines, "\n")
	case tabTasks:
		log := conversation.TaskLogs[id]
		if log == nil {
			return "Output not loaded"
		}
		markers := ""
		if log.Truncated {
			markers += "[truncated] "
		}
		if log.EOF {
			markers += "[terminal]"
		}
		return strings.TrimSpace(markers + "\n" + log.Data)
	case tabHands:
		hand := m.hands[id]
		return fmt.Sprintf("%s/%s\nTools: %d", hand.OS, hand.Arch, len(hand.Tools))
	default:
		return ""
	}
}

func (m *Model) renderApprovalModal() string {
	approval := m.modal.Approval
	choices := []string{"Deny once", "Allow once", "Deny session", "Allow session"}
	for index := range choices {
		choices[index] = selectableLine(choices[index], index == m.modal.Choice)
	}
	state := ""
	if m.modal.Resolving {
		state = "\n\nResolving..."
	}
	return fmt.Sprintf("Tool: %s\nReason: %s\nArguments: %s\nExpires: %s\n\n%s%s",
		approval.Tool, approval.Reason, approval.ArgsDigest, approval.ExpiresAt.Local().Format(time.DateTime), strings.Join(choices, "   "), state)
}

func (m *Model) renderCompletions() string {
	if len(m.completion) == 0 {
		return "No matching commands"
	}
	lines := make([]string, 0, len(m.completion))
	for index, completion := range m.completion {
		line := completion.Label
		if completion.Description != "" {
			line += "  " + lipgloss.NewStyle().Foreground(colorMuted).Render(completion.Description)
		}
		lines = append(lines, selectableLine(line, index == m.completionIndex))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderHistorySearch() string {
	if len(m.completion) == 0 {
		return "Search: " + m.historyQuery + "\n\nNo input history"
	}
	lines := []string{"Search: " + m.historyQuery, ""}
	for index, item := range m.completion {
		lines = append(lines, selectableLine(strings.ReplaceAll(item.Label, "\n", " "), index == m.selectedOverlay))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) refreshViewport(newContent bool) {
	conversation := m.activeConversation()
	if conversation == nil || m.layout.TooSmall || m.chatViewport.Width <= 0 {
		return
	}
	wasBottom := conversation.AtBottom || m.chatViewport.AtBottom()
	yOffset := m.chatViewport.YOffset
	m.chatViewport.SetContent(m.renderChatContent(conversation, m.chatViewport.Width))
	if wasBottom {
		m.chatViewport.GotoBottom()
		conversation.AtBottom = true
		conversation.NewContent = false
	} else {
		m.chatViewport.SetYOffset(yOffset)
		if newContent {
			conversation.NewContent = true
		}
	}
}

func (m *Model) renderChatContent(conversation *conversationState, width int) string {
	var sections []string
	for _, message := range conversation.sortedMessages() {
		label := strings.ToUpper(message.Role)
		content := message.Content
		if message.Role == "assistant" {
			content = m.renderPersistedMarkdown(conversation, message.Seq, content, width)
		} else {
			content = ansi.Wrap(content, width, " \t-")
		}
		sections = append(sections, roleLabel(label)+"\n"+content)
	}
	seen := make(map[string]struct{}, len(conversation.ChatOrder))
	for _, requestID := range conversation.ChatOrder {
		if _, ok := seen[requestID]; ok {
			continue
		}
		seen[requestID] = struct{}{}
		chat := conversation.Chats[requestID]
		if chat == nil {
			continue
		}
		if chat.UserText != "" {
			state := ""
			if chat.Sending {
				state = "  sending"
			}
			sections = append(sections, roleLabel("YOU")+state+"\n"+ansi.Wrap(chat.UserText, width, " \t-"))
		}
		indexes := make([]int, 0, len(chat.Responses))
		for index := range chat.Responses {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			response := chat.Responses[index]
			content := response.Content
			if response.Complete || chat.Terminal {
				content = renderResponseMarkdown(response, width)
			} else {
				content = ansi.Wrap(content, width, " \t-")
			}
			sections = append(sections, roleLabel("HALF-PI")+"\n"+content)
			for _, tool := range chat.Tools {
				if tool.AfterResponse == index {
					sections = append(sections, renderTool(tool))
				}
			}
		}
		for _, tool := range chat.Tools {
			if tool.AfterResponse == 0 {
				sections = append(sections, renderTool(tool))
			}
		}
		if chat.Recovering {
			sections = append(sections, lipgloss.NewStyle().Foreground(colorWarning).Render("Recovering response..."))
		}
		if chat.Error != "" {
			sections = append(sections, lipgloss.NewStyle().Foreground(colorDanger).Render(chat.Error))
		}
	}
	if conversation.Notice != "" {
		sections = append(sections, lipgloss.NewStyle().Foreground(colorWarning).Render(conversation.Notice))
	}
	if len(sections) == 0 {
		return lipgloss.NewStyle().Foreground(colorMuted).Render("New conversation")
	}
	return strings.Join(sections, "\n\n")
}

func (m *Model) renderPersistedMarkdown(conversation *conversationState, seq int, content string, width int) string {
	cache := conversation.RenderedMessages[seq]
	if cache.Content == content && cache.Width == width {
		return cache.Value
	}
	value := renderMarkdown(content, width)
	conversation.RenderedMessages[seq] = renderedMessage{Content: content, Width: width, Value: value}
	return value
}

func renderResponseMarkdown(response *responseState, width int) string {
	if response.Rendered != "" && response.RenderWidth == width {
		return response.Rendered
	}
	response.Rendered = renderMarkdown(response.Content, width)
	response.RenderWidth = width
	return response.Rendered
}

func renderMarkdown(content string, width int) string {
	renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(max(20, width)))
	if err != nil {
		return ansi.Wrap(content, width, " \t-")
	}
	result, err := renderer.Render(content)
	if err != nil {
		return ansi.Wrap(content, width, " \t-")
	}
	return strings.TrimRight(result, "\n")
}

func renderTool(tool toolActivity) string {
	state := "running"
	style := lipgloss.NewStyle().Foreground(colorWarning)
	if tool.Complete && tool.Success {
		state = "succeeded"
		style = style.Foreground(colorSuccess)
	} else if tool.Complete {
		state = "failed"
		style = style.Foreground(colorDanger)
	}
	return roleLabel("TOOL") + "  " + tool.Tool + "  " + style.Render(state)
}

func roleLabel(value string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).Render(value)
}

func selectableLine(value string, selected bool) string {
	if selected {
		return lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).Render("> " + value)
	}
	return "  " + value
}

func renderPanel(title, content string, area rect, focused bool) string {
	innerWidth, innerHeight := max(1, area.W-2), max(1, area.H-2)
	if title != "" {
		titleStyle := lipgloss.NewStyle().Bold(true).Foreground(colorMuted)
		if focused {
			titleStyle = titleStyle.Foreground(colorPrimary)
		}
		content = titleStyle.Render(title) + "\n" + content
	}
	content = clipContent(content, innerWidth, innerHeight)
	borderColor := colorBorder
	if focused {
		borderColor = colorPrimary
	}
	rendered := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(borderColor).
		Width(innerWidth).Height(innerHeight).Render(content)
	return fitBlock(rendered, area.W, area.H)
}

func fitBlock(value string, width, height int) string {
	lines := strings.Split(value, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for index := range lines {
		lines[index] = fitLine(lines[index], width)
	}
	return strings.Join(lines, "\n")
}

func clipContent(content string, width, height int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for index := range lines {
		lines[index] = fitLine(lines[index], width)
	}
	return strings.Join(lines, "\n")
}

func fitLine(value string, width int) string {
	value = ansi.Truncate(value, width, "")
	padding := width - ansi.StringWidth(value)
	if padding > 0 {
		value += strings.Repeat(" ", padding)
	}
	return value
}
