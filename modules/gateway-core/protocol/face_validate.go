package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// IsFaceMessageType 判断消息类型是否属于统一 Face 协议。
func IsFaceMessageType(typ string) bool {
	switch typ {
	case TypeFaceChat, TypeFaceChatCancel,
		TypeFaceConversationList, TypeFaceConversationCreate,
		TypeFaceConversationSnapshot, TypeFaceConversationRename,
		TypeFaceSubscribe, TypeFaceApprovalResolve,
		TypeFaceRunGet, TypeFaceRunCancel, TypeFaceHandList, TypeFaceHandGet,
		TypeFaceAccepted, TypeFaceResult, TypeFaceError, TypeFaceSnapshot, TypeFaceEvent:
		return true
	default:
		return false
	}
}

// ValidateFacePayload 严格解码并校验 Face payload 的结构，不执行权限或业务状态检查。
func ValidateFacePayload(typ string, payload json.RawMessage) error {
	var err error
	switch typ {
	case TypeFaceChat:
		var v FaceChat
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id", v.Content, "content")
		}
	case TypeFaceChatCancel:
		var v FaceChatCancel
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.TargetRequestID, "target_request_id", v.ConversationID, "conversation_id")
		}
	case TypeFaceConversationList:
		var v FaceConversationList
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id")
		}
	case TypeFaceConversationCreate:
		var v FaceConversationCreate
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id")
		}
	case TypeFaceConversationSnapshot:
		var v FaceConversationSnapshot
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id")
		}
	case TypeFaceConversationRename:
		var v FaceConversationRename
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id", v.Name, "name")
		}
	case TypeFaceSubscribe:
		var v FaceSubscribe
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = validateSubscribe(v)
		}
	case TypeFaceApprovalResolve:
		var v FaceApprovalResolve
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.ApprovalID, "approval_id")
		}
		if err == nil && !validFaceApprovalDecision(v.Decision) {
			err = fmt.Errorf("unknown approval decision %q", v.Decision)
		}
	case TypeFaceRunGet:
		var v FaceRunGet
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id", v.RunID, "run_id")
		}
	case TypeFaceRunCancel:
		var v FaceRunCancel
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id", v.RunID, "run_id")
		}
	case TypeFaceHandList:
		var v FaceHandList
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id")
		}
	case TypeFaceHandGet:
		var v FaceHandGet
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.HandID, "hand_id")
		}
	case TypeFaceAccepted:
		var v FaceAccepted
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id")
		}
		if err == nil && !validFaceOperation(v.Operation) {
			err = fmt.Errorf("unknown operation %q", v.Operation)
		}
		if err == nil && v.SnapshotVersion < 0 {
			err = fmt.Errorf("snapshot_version must not be negative")
		}
	case TypeFaceResult:
		var v FaceResult
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id")
		}
		if err == nil && !validFaceResultStatus(v.Status) {
			err = fmt.Errorf("unknown result status %q", v.Status)
		}
		if err == nil && v.ErrorCode != "" && !validFaceErrorCode(v.ErrorCode) {
			err = fmt.Errorf("unknown error code %q", v.ErrorCode)
		}
		if err == nil && len(v.Data) > 0 {
			err = validateRawJSON(v.Data, "data")
		}
	case TypeFaceError:
		var v FaceError
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.Message, "message")
		}
		if err == nil && !validFaceErrorCode(v.Code) {
			err = fmt.Errorf("unknown error code %q", v.Code)
		}
	case TypeFaceSnapshot:
		var v FaceSnapshot
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id")
		}
		if err == nil {
			err = validateConversationSnapshot(v.Snapshot)
		}
	case TypeFaceEvent:
		var v FaceEvent
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = validateFaceEvent(v)
		}
	default:
		return fmt.Errorf("unknown Face message type %q", typ)
	}
	if err != nil {
		return fmt.Errorf("validate %s payload: %w", typ, err)
	}
	return nil
}

func decodeFacePayload(payload json.RawMessage, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON data")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func validateRawJSON(data json.RawMessage, field string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%s contains trailing JSON data", field)
	}
	return nil
}

