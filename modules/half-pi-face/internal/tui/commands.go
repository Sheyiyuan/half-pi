package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// Completion 是 composer 和 command palette 共用的候选项。
type Completion struct {
	Label       string
	Description string
	Insert      string
}

// ArgumentSpec 描述命令参数及其动态补全来源。
type ArgumentSpec struct {
	Name     string
	Required bool
	Variadic bool
	Complete func(*Model, string) []Completion
}

// ParsedCommand 是完成引号解析后的命令调用。
type ParsedCommand struct {
	Spec *CommandSpec
	Args []string
}

// CommandHandler 执行一条已验证的命令。
type CommandHandler func(*Model, ParsedCommand) (tea.Cmd, error)

// CommandSpec 是解析、帮助、补全和执行共用的命令元数据。
type CommandSpec struct {
	Name        string
	Aliases     []string
	Description string
	Args        []ArgumentSpec
	Scopes      []protocol.FaceScope
	Visible     func(*Model) bool
	Execute     CommandHandler
}

// CommandRegistry 保存全部 typed command metadata。
type CommandRegistry struct {
	specs  []*CommandSpec
	byName map[string]*CommandSpec
}

// NewCommandRegistry 创建默认 Face 命令注册表。
func NewCommandRegistry() *CommandRegistry {
	registry := &CommandRegistry{byName: make(map[string]*CommandSpec)}
	registry.register(defaultCommandSpecs()...)
	return registry
}

func (r *CommandRegistry) register(specs ...CommandSpec) {
	for index := range specs {
		spec := &specs[index]
		r.specs = append(r.specs, spec)
		r.byName[spec.Name] = spec
		for _, alias := range spec.Aliases {
			r.byName[alias] = spec
		}
	}
	sort.Slice(r.specs, func(i, j int) bool { return r.specs[i].Name < r.specs[j].Name })
}

// Parse 解析并验证一条 slash command。
func (r *CommandRegistry) Parse(input string) (ParsedCommand, error) {
	words, err := splitCommandLine(strings.TrimSpace(input))
	if err != nil {
		return ParsedCommand{}, err
	}
	if len(words) == 0 || !strings.HasPrefix(words[0], "/") {
		return ParsedCommand{}, fmt.Errorf("command must start with /")
	}
	spec := r.byName[strings.TrimPrefix(words[0], "/")]
	if spec == nil {
		return ParsedCommand{}, fmt.Errorf("unknown command %q", words[0])
	}
	args := words[1:]
	minArgs, maxArgs := 0, len(spec.Args)
	for _, argument := range spec.Args {
		if argument.Required {
			minArgs++
		}
		if argument.Variadic {
			maxArgs = int(^uint(0) >> 1)
		}
	}
	if len(args) < minArgs || len(args) > maxArgs {
		return ParsedCommand{}, fmt.Errorf("usage: %s", commandUsage(spec))
	}
	return ParsedCommand{Spec: spec, Args: args}, nil
}

// Complete 返回当前输入上下文中的命令或参数候选。
func (r *CommandRegistry) Complete(model *Model, input string) []Completion {
	trimmed := strings.TrimLeftFunc(input, unicode.IsSpace)
	if !strings.HasPrefix(trimmed, "/") {
		return nil
	}
	words, err := splitCommandLine(trimmed)
	if err != nil {
		return nil
	}
	endsWithSpace := len(trimmed) > 0 && unicode.IsSpace(rune(trimmed[len(trimmed)-1]))
	if len(words) <= 1 && !endsWithSpace {
		query := strings.TrimPrefix(trimmed, "/")
		result := make([]Completion, 0, len(r.specs))
		for _, spec := range r.specs {
			if commandAvailable(model, spec) && fuzzyMatch(spec.Name, query) {
				result = append(result, Completion{Label: "/" + spec.Name, Description: spec.Description, Insert: "/" + spec.Name + completionSuffix(spec)})
			}
		}
		return result
	}
	name := strings.TrimPrefix(words[0], "/")
	spec := r.byName[name]
	if spec == nil {
		return nil
	}
	argIndex := len(words) - 2
	prefix := ""
	if endsWithSpace {
		argIndex++
	} else if len(words) > 1 {
		prefix = words[len(words)-1]
	}
	if argIndex < 0 || argIndex >= len(spec.Args) || spec.Args[argIndex].Complete == nil {
		return nil
	}
	return spec.Args[argIndex].Complete(model, prefix)
}

