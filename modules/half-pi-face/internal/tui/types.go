package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-face/internal/client"
)

type connectionState string

const (
	stateConnecting     connectionState = "connecting"
	stateAuthenticating connectionState = "authenticating"
	stateSynchronizing  connectionState = "synchronizing"
	stateReady          connectionState = "ready"
	stateOffline        connectionState = "offline"
)

type focusTarget int

const (
	focusConversations focusTarget = iota
	focusChat
	focusActivity
	focusComposer
	focusOverlay
	focusModal
)

type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayConversations
	overlayActivity
	overlayPalette
	overlayHistory
	overlayExit
)

type activityTab int

const (
	tabApprovals activityTab = iota
	tabRuns
	tabTasks
	tabHands
)

type responseState struct {
	Index       int
	Content     string
	NextOffset  int64
	Complete    bool
	Rendered    string
	RenderWidth int
}

type toolActivity struct {
	Tool          string
	ArgsDigest    string
	AfterResponse int
	Success       bool
	Complete      bool
}

type chatState struct {
	RequestID  string
	UserText   string
	Sending    bool
	Accepted   bool
	Responses  map[int]*responseState
	Tools      []toolActivity
	LastSeq    int64
	Buffered   map[int64]protocol.FaceChatDelta
	Recovering bool
	Terminal   bool
	Status     protocol.FaceResultStatus
	Error      string
}

type runOutput struct {
	LastSeq int64
	Gap     bool
	Chunks  []protocol.FaceRunProgress
	Bytes   int
}

type taskLog struct {
	Offset    int64
	Data      string
	EOF       bool
	Truncated bool
	Loading   bool
}

type conversationState struct {
	Summary          protocol.ConversationSummary
	Messages         map[int]protocol.FaceMessage
	Chats            map[string]*chatState
	ChatOrder        []string
	Approvals        map[string]protocol.ApprovalSummary
	Runs             map[string]protocol.RemoteRunSummary
	RunOutput        map[string]*runOutput
	Tasks            map[string]protocol.TaskSummary
	TaskLogs         map[string]*taskLog
	RenderedMessages map[int]renderedMessage
	Draft            string
	History          inputHistory
	HasMore          bool
	NextBeforeSeq    int
	Paging           bool
	ScrollOffset     int
	AtBottom         bool
	NewContent       bool
	Notice           string
	CompactStatus    protocol.FaceCompactStatus
	CompactKnown     bool
}

func newConversation(id string) *conversationState {
	return &conversationState{
		Summary:  protocol.ConversationSummary{ConversationID: id},
		Messages: make(map[int]protocol.FaceMessage), Chats: make(map[string]*chatState),
		Approvals: make(map[string]protocol.ApprovalSummary), Runs: make(map[string]protocol.RemoteRunSummary),
		RunOutput: make(map[string]*runOutput), Tasks: make(map[string]protocol.TaskSummary),
		TaskLogs: make(map[string]*taskLog), RenderedMessages: make(map[int]renderedMessage),
		History: newInputHistory(), AtBottom: true,
	}
}

type renderedMessage struct {
	Content string
	Width   int
	Value   string
}

func (c *conversationState) sortedMessages() []protocol.FaceMessage {
	result := make([]protocol.FaceMessage, 0, len(c.Messages))
	for _, message := range c.Messages {
		result = append(result, message)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Seq < result[j].Seq })
	return result
}

type pendingRequest struct {
	Operation      protocol.FaceOperation
	ConversationID string
	TargetID       string
	Mutation       bool
	Sent           bool
}

type sendFlow struct {
	Text           string
	ChatRequestID  string
	ConversationID string
}

type approvalModal struct {
	Approval  protocol.ApprovalSummary
	Choice    int
	Resolving bool
}

// Model 是 Half-Pi Face 的单一 Bubble Tea 状态模型。
type Model struct {
	ctx            context.Context
	connector      client.Connector
	conn           client.Connection
	generation     uint64
	state          connectionState
	permanentError bool
	retryAttempt   int
	retryAt        time.Time
	status         string

	width                int
	height               int
	layout               layout
	focus                focusTarget
	overlay              overlayKind
	overlayDraft         string
	modal                *approvalModal
	activityTab          activityTab
	selectedConversation int
	selectedOverlay      int
	selectedActivity     int

	composer          textarea.Model
	chatViewport      viewport.Model
	conversations     map[string]*conversationState
	conversationOrder []string
	activeID          string
	localDraft        *conversationState
	hands             map[string]protocol.HandSummary

	pending            map[string]pendingRequest
	outgoingChats      map[string]*sendFlow
	flow               *sendFlow
	features           map[protocol.FaceFeature]struct{}
	scopes             map[protocol.FaceScope]struct{}
	capabilitiesKnown  bool
	legacyCapabilities bool
	capabilityFallback bool
	limits             protocol.FaceProtocolLimits
	syncCapabilities   bool
	syncConversations  bool
	compactRequests    map[string]protocol.FaceConversationCompact

	completion      []Completion
	completionIndex int
	historyQuery    string
	pickerQuery     string
	commands        *CommandRegistry
	idSource        func() (string, error)
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