func requireFields(fields ...string) error {
	for i := 0; i < len(fields); i += 2 {
		if fields[i] == "" {
			return fmt.Errorf("%s is required", fields[i+1])
		}
	}
	return nil
}

func validateSubscribe(v FaceSubscribe) error {
	if err := requireFields(v.RequestID, "request_id"); err != nil {
		return err
	}
	for _, id := range v.ConversationIDs {
		if id == "" {
			return fmt.Errorf("conversation_ids must not contain empty values")
		}
	}
	for _, typ := range v.EventTypes {
		if !validFaceEventType(typ) {
			return fmt.Errorf("unknown event type %q", typ)
		}
	}
	return nil
}

func validateConversationSnapshot(v ConversationSnapshot) error {
	if err := requireFields(v.ConversationID, "snapshot.conversation_id", v.Name, "snapshot.name", v.Mode, "snapshot.mode"); err != nil {
		return err
	}
	if v.Messages == nil || v.PendingChats == nil || v.PendingApprovals == nil || v.ActiveRuns == nil {
		return fmt.Errorf("snapshot collections are required")
	}
	if v.SnapshotVersion < 0 {
		return fmt.Errorf("snapshot.snapshot_version must not be negative")
	}
	for _, chat := range v.PendingChats {
		if err := requireFields(chat.RequestID, "pending_chats.request_id"); err != nil || chat.StartedAt.IsZero() {
			if err != nil {
				return err
			}
			return fmt.Errorf("pending_chats.started_at is required")
		}
	}
	for _, approval := range v.PendingApprovals {
		if err := validateApprovalRequest(approval); err != nil {
			return err
		}
	}
	for _, run := range v.ActiveRuns {
		if err := requireFields(run.RunID, "active_runs.run_id", run.RequestID, "active_runs.request_id", run.HandID, "active_runs.hand_id", run.Tool, "active_runs.tool"); err != nil {
			return err
		}
		if !validRunStatus(run.Status) {
			return fmt.Errorf("unknown run status %q", run.Status)
		}
		if run.DurationMs < 0 || run.CreatedAt.IsZero() {
			return fmt.Errorf("active run duration and created_at are invalid")
		}
	}
	return nil
}

func validateFaceEvent(v FaceEvent) error {
	if v.EventSeq <= 0 {
		return fmt.Errorf("event_seq must be positive")
	}
	if !validFaceEventType(v.Type) {
		return fmt.Errorf("unknown event type %q", v.Type)
	}
	if err := requireFields(v.Source, "source", v.Message, "message"); err != nil {
		return err
	}
	if !validFaceEventLevel(v.Level) {
		return fmt.Errorf("unknown event level %q", v.Level)
	}
	if v.Timestamp.IsZero() {
		return fmt.Errorf("timestamp is required")
	}
	if len(v.Data) == 0 {
		return fmt.Errorf("data is required")
	}

	switch v.Type {
	case FaceEventChatStarted:
		var data ChatStartedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		return requireFields(data.RequestID, "data.request_id")
	case FaceEventChatToolCalled:
		var data ChatToolCalledEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		return requireFields(data.RequestID, "data.request_id", data.Tool, "data.tool", data.ArgsDigest, "data.args_digest")
	case FaceEventChatToolCompleted:
		var data ChatToolCompletedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		return requireFields(data.RequestID, "data.request_id", data.Tool, "data.tool")
	case FaceEventChatCompleted:
		var data ChatCompletedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		return requireFields(data.RequestID, "data.request_id")
	case FaceEventChatFailed:
		var data ChatFailedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if err := requireFields(data.RequestID, "data.request_id"); err != nil {
			return err
		}
		if !validFaceErrorCode(data.Code) {
			return fmt.Errorf("unknown error code %q", data.Code)
		}
	case FaceEventChatCancelled:
		var data ChatCancelledEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		return requireFields(data.RequestID, "data.request_id")
	case FaceEventApprovalRequested:
		var data ApprovalRequestedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		return validateApprovalRequest(data)
	case FaceEventApprovalResolved:
		var data ApprovalResolvedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if err := requireFields(data.ApprovalID, "data.approval_id", data.Actor, "data.actor"); err != nil {
			return err
		}
		if !validFaceApprovalDecision(data.Decision) {
			return fmt.Errorf("unknown approval decision %q", data.Decision)
		}
	case FaceEventRemoteRunChanged:
		var data RemoteRunChangedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if err := requireFields(data.RunID, "data.run_id", data.HandID, "data.hand_id", data.Tool, "data.tool"); err != nil {
			return err
		}
		if !validRunStatus(data.Status) {
			return fmt.Errorf("unknown run status %q", data.Status)
		}
		if data.DurationMs < 0 {
			return fmt.Errorf("data.duration_ms must not be negative")
		}
	case FaceEventHandConnected:
		var data HandConnectedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		return requireFields(data.HandID, "data.hand_id", data.Hostname, "data.hostname", data.OS, "data.os", data.Arch, "data.arch")
	case FaceEventHandDisconnected:
		var data HandDisconnectedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		return requireFields(data.HandID, "data.hand_id")
	case FaceEventConversationChanged:
		var data ConversationChangedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if err := requireFields(data.ConversationID, "data.conversation_id"); err != nil {
			return err
		}
		if data.SnapshotVersion < 0 {
			return fmt.Errorf("data.snapshot_version must not be negative")
		}
	}
	return nil
}

