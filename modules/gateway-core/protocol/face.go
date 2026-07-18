package protocol

import (
	"encoding/json"
	"time"
)

const (
	// FaceProtocolRevision 是注册后 Face 应用协议的当前修订号。
	FaceProtocolRevision = 2
	// DefaultFaceMessageListLimit 是会话消息分页的默认大小。
	DefaultFaceMessageListLimit = 100
	// MaxFaceMessageListLimit 是会话消息分页的最大大小。
	MaxFaceMessageListLimit = 500
	// MaxFaceChatContentBytes 限制单次 Face Chat 输入的 UTF-8 字节数。
	MaxFaceChatContentBytes = 256 << 10
	// MaxFaceChatDeltaBytes 限制单条 Chat 增量的 UTF-8 字节数。
	MaxFaceChatDeltaBytes = 4 << 10
	// MaxFaceChatStreamBytes 限制单个 Chat 可恢复文本的总字节数。
	MaxFaceChatStreamBytes = 2 << 20
	// MaxFaceChatStreamChunks 限制单个 Chat 的聚合增量数量。
	MaxFaceChatStreamChunks = 2048
	// DefaultFaceTaskListLimit 是 Face 任务列表的默认分页大小。
	DefaultFaceTaskListLimit = 50
	// MaxFaceTaskListLimit 是 Face 任务列表的最大分页大小。
	MaxFaceTaskListLimit = 200
	// DefaultFaceTaskHistoryLimit 是快照默认包含的终态任务数量。
	DefaultFaceTaskHistoryLimit = 50
	// MaxFaceTaskHistoryLimit 是快照可包含的终态任务硬上限。
	MaxFaceTaskHistoryLimit = 500
)

const (
	TypeFaceChat                 = "face.chat"
	TypeFaceChatCancel           = "face.chat.cancel"
	TypeFaceChatStreamGet        = "face.chat.stream.get"
	TypeFaceCapabilitiesGet      = "face.capabilities.get"
	TypeFaceConversationList     = "face.conversation.list"
	TypeFaceConversationCreate   = "face.conversation.create"
	TypeFaceConversationSnapshot = "face.conversation.snapshot"
	TypeFaceConversationRename   = "face.conversation.rename"
	TypeFaceConversationMessages = "face.conversation.messages"
	TypeFaceSubscribe            = "face.subscribe"
	TypeFaceApprovalResolve      = "face.approval.resolve"
	TypeFaceRunGet               = "face.run.get"
	TypeFaceRunCancel            = "face.run.cancel"
	TypeFaceHandList             = "face.hand.list"
	TypeFaceHandGet              = "face.hand.get"
	TypeFaceTaskList             = "face.task.list"
	TypeFaceTaskGet              = "face.task.get"
	TypeFaceTaskLog              = "face.task.log"
	TypeFaceTaskCancel           = "face.task.cancel"

	TypeFaceAccepted = "face.accepted"
	TypeFaceResult   = "face.result"
	TypeFaceError    = "face.error"
	TypeFaceSnapshot = "face.snapshot"
	TypeFaceEvent    = "face.event"

	TypeFaceChatDelta     = "face.chat.delta"
	TypeFaceChatStreamEnd = "face.chat.stream.end"
	TypeFaceRunProgress   = "face.run.progress"
)

// FaceScope 是 Face 身份可被授予的权限。
type FaceScope string

const (
	FaceScopeChat          FaceScope = "face:chat"
	FaceScopeSessionsRead  FaceScope = "face:sessions:read"
	FaceScopeSessionsWrite FaceScope = "face:sessions:write"
	FaceScopeRunsRead      FaceScope = "face:runs:read"
	FaceScopeRunsCancel    FaceScope = "face:runs:cancel"
	FaceScopeRunsOutput    FaceScope = "face:runs:output"
	FaceScopeApprove       FaceScope = "face:approve"
	FaceScopeHandsRead     FaceScope = "face:hands:read"
	FaceScopeTasksRead     FaceScope = "face:tasks:read"
	FaceScopeTasksCancel   FaceScope = "face:tasks:cancel"
)