func (r *CommandRegistry) palette(model *Model, query string) []Completion {
	query = strings.TrimSpace(strings.TrimPrefix(query, "/"))
	result := make([]Completion, 0, len(r.specs))
	for _, spec := range r.specs {
		if commandAvailable(model, spec) && fuzzyMatch(spec.Name+" "+spec.Description, query) {
			result = append(result, Completion{Label: "/" + spec.Name, Description: spec.Description, Insert: "/" + spec.Name + completionSuffix(spec)})
		}
	}
	return result
}

func commandAvailable(model *Model, spec *CommandSpec) bool {
	if model == nil || spec == nil {
		return false
	}
	for _, scope := range spec.Scopes {
		if !model.hasScope(scope) {
			return false
		}
	}
	return spec.Visible == nil || spec.Visible(model)
}

func splitCommandLine(value string) ([]string, error) {
	var words []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	for _, r := range value {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		current.WriteRune(r)
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("unterminated command quote or escape")
	}
	flush()
	return words, nil
}

func commandUsage(spec *CommandSpec) string {
	result := "/" + spec.Name
	for _, argument := range spec.Args {
		name := argument.Name
		if argument.Variadic {
			name += "..."
		}
		if argument.Required {
			result += " <" + name + ">"
		} else {
			result += " [" + name + "]"
		}
	}
	return result
}

func completionSuffix(spec *CommandSpec) string {
	if len(spec.Args) > 0 {
		return " "
	}
	return ""
}

func fuzzyMatch(value, query string) bool {
	value, query = strings.ToLower(value), strings.ToLower(query)
	if strings.Contains(value, query) {
		return true
	}
	queryRunes := []rune(query)
	index := 0
	for _, r := range value {
		if index < len(queryRunes) && r == queryRunes[index] {
			index++
		}
	}
	return index == len(queryRunes)
}

func completeConversations(model *Model, prefix string) []Completion {
	var result []Completion
	for _, id := range model.conversationOrder {
		conversation := model.conversations[id]
		if conversation == nil {
			continue
		}
		name := conversation.Summary.Name
		if fuzzyMatch(name+" "+id, prefix) {
			result = append(result, Completion{Label: conversationTitle(conversation.Summary), Description: id, Insert: id})
		}
	}
	return result
}

func completeHands(model *Model, prefix string) []Completion {
	var result []Completion
	for id, hand := range model.hands {
		if fuzzyMatch(id+" "+hand.Hostname, prefix) {
			result = append(result, Completion{Label: hand.Hostname, Description: id, Insert: id})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Label < result[j].Label })
	return result
}

func completeApprovals(model *Model, prefix string) []Completion {
	conversation := model.activeConversation()
	if conversation == nil {
		return nil
	}
	var result []Completion
	for id, approval := range conversation.Approvals {
		if fuzzyMatch(id+" "+approval.Tool, prefix) {
			result = append(result, Completion{Label: approval.Tool, Description: id, Insert: id})
		}
	}
	return result
}

func completeRuns(model *Model, prefix string) []Completion {
	conversation := model.activeConversation()
	if conversation == nil {
		return nil
	}
	var result []Completion
	for id, run := range conversation.Runs {
		if fuzzyMatch(id+" "+run.Tool, prefix) {
			result = append(result, Completion{Label: run.Tool, Description: string(run.Status), Insert: id})
		}
	}
	return result
}

func completeTasks(model *Model, prefix string) []Completion {
	conversation := model.activeConversation()
	if conversation == nil {
		return nil
	}
	var result []Completion
	for id, task := range conversation.Tasks {
		if fuzzyMatch(id+" "+task.Tool, prefix) {
			result = append(result, Completion{Label: task.Tool, Description: string(task.Status), Insert: id})
		}
	}
	return result
}

func completeCurrentName(model *Model, prefix string) []Completion {
	conversation := model.activeConversation()
	if conversation == nil || !fuzzyMatch(conversation.Summary.Name, prefix) {
		return nil
	}
	return []Completion{{Label: conversationTitle(conversation.Summary), Insert: conversation.Summary.Name}}
}