func validateApprovalRequest(v ApprovalRequest) error {
	if err := requireFields(v.ApprovalID, "approval_id", v.ConversationID, "conversation_id", v.RequestID, "request_id", v.Tool, "tool", v.Reason, "reason", v.ArgsDigest, "args_digest"); err != nil {
		return err
	}
	if v.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at is required")
	}
	return nil
}

func validFaceErrorCode(code FaceErrorCode) bool {
	switch code {
	case FaceErrorInvalidRequest, FaceErrorUnauthorized, FaceErrorForbidden,
		FaceErrorConversationNotFound, FaceErrorRequestConflict, FaceErrorRequestInProgress,
		FaceErrorApprovalNotFound, FaceErrorApprovalExpired, FaceErrorRunNotFound,
		FaceErrorHandNotFound, FaceErrorBusy, FaceErrorCancelled, FaceErrorTimeout, FaceErrorInternal:
		return true
	default:
		return false
	}
}

func validFaceResultStatus(status FaceResultStatus) bool {
	switch status {
	case FaceResultSucceeded, FaceResultFailed, FaceResultCancelled, FaceResultTimedOut:
		return true
	default:
		return false
	}
}

func validFaceApprovalDecision(decision FaceApprovalDecision) bool {
	switch decision {
	case FaceApprovalAllowOnce, FaceApprovalDenyOnce, FaceApprovalAllowSession, FaceApprovalDenySession:
		return true
	default:
		return false
	}
}

func validFaceEventType(typ FaceEventType) bool {
	switch typ {
	case FaceEventChatStarted, FaceEventChatToolCalled, FaceEventChatToolCompleted,
		FaceEventChatCompleted, FaceEventChatFailed, FaceEventChatCancelled,
		FaceEventApprovalRequested, FaceEventApprovalResolved, FaceEventRemoteRunChanged,
		FaceEventHandConnected, FaceEventHandDisconnected, FaceEventConversationChanged:
		return true
	default:
		return false
	}
}

func validFaceEventLevel(level FaceEventLevel) bool {
	switch level {
	case FaceEventLevelInfo, FaceEventLevelWarn, FaceEventLevelError:
		return true
	default:
		return false
	}
}

func validFaceOperation(operation FaceOperation) bool {
	switch operation {
	case FaceOperationChat, FaceOperationChatCancel, FaceOperationConversationList,
		FaceOperationConversationCreate, FaceOperationConversationSnapshot,
		FaceOperationConversationRename, FaceOperationSubscribe, FaceOperationApprovalResolve,
		FaceOperationRunGet, FaceOperationRunCancel, FaceOperationHandList, FaceOperationHandGet:
		return true
	default:
		return false
	}
}

func validRunStatus(status RunStatus) bool {
	switch status {
	case RunCreated, RunApproved, RunSent, RunAccepted, RunRunning, RunSucceeded,
		RunFailed, RunRejected, RunCancelRequested, RunCancelled, RunTimedOut, RunLost:
		return true
	default:
		return false
	}
}