// FaceFeature 是 Mind 声明支持的可选 Face 应用能力。
type FaceFeature string

const (
	FaceFeatureChatStream       FaceFeature = "chat_stream.v1"
	FaceFeatureChatStreamResume FaceFeature = "chat_stream_resume.v1"
	FaceFeatureRunProgress      FaceFeature = "run_progress.v1"
	FaceFeatureMessagePaging    FaceFeature = "message_pagination.v1"
)

// FaceTransientType 是必须显式订阅的非权威增量类型。
type FaceTransientType string

const (
	FaceTransientChatDelta   FaceTransientType = "chat.delta"
	FaceTransientRunProgress FaceTransientType = "run.progress"
)

// FaceIdentity 是通过鉴权的 Face 身份及其权限集合。
type FaceIdentity struct {
	ID     string      `json:"id"`
	Label  string      `json:"label"`
	Scopes []FaceScope `json:"scopes"`
}

// FaceErrorCode 是 Face 协议的稳定错误码。
type FaceErrorCode string

const (
	FaceErrorInvalidRequest       FaceErrorCode = "invalid_request"
	FaceErrorUnauthorized         FaceErrorCode = "unauthorized"
	FaceErrorForbidden            FaceErrorCode = "forbidden"
	FaceErrorConversationNotFound FaceErrorCode = "conversation_not_found"
	FaceErrorRequestConflict      FaceErrorCode = "request_conflict"
	FaceErrorRequestInProgress    FaceErrorCode = "request_in_progress"
	FaceErrorApprovalNotFound     FaceErrorCode = "approval_not_found"
	FaceErrorApprovalExpired      FaceErrorCode = "approval_expired"
	FaceErrorRunNotFound          FaceErrorCode = "run_not_found"
	FaceErrorHandNotFound         FaceErrorCode = "hand_not_found"
	FaceErrorTaskFailed           FaceErrorCode = "task_failed"
	FaceErrorTaskCancelled        FaceErrorCode = "task_cancelled"
	FaceErrorTaskTimedOut         FaceErrorCode = "task_timed_out"
	FaceErrorTaskLost             FaceErrorCode = "task_lost"
	FaceErrorTaskStale            FaceErrorCode = "task_stale"
	FaceErrorTaskNotFound         FaceErrorCode = "task_not_found"
	FaceErrorHandOffline          FaceErrorCode = "hand_offline"
	FaceErrorLogUnavailable       FaceErrorCode = "log_unavailable"
	FaceErrorBusy                 FaceErrorCode = "busy"
	FaceErrorCancelled            FaceErrorCode = "cancelled"
	FaceErrorTimeout              FaceErrorCode = "timeout"
	FaceErrorInternal             FaceErrorCode = "internal_error"
)

// FaceResultStatus 是已接受请求的终态。
type FaceResultStatus string

const (
	FaceResultSucceeded FaceResultStatus = "succeeded"
	FaceResultFailed    FaceResultStatus = "failed"
	FaceResultCancelled FaceResultStatus = "cancelled"
	FaceResultTimedOut  FaceResultStatus = "timed_out"
)

// FaceApprovalDecision 是 Face 对审批请求的裁决。
type FaceApprovalDecision string

const (
	// MaxFaceApprovalReasonBytes 限制审批请求和裁决原因的 UTF-8 字节数。
	MaxFaceApprovalReasonBytes = 1024

	FaceApprovalAllowOnce    FaceApprovalDecision = "allow_once"
	FaceApprovalDenyOnce     FaceApprovalDecision = "deny_once"
	FaceApprovalAllowSession FaceApprovalDecision = "allow_session"
	FaceApprovalDenySession  FaceApprovalDecision = "deny_session"
)

// FaceEventType 是可订阅的稳定业务事件类型。
type FaceEventType string