func completeMessageCursor(model *Model, prefix string) []Completion {
	conversation := model.activeConversation()
	if conversation == nil || conversation.NextBeforeSeq <= 0 {
		return nil
	}
	value := strconv.Itoa(conversation.NextBeforeSeq)
	if !strings.HasPrefix(value, prefix) {
		return nil
	}
	return []Completion{{Label: value, Description: "oldest loaded message", Insert: value}}
}

func completeDecisions(_ *Model, prefix string) []Completion {
	decisions := []protocol.FaceApprovalDecision{
		protocol.FaceApprovalDenyOnce, protocol.FaceApprovalAllowOnce,
		protocol.FaceApprovalDenySession, protocol.FaceApprovalAllowSession,
	}
	var result []Completion
	for _, decision := range decisions {
		if strings.HasPrefix(string(decision), prefix) {
			result = append(result, Completion{Label: string(decision), Insert: string(decision)})
		}
	}
	return result
}

func defaultCommandSpecs() []CommandSpec {
	conversationArg := ArgumentSpec{Name: "conversation", Required: true, Complete: completeConversations}
	handArg := ArgumentSpec{Name: "hand", Required: true, Complete: completeHands}
	return []CommandSpec{
		{Name: "help", Description: "Open command palette", Execute: func(m *Model, _ ParsedCommand) (tea.Cmd, error) {
			m.clearComposer()
			m.openPalette()
			return nil, nil
		}},
		{Name: "quit", Aliases: []string{"exit"}, Description: "Exit Face", Execute: func(_ *Model, _ ParsedCommand) (tea.Cmd, error) { return tea.Quit, nil }},
		{Name: "list", Description: "Refresh conversations", Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead}, Execute: func(m *Model, _ ParsedCommand) (tea.Cmd, error) { return m.requestConversationList() }},
		{Name: "create", Description: "Create a named conversation", Args: []ArgumentSpec{{Name: "name", Required: true, Variadic: true}}, Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsWrite}, Execute: commandCreate},
		{Name: "open", Description: "Open a conversation", Args: []ArgumentSpec{conversationArg}, Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead}, Execute: commandOpen},
		{Name: "snapshot", Description: "Refresh the current conversation", Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead}, Execute: commandSnapshot},
		{Name: "messages", Description: "Load a message page", Args: []ArgumentSpec{{Name: "before_seq", Complete: completeMessageCursor}, {Name: "limit"}}, Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead}, Execute: commandMessages},
		{Name: "rename", Description: "Rename the current conversation", Args: []ArgumentSpec{{Name: "name", Required: true, Variadic: true, Complete: completeCurrentName}}, Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsWrite}, Execute: commandRename},
		{Name: "cancel", Description: "Cancel the active Chat", Args: []ArgumentSpec{{Name: "request_id"}}, Scopes: []protocol.FaceScope{protocol.FaceScopeChat}, Execute: commandCancelChat},
		{Name: "approve", Description: "Resolve an approval", Args: []ArgumentSpec{{Name: "approval", Required: true, Complete: completeApprovals}, {Name: "decision", Required: true, Complete: completeDecisions}, {Name: "reason", Variadic: true}}, Scopes: []protocol.FaceScope{protocol.FaceScopeApprove}, Execute: commandApprove},
		{Name: "hands", Description: "Refresh Hands", Scopes: []protocol.FaceScope{protocol.FaceScopeHandsRead}, Execute: func(m *Model, _ ParsedCommand) (tea.Cmd, error) { return m.requestHands() }},
		{Name: "activity", Description: "Open the activity workspace", Execute: func(m *Model, _ ParsedCommand) (tea.Cmd, error) {
			m.overlay, m.focus = overlayActivity, focusOverlay
			m.normalizeActivityTab()
			return nil, nil
		}, Visible: func(m *Model) bool { return len(m.availableActivityTabs()) > 0 }},
		{Name: "hand", Description: "Inspect a Hand", Args: []ArgumentSpec{handArg}, Scopes: []protocol.FaceScope{protocol.FaceScopeHandsRead}, Execute: commandHand},
		{Name: "run", Description: "Inspect a foreground run", Args: []ArgumentSpec{{Name: "run", Required: true, Complete: completeRuns}}, Scopes: []protocol.FaceScope{protocol.FaceScopeRunsRead}, Execute: commandRun},
		{Name: "run-cancel", Description: "Cancel a foreground run", Args: []ArgumentSpec{{Name: "run", Required: true, Complete: completeRuns}}, Scopes: []protocol.FaceScope{protocol.FaceScopeRunsCancel}, Execute: commandRunCancel},
		{Name: "tasks", Description: "Refresh durable tasks", Scopes: []protocol.FaceScope{protocol.FaceScopeTasksRead}, Execute: func(m *Model, _ ParsedCommand) (tea.Cmd, error) { return m.requestTasks() }},
		{Name: "task", Description: "Inspect a durable task", Args: []ArgumentSpec{{Name: "task", Required: true, Complete: completeTasks}}, Scopes: []protocol.FaceScope{protocol.FaceScopeTasksRead}, Execute: commandTask},
		{Name: "task-log", Description: "Read durable task output", Args: []ArgumentSpec{{Name: "task", Required: true, Complete: completeTasks}, {Name: "offset"}, {Name: "limit"}}, Scopes: []protocol.FaceScope{protocol.FaceScopeTasksRead}, Execute: commandTaskLog},
		{Name: "task-cancel", Description: "Cancel a durable task", Args: []ArgumentSpec{{Name: "task", Required: true, Complete: completeTasks}}, Scopes: []protocol.FaceScope{protocol.FaceScopeTasksRead, protocol.FaceScopeTasksCancel}, Execute: commandTaskCancel},
	}
}

