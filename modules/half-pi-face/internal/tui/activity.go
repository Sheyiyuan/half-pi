package tui

import (
	"fmt"
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func (m *Model) availableActivityTabs() []activityTab {
	var result []activityTab
	if m.hasScope(protocol.FaceScopeApprove) {
		result = append(result, tabApprovals)
	}
	if m.hasScope(protocol.FaceScopeRunsRead) {
		result = append(result, tabRuns)
	}
	if m.hasScope(protocol.FaceScopeTasksRead) {
		result = append(result, tabTasks)
	}
	if m.hasScope(protocol.FaceScopeHandsRead) {
		result = append(result, tabHands)
	}
	return result
}

func (m *Model) activityTabAvailable(tab activityTab) bool {
	for _, available := range m.availableActivityTabs() {
		if available == tab {
			return true
		}
	}
	return false
}

func (m *Model) moveActivityTab(forward bool) {
	tabs := m.availableActivityTabs()
	if len(tabs) == 0 {
		return
	}
	index := 0
	for i, tab := range tabs {
		if tab == m.activityTab {
			index = i
			break
		}
	}
	if forward {
		index = (index + 1) % len(tabs)
	} else {
		index = (index - 1 + len(tabs)) % len(tabs)
	}
	m.activityTab = tabs[index]
	m.selectedActivity = 0
}

func (m *Model) normalizeActivityTab() {
	if m.activityTabAvailable(m.activityTab) {
		return
	}
	tabs := m.availableActivityTabs()
	if len(tabs) > 0 {
		m.activityTab = tabs[0]
	}
}

func (m *Model) selectActivityTabAt(column int) {
	for _, tab := range m.availableActivityTabs() {
		width := len(activityTabName(tab)) + 3
		if column < width {
			m.activityTab = tab
			m.selectedActivity = 0
			return
		}
		column -= width
	}
}

func (m *Model) activityIDs() []string {
	m.normalizeActivityTab()
	conversation := m.activeConversation()
	var result []string
	switch m.activityTab {
	case tabApprovals:
		if conversation != nil {
			for id := range conversation.Approvals {
				result = append(result, id)
			}
		}
	case tabRuns:
		if conversation != nil {
			for id := range conversation.Runs {
				result = append(result, id)
			}
		}
	case tabTasks:
		if conversation != nil {
			for id := range conversation.Tasks {
				result = append(result, id)
			}
		}
	case tabHands:
		for id := range m.hands {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func (m *Model) activityItemCount() int { return len(m.activityIDs()) }

func (m *Model) activateActivitySelection() (tea.Cmd, error) {
	ids := m.activityIDs()
	if len(ids) == 0 {
		return nil, nil
	}
	if m.selectedActivity >= len(ids) {
		m.selectedActivity = len(ids) - 1
	}
	id := ids[m.selectedActivity]
	conversation := m.activeConversation()
	switch m.activityTab {
	case tabApprovals:
		approval := conversation.Approvals[id]
		m.modal = &approvalModal{Approval: approval, Choice: 0}
		m.focus = focusModal
		return nil, nil
	case tabRuns:
		return m.requestRun(id)
	case tabTasks:
		log := conversation.TaskLogs[id]
		if log == nil {
			return m.requestTaskLog(id, 0, 64<<10)
		}
		if log.Loading || log.EOF {
			return nil, nil
		}
		return m.requestTaskLog(id, log.Offset, 64<<10)
	case tabHands:
		return m.requestHand(id)
	default:
		return nil, fmt.Errorf("activity tab is unavailable")
	}
}

func (m *Model) cancelActivitySelection() (tea.Cmd, error) {
	ids := m.activityIDs()
	if len(ids) == 0 {
		return nil, nil
	}
	if m.selectedActivity >= len(ids) {
		m.selectedActivity = len(ids) - 1
	}
	switch m.activityTab {
	case tabRuns:
		return m.cancelRun(ids[m.selectedActivity])
	case tabTasks:
		return m.cancelTask(ids[m.selectedActivity])
	default:
		return nil, fmt.Errorf("selected activity cannot be cancelled")
	}
}

func activityTabName(tab activityTab) string {
	switch tab {
	case tabApprovals:
		return "Approvals"
	case tabRuns:
		return "Runs"
	case tabTasks:
		return "Tasks"
	case tabHands:
		return "Hands"
	default:
		return "Activity"
	}
}