const (
	FaceEventChatStarted         FaceEventType = "chat.started"
	FaceEventChatToolCalled      FaceEventType = "chat.tool_called"
	FaceEventChatToolCompleted   FaceEventType = "chat.tool_completed"
	FaceEventChatCompleted       FaceEventType = "chat.completed"
	FaceEventChatFailed          FaceEventType = "chat.failed"
	FaceEventChatCancelled       FaceEventType = "chat.cancelled"
	FaceEventApprovalRequested   FaceEventType = "approval.requested"
	FaceEventApprovalResolved    FaceEventType = "approval.resolved"
	FaceEventRemoteRunChanged    FaceEventType = "remote_run.changed"
	FaceEventHandConnected       FaceEventType = "hand.connected"
	FaceEventHandDisconnected    FaceEventType = "hand.disconnected"
	FaceEventConversationChanged FaceEventType = "conversation.changed"
	FaceEventTaskChanged         FaceEventType = "task.changed"
)

// FaceEventLevel 是 Face 事件的严重级别。
type FaceEventLevel string

const (
	FaceEventLevelInfo  FaceEventLevel = "info"
	FaceEventLevelWarn  FaceEventLevel = "warn"
	FaceEventLevelError FaceEventLevel = "error"
)

// FaceOperation 标识 accepted 响应接受的操作。
type FaceOperation string

const (
	FaceOperationChat                 FaceOperation = "chat"
	FaceOperationChatCancel           FaceOperation = "chat.cancel"
	FaceOperationChatStreamGet        FaceOperation = "chat.stream.get"
	FaceOperationCapabilitiesGet      FaceOperation = "capabilities.get"
	FaceOperationConversationList     FaceOperation = "conversation.list"
	FaceOperationConversationCreate   FaceOperation = "conversation.create"
	FaceOperationConversationSnapshot FaceOperation = "conversation.snapshot"
	FaceOperationConversationRename   FaceOperation = "conversation.rename"
	FaceOperationConversationMessages FaceOperation = "conversation.messages"
	FaceOperationSubscribe            FaceOperation = "subscribe"
	FaceOperationApprovalResolve      FaceOperation = "approval.resolve"
	FaceOperationRunGet               FaceOperation = "run.get"
	FaceOperationRunCancel            FaceOperation = "run.cancel"
	FaceOperationHandList             FaceOperation = "hand.list"
	FaceOperationHandGet              FaceOperation = "hand.get"
	FaceOperationTaskList             FaceOperation = "task.list"
	FaceOperationTaskGet              FaceOperation = "task.get"
	FaceOperationTaskLog              FaceOperation = "task.log"
	FaceOperationTaskCancel           FaceOperation = "task.cancel"
)

// FaceCommandMeta 是所有 Face command 共用的关联字段。
type FaceCommandMeta struct {
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id,omitempty"`
}

// FaceChat 请求在指定对话中发起一轮 Chat。
type FaceChat struct {
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	Content        string `json:"content"`
}

// FaceChatCancel 请求取消指定 Chat。
type FaceChatCancel struct {
	RequestID       string `json:"request_id"`
	TargetRequestID string `json:"target_request_id"`
	ConversationID  string `json:"conversation_id"`
	Reason          string `json:"reason,omitempty"`
}

// FaceChatStreamGet 请求读取活动或短期保留 Chat 的可恢复文本前缀。
type FaceChatStreamGet struct {
	RequestID       string `json:"request_id"`
	ConversationID  string `json:"conversation_id"`
	TargetRequestID string `json:"target_request_id"`
}

// FaceCapabilitiesGet 请求当前 Face 身份、应用能力和协议限额。
type FaceCapabilitiesGet struct {
	RequestID string `json:"request_id"`
}

// FaceConversationList 请求列出可访问的对话。
type FaceConversationList struct {
	RequestID string `json:"request_id"`
}

// FaceConversationCreate 请求创建对话。
type FaceConversationCreate struct {
	RequestID string `json:"request_id"`
	Name      string `json:"name,omitempty"`
}

// FaceConversationSnapshot 请求指定对话的权威快照。
type FaceConversationSnapshot struct {
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
}