func commandCreate(m *Model, command ParsedCommand) (tea.Cmd, error) {
	return m.createNamedConversation(strings.Join(command.Args, " "))
}

func commandOpen(m *Model, command ParsedCommand) (tea.Cmd, error) {
	return m.openConversation(command.Args[0])
}

func commandSnapshot(m *Model, _ ParsedCommand) (tea.Cmd, error) {
	if m.activeID == "" {
		return nil, fmt.Errorf("no persisted conversation is open")
	}
	return m.requestSnapshot(m.activeID, "")
}

func commandMessages(m *Model, command ParsedCommand) (tea.Cmd, error) {
	before, limit := 0, 0
	var err error
	if len(command.Args) > 0 {
		before, err = strconv.Atoi(command.Args[0])
	}
	if err == nil && len(command.Args) > 1 {
		limit, err = strconv.Atoi(command.Args[1])
	}
	if err != nil {
		return nil, fmt.Errorf("message cursor and limit must be integers")
	}
	return m.requestMessages(before, limit)
}

func commandRename(m *Model, command ParsedCommand) (tea.Cmd, error) {
	return m.renameConversation(strings.Join(command.Args, " "))
}

func commandCancelChat(m *Model, command ParsedCommand) (tea.Cmd, error) {
	target := ""
	if len(command.Args) > 0 {
		target = command.Args[0]
	}
	return m.cancelChat(target)
}

func commandApprove(m *Model, command ParsedCommand) (tea.Cmd, error) {
	decision := protocol.FaceApprovalDecision(command.Args[1])
	reason := ""
	if len(command.Args) > 2 {
		reason = strings.Join(command.Args[2:], " ")
	}
	return m.resolveApproval(command.Args[0], decision, reason)
}

func commandHand(m *Model, command ParsedCommand) (tea.Cmd, error) {
	return m.requestHand(command.Args[0])
}
func commandRun(m *Model, command ParsedCommand) (tea.Cmd, error) {
	return m.requestRun(command.Args[0])
}
func commandRunCancel(m *Model, command ParsedCommand) (tea.Cmd, error) {
	return m.cancelRun(command.Args[0])
}
func commandTask(m *Model, command ParsedCommand) (tea.Cmd, error) {
	return m.requestTask(command.Args[0])
}
func commandTaskCancel(m *Model, command ParsedCommand) (tea.Cmd, error) {
	return m.cancelTask(command.Args[0])
}

func commandTaskLog(m *Model, command ParsedCommand) (tea.Cmd, error) {
	offset, limit := int64(0), 64<<10
	var err error
	if len(command.Args) > 1 {
		offset, err = strconv.ParseInt(command.Args[1], 10, 64)
	}
	if err == nil && len(command.Args) > 2 {
		limit, err = strconv.Atoi(command.Args[2])
	}
	if err != nil {
		return nil, fmt.Errorf("task log offset and limit must be integers")
	}
	return m.requestTaskLog(command.Args[0], offset, limit)
}