// FaceConversationRename 请求重命名指定对话。
type FaceConversationRename struct {
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	Name           string `json:"name"`
}

// FaceConversationMessages 请求按稳定 seq 向前分页读取会话消息。
type FaceConversationMessages struct {
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	BeforeSeq      int    `json:"before_seq,omitempty"`
	Limit          int    `json:"limit,omitempty"`
}

// FaceSubscribe 设置连接的增量事件订阅。
type FaceSubscribe struct {
	RequestID       string              `json:"request_id"`
	ConversationIDs []string            `json:"conversation_ids,omitempty"`
	EventTypes      []FaceEventType     `json:"event_types,omitempty"`
	TransientTypes  []FaceTransientType `json:"transient_types,omitempty"`
}

// FaceApprovalResolve 裁决一个待处理审批。
type FaceApprovalResolve struct {
	RequestID  string               `json:"request_id"`
	ApprovalID string               `json:"approval_id"`
	Decision   FaceApprovalDecision `json:"decision"`
	Reason     string               `json:"reason,omitempty"`
}

// FaceRunGet 请求读取指定远程执行。
type FaceRunGet struct {
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
}

// FaceRunCancel 请求取消指定远程执行。
type FaceRunCancel struct {
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
	Reason         string `json:"reason,omitempty"`
}

// FaceHandList 请求列出可见 Hand。
type FaceHandList struct {
	RequestID string `json:"request_id"`
}

// FaceHandGet 请求读取指定 Hand 的详情。
type FaceHandGet struct {
	RequestID string `json:"request_id"`
	HandID    string `json:"hand_id"`
}

// FaceTaskList 请求分页列出指定对话的后台任务。
type FaceTaskList struct {
	RequestID      string       `json:"request_id"`
	ConversationID string       `json:"conversation_id"`
	HandID         string       `json:"hand_id,omitempty"`
	Statuses       []TaskStatus `json:"statuses,omitempty"`
	Cursor         string       `json:"cursor,omitempty"`
	Limit          int          `json:"limit,omitempty"`
}

// FaceTaskGet 请求读取指定对话中的后台任务。
type FaceTaskGet struct {
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	TaskID         string `json:"task_id"`
}

// FaceTaskLog 请求从精确字节偏移读取后台任务日志。
type FaceTaskLog struct {
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	TaskID         string `json:"task_id"`
	Offset         int64  `json:"offset"`
	Limit          int    `json:"limit"`
}

// FaceTaskCancel 请求取消指定对话中的后台任务。
type FaceTaskCancel struct {
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id"`
	TaskID         string `json:"task_id"`
	Reason         string `json:"reason,omitempty"`
}

// FaceAccepted 表示 command 已进入处理流程。
type FaceAccepted struct {
	RequestID       string        `json:"request_id"`
	ConversationID  string        `json:"conversation_id,omitempty"`
	Operation       FaceOperation `json:"operation"`
	SnapshotVersion int64         `json:"snapshot_version,omitempty"`
}

// FaceResult 是已接受请求的最终结果。
type FaceResult struct {
	RequestID      string           `json:"request_id"`
	ConversationID string           `json:"conversation_id,omitempty"`
	Status         FaceResultStatus `json:"status"`
	Content        string           `json:"content,omitempty"`
	Data           json.RawMessage  `json:"data,omitempty"`
	ErrorCode      FaceErrorCode    `json:"error_code,omitempty"`
	Error          string           `json:"error,omitempty"`
}

// FaceError 是未接受请求的结构化错误。
type FaceError struct {
	RequestID      string        `json:"request_id,omitempty"`
	ConversationID string        `json:"conversation_id,omitempty"`
	Code           FaceErrorCode `json:"code"`
	Message        string        `json:"message"`
	Retryable      bool          `json:"retryable"`
}

// FaceSnapshot 携带一次对话快照响应。
type FaceSnapshot struct {
	RequestID string               `json:"request_id"`
	Snapshot  ConversationSnapshot `json:"snapshot"`
}

// FaceEvent 是 Mind 向 Face 投递的有序业务事件。
type FaceEvent struct {
	EventSeq       int64           `json:"event_seq"`
	ConversationID string          `json:"conversation_id,omitempty"`
	RequestID      string          `json:"request_id,omitempty"`
	Type           FaceEventType   `json:"type"`
	Source         string          `json:"source"`
	Level          FaceEventLevel  `json:"level"`
	Message        string          `json:"message"`
	Data           json.RawMessage `json:"data,omitempty"`
	Timestamp      time.Time       `json:"timestamp"`
}

// FaceChatDelta 是一次 Chat 中用户可见文本的有界增量。
type FaceChatDelta struct {
	ConversationID string `json:"conversation_id"`
	RequestID      string `json:"request_id"`
	ResponseIndex  int    `json:"response_index"`
	Seq            int64  `json:"seq"`
	Offset         int64  `json:"offset"`
	Delta          string `json:"delta"`
}

// FaceChatStreamEnd 是 Chat 瞬时流的可靠终止屏障。
type FaceChatStreamEnd struct {
	ConversationID string           `json:"conversation_id"`
	RequestID      string           `json:"request_id"`
	LastSeq        int64            `json:"last_seq"`
	ResponseCount  int              `json:"response_count"`
	Complete       bool             `json:"complete"`
	Status         FaceResultStatus `json:"status"`
}

// FaceRunProgress 是 foreground run 的有界 stdout/stderr 增量。
type FaceRunProgress struct {
	ConversationID string       `json:"conversation_id"`
	RequestID      string       `json:"request_id,omitempty"`
	RunID          string       `json:"run_id"`
	Seq            int64        `json:"seq"`
	Kind           ProgressKind `json:"kind"`
	Data           string       `json:"data"`
	Gap            bool         `json:"gap"`
}

// ConversationSummary 是对话列表中的稳定摘要。
type ConversationSummary struct {
	ConversationID string    `json:"conversation_id"`
	Name           string    `json:"name"`
	Mode           string    `json:"mode"`
	ActiveHand     string    `json:"active_hand,omitempty"`
	MessageCount   int       `json:"message_count"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// FaceMessage 是对话快照中的一条历史消息。
type FaceMessage struct {
	ID        int64     `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	RequestID string    `json:"request_id,omitempty"`
	ToolID    string    `json:"tool_id,omitempty"`
	Seq       int       `json:"seq"`
	CreatedAt time.Time `json:"created_at"`
}

// ChatSummary 是快照中的活动 Chat 摘要。
type ChatSummary struct {
	RequestID string    `json:"request_id"`
	StartedAt time.Time `json:"started_at"`
}

// ApprovalRequest 是等待 Face 裁决的审批请求。
type ApprovalRequest struct {
	ApprovalID     string    `json:"approval_id"`
	ConversationID string    `json:"conversation_id"`
	RequestID      string    `json:"request_id,omitempty"`
	RunID          string    `json:"run_id,omitempty"`
	Tool           string    `json:"tool"`
	Reason         string    `json:"reason"`
	ArgsDigest     string    `json:"args_digest"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// ApprovalSummary 是快照中的待处理审批摘要。
type ApprovalSummary = ApprovalRequest

// RemoteRunSummary 是 Face 可见的远程执行摘要。
type RemoteRunSummary struct {
	RunID      string     `json:"run_id"`
	RequestID  string     `json:"request_id"`
	HandID     string     `json:"hand_id"`
	Tool       string     `json:"tool"`
	Status     RunStatus  `json:"status"`
	DurationMs int64      `json:"duration_ms"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// HandSummary 是 Hand 列表和详情使用的设备摘要。
type HandSummary struct {
	HandID    string     `json:"hand_id"`
	Hostname  string     `json:"hostname"`
	OS        string     `json:"os"`
	Arch      string     `json:"arch"`
	WorkDir   string     `json:"work_dir,omitempty"`
	Connected bool       `json:"connected"`
	Tools     []ToolInfo `json:"tools,omitempty"`
}

// TaskSummary 是 Face 可见的后台任务完整摘要。
type TaskSummary struct {
	TaskID         string        `json:"task_id"`
	ConversationID string        `json:"conversation_id"`
	HandID         string        `json:"hand_id"`
	Tool           string        `json:"tool"`
	ArgsDigest     string        `json:"args_digest"`
	Status         TaskStatus    `json:"status"`
	CreatedAt      time.Time     `json:"created_at"`
	StartedAt      *time.Time    `json:"started_at,omitempty"`
	FinishedAt     *time.Time    `json:"finished_at,omitempty"`
	UpdatedAt      time.Time     `json:"updated_at"`
	LogBytes       int64         `json:"log_bytes"`
	Truncated      bool          `json:"truncated"`
	Stale          bool          `json:"stale"`
	ErrorCode      FaceErrorCode `json:"error_code,omitempty"`
	Error          string        `json:"error,omitempty"`
}

// ConversationSnapshot 是断线恢复所需的对话权威状态。
type ConversationSnapshot struct {
	ConversationID       string             `json:"conversation_id"`
	Name                 string             `json:"name"`
	Mode                 string             `json:"mode"`
	ActiveHand           string             `json:"active_hand,omitempty"`
	Messages             []FaceMessage      `json:"messages"`
	PendingChats         []ChatSummary      `json:"pending_chats"`
	PendingApprovals     []ApprovalSummary  `json:"pending_approvals"`
	ActiveRuns           []RemoteRunSummary `json:"active_runs"`
	Tasks                []TaskSummary      `json:"tasks"`
	TaskHistoryLimit     int                `json:"task_history_limit"`
	TaskHistoryTruncated bool               `json:"task_history_truncated"`
	SnapshotVersion      int64              `json:"snapshot_version"`
}

// ConversationListResult 是 conversation.list 的结构化结果。
type ConversationListResult struct {
	Conversations []ConversationSummary `json:"conversations"`
}

// ConversationCreateResult 是 conversation.create 的结构化结果。
type ConversationCreateResult struct {
	Conversation ConversationSummary `json:"conversation"`
}

// ConversationRenameResult 是 conversation.rename 的结构化结果。
type ConversationRenameResult struct {
	Conversation ConversationSummary `json:"conversation"`
}

// FaceProtocolLimits 是客户端可依赖的 Face 应用协议硬限额。
type FaceProtocolLimits struct {
	MaxChatContentBytes int `json:"max_chat_content_bytes"`
	MaxChatDeltaBytes   int `json:"max_chat_delta_bytes"`
	MaxChatStreamBytes  int `json:"max_chat_stream_bytes"`
	MaxChatStreamChunks int `json:"max_chat_stream_chunks"`
	MaxMessageListLimit int `json:"max_message_list_limit"`
}

// FaceCapabilitiesResult 返回当前身份、可选能力和协议限额。
type FaceCapabilitiesResult struct {
	Revision int                `json:"revision"`
	Identity FaceIdentity       `json:"identity"`
	Features []FaceFeature      `json:"features"`
	Limits   FaceProtocolLimits `json:"limits"`
}

// ChatStreamResponse 是一次 provider response 的可恢复可见文本。
type ChatStreamResponse struct {
	ResponseIndex int    `json:"response_index"`
	Content       string `json:"content"`
	Complete      bool   `json:"complete"`
}

// ChatStreamGetResult 是活动或短期保留 Chat 的流快照。
type ChatStreamGetResult struct {
	TargetRequestID string               `json:"target_request_id"`
	LastSeq         int64                `json:"last_seq"`
	Responses       []ChatStreamResponse `json:"responses"`
	Terminal        bool                 `json:"terminal"`
	Status          FaceResultStatus     `json:"status,omitempty"`
}

// ConversationMessagesResult 是稳定 seq 消息分页结果。
type ConversationMessagesResult struct {
	Messages      []FaceMessage `json:"messages"`
	NextBeforeSeq int           `json:"next_before_seq,omitempty"`
	HasMore       bool          `json:"has_more"`
}

// HandListResult 是 hand.list 的结构化结果。
type HandListResult struct {
	Hands []HandSummary `json:"hands"`
}

// HandGetResult 是 hand.get 的结构化结果。
type HandGetResult struct {
	Hand HandSummary `json:"hand"`
}

// RunGetResult 是 run.get 的结构化结果。
type RunGetResult struct {
	Run RemoteRunSummary `json:"run"`
}

// TaskListResult 是 task.list 的分页结果。
type TaskListResult struct {
	Tasks      []TaskSummary `json:"tasks"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// TaskGetResult 是 task.get 的结构化结果。
type TaskGetResult struct {
	Task TaskSummary `json:"task"`
}

// TaskLogResult 是 task.log 的字节区间结果。
// encoding/json 会将 Data 的 []byte 表示编码为 base64 字符串。
type TaskLogResult struct {
	TaskID     string `json:"task_id"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	Data       []byte `json:"data"`
	EOF        bool   `json:"eof"`
	Truncated  bool   `json:"truncated"`
}

// FaceTaskCancelResult 是 task.cancel 的结构化结果。
type FaceTaskCancelResult struct {
	Outcome string      `json:"outcome"`
	Task    TaskSummary `json:"task"`
}

// ChatStartedEventData 是 chat.started 的结构化数据。
type ChatStartedEventData struct {
	RequestID string `json:"request_id"`
}

// ChatToolCalledEventData 是 chat.tool_called 的结构化数据。
type ChatToolCalledEventData struct {
	RequestID  string `json:"request_id"`
	Tool       string `json:"tool"`
	ArgsDigest string `json:"args_digest"`
}

// ChatToolCompletedEventData 是 chat.tool_completed 的结构化数据。
type ChatToolCompletedEventData struct {
	RequestID string `json:"request_id"`
	Tool      string `json:"tool"`
	Success   bool   `json:"success"`
}

// ChatCompletedEventData 是 chat.completed 的结构化数据。
type ChatCompletedEventData struct {
	RequestID string `json:"request_id"`
}

// ChatFailedEventData 是 chat.failed 的结构化数据。
type ChatFailedEventData struct {
	RequestID string        `json:"request_id"`
	Code      FaceErrorCode `json:"code"`
}

// ChatCancelledEventData 是 chat.cancelled 的结构化数据。
type ChatCancelledEventData struct {
	RequestID string `json:"request_id"`
	Reason    string `json:"reason,omitempty"`
}

// ApprovalRequestedEventData 是 approval.requested 的结构化数据。
type ApprovalRequestedEventData = ApprovalRequest

// ApprovalResolvedEventData 是 approval.resolved 的结构化数据。
type ApprovalResolvedEventData struct {
	ApprovalID string               `json:"approval_id"`
	Decision   FaceApprovalDecision `json:"decision"`
	Actor      string               `json:"actor"`
}

// RemoteRunChangedEventData 是 remote_run.changed 的结构化数据。
type RemoteRunChangedEventData struct {
	RunID      string    `json:"run_id"`
	HandID     string    `json:"hand_id"`
	Tool       string    `json:"tool"`
	Status     RunStatus `json:"status"`
	DurationMs int64     `json:"duration_ms"`
}

// HandConnectedEventData 是 hand.connected 的结构化数据。
type HandConnectedEventData struct {
	HandID   string `json:"hand_id"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

// HandDisconnectedEventData 是 hand.disconnected 的结构化数据。
type HandDisconnectedEventData struct {
	HandID string `json:"hand_id"`
}

// ConversationChangedEventData 是 conversation.changed 的结构化数据。
type ConversationChangedEventData struct {
	ConversationID  string `json:"conversation_id"`
	SnapshotVersion int64  `json:"snapshot_version"`
}

// TaskChangedEventData 是 task.changed 携带的完整任务摘要。
type TaskChangedEventData = TaskSummary
