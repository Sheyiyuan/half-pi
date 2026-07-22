package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"unicode/utf8"
)

// IsFaceCommandType 判断消息类型是否为 Face 发往 Mind 的 command。
func IsFaceCommandType(typ string) bool {
	switch typ {
	case TypeFaceChat, TypeFaceChatCancel,
		TypeFaceChatStreamGet, TypeFaceCapabilitiesGet,
		TypeFaceConversationList, TypeFaceConversationCreate,
		TypeFaceConversationSnapshot, TypeFaceConversationRename, TypeFaceConversationMessages,
		TypeFaceConversationCompact, TypeFaceCompactStatus,
		TypeFaceSubscribe, TypeFaceApprovalResolve,
		TypeFaceRunGet, TypeFaceRunCancel, TypeFaceHandList, TypeFaceHandGet,
		TypeFaceTaskList, TypeFaceTaskGet, TypeFaceTaskLog, TypeFaceTaskCancel:
		return true
	default:
		return false
	}
}

// IsFaceServerMessageType 判断消息类型是否为 Mind 发往 Face 的正式消息。
func IsFaceServerMessageType(typ string) bool {
	switch typ {
	case TypeFaceAccepted, TypeFaceResult, TypeFaceError, TypeFaceSnapshot, TypeFaceEvent,
		TypeFaceChatDelta, TypeFaceChatStreamEnd, TypeFaceRunProgress:
		return true
	default:
		return false
	}
}

// IsFaceMessageType 判断消息类型是否属于统一 Face 协议。
func IsFaceMessageType(typ string) bool {
	return IsFaceCommandType(typ) || IsFaceServerMessageType(typ)
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
		if err == nil && len(v.Content) > MaxFaceChatContentBytes {
			err = fmt.Errorf("content exceeds %d bytes", MaxFaceChatContentBytes)
		}
	case TypeFaceChatCancel:
		var v FaceChatCancel
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.TargetRequestID, "target_request_id", v.ConversationID, "conversation_id")
		}
	case TypeFaceChatStreamGet:
		var v FaceChatStreamGet
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id", v.TargetRequestID, "target_request_id")
		}
	case TypeFaceCapabilitiesGet:
		var v FaceCapabilitiesGet
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id")
		}
		if err == nil {
			err = validateAcceptedFeatures(v.AcceptFeatures)
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
	case TypeFaceConversationMessages:
		var v FaceConversationMessages
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = validateFaceConversationMessages(v)
		}
	case TypeFaceConversationCompact:
		var v FaceConversationCompact
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id")
		}
		if err == nil {
			err = validateFaceCompactTarget(v.Target)
		}
	case TypeFaceCompactStatus:
		var v FaceCompactStatusGet
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id")
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
		if err == nil && len(v.Reason) > MaxFaceApprovalReasonBytes {
			err = fmt.Errorf("approval reason exceeds %d bytes", MaxFaceApprovalReasonBytes)
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
	case TypeFaceTaskList:
		var v FaceTaskList
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = validateFaceTaskList(v)
		}
	case TypeFaceTaskGet:
		var v FaceTaskGet
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id", v.TaskID, "task_id")
		}
	case TypeFaceTaskLog:
		var v FaceTaskLog
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = validateFaceTaskLog(v)
		}
	case TypeFaceTaskCancel:
		var v FaceTaskCancel
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id", v.TaskID, "task_id")
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
		if err == nil {
			err = validateFaceResult(v)
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
	case TypeFaceChatDelta:
		var v FaceChatDelta
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = validateFaceChatDelta(v)
		}
	case TypeFaceChatStreamEnd:
		var v FaceChatStreamEnd
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = validateFaceChatStreamEnd(v)
		}
	case TypeFaceRunProgress:
		var v FaceRunProgress
		err = decodeFacePayload(payload, &v)
		if err == nil {
			err = validateFaceRunProgress(v)
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

func requireJSONFields(data json.RawMessage, fields ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	for _, field := range fields {
		if _, ok := object[field]; !ok {
			return fmt.Errorf("%s is required", field)
		}
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

// ValidateFaceScopes 校验 Face 权限集合非空、无重复且全部已知。
func ValidateFaceScopes(scopes []FaceScope) error {
	if len(scopes) == 0 {
		return fmt.Errorf("at least one Face scope is required")
	}
	seen := make(map[FaceScope]struct{}, len(scopes))
	for _, scope := range scopes {
		if !validFaceScope(scope) {
			return fmt.Errorf("unknown Face scope %q", scope)
		}
		if _, ok := seen[scope]; ok {
			return fmt.Errorf("duplicate Face scope %q", scope)
		}
		seen[scope] = struct{}{}
	}
	return nil
}

func validateSubscribe(v FaceSubscribe) error {
	if err := requireFields(v.RequestID, "request_id"); err != nil {
		return err
	}
	conversationIDs := make(map[string]struct{}, len(v.ConversationIDs))
	for _, id := range v.ConversationIDs {
		if id == "" {
			return fmt.Errorf("conversation_ids must not contain empty values")
		}
		if _, ok := conversationIDs[id]; ok {
			return fmt.Errorf("conversation_ids must not contain duplicate values")
		}
		conversationIDs[id] = struct{}{}
	}
	eventTypes := make(map[FaceEventType]struct{}, len(v.EventTypes))
	for _, typ := range v.EventTypes {
		if !validFaceEventType(typ) {
			return fmt.Errorf("unknown event type %q", typ)
		}
		if _, ok := eventTypes[typ]; ok {
			return fmt.Errorf("event_types must not contain duplicate values")
		}
		eventTypes[typ] = struct{}{}
	}
	transientTypes := make(map[FaceTransientType]struct{}, len(v.TransientTypes))
	for _, typ := range v.TransientTypes {
		if !validFaceTransientType(typ) {
			return fmt.Errorf("unknown transient type %q", typ)
		}
		if _, ok := transientTypes[typ]; ok {
			return fmt.Errorf("transient_types must not contain duplicate values")
		}
		transientTypes[typ] = struct{}{}
	}
	return nil
}

func validateFaceConversationMessages(v FaceConversationMessages) error {
	if err := requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id"); err != nil {
		return err
	}
	if v.BeforeSeq < 0 {
		return fmt.Errorf("before_seq must not be negative")
	}
	if v.Limit < 0 || v.Limit > MaxFaceMessageListLimit {
		return fmt.Errorf("limit must be zero or between 1 and %d", MaxFaceMessageListLimit)
	}
	return nil
}

func validateFaceTaskList(v FaceTaskList) error {
	if err := requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id"); err != nil {
		return err
	}
	if v.Limit < 0 || v.Limit > MaxFaceTaskListLimit {
		return fmt.Errorf("limit must be zero or between 1 and %d", MaxFaceTaskListLimit)
	}
	statuses := make(map[TaskStatus]struct{}, len(v.Statuses))
	for _, status := range v.Statuses {
		if !validTaskStatus(status) {
			return fmt.Errorf("unknown task status %q", status)
		}
		if _, ok := statuses[status]; ok {
			return fmt.Errorf("statuses must not contain duplicate values")
		}
		statuses[status] = struct{}{}
	}
	return nil
}

func validateFaceTaskLog(v FaceTaskLog) error {
	if err := requireFields(v.RequestID, "request_id", v.ConversationID, "conversation_id", v.TaskID, "task_id"); err != nil {
		return err
	}
	if v.Offset < 0 {
		return fmt.Errorf("offset must not be negative")
	}
	if v.Limit <= 0 || v.Limit > MaxTaskLogResponseBytes {
		return fmt.Errorf("limit must be between 1 and %d", MaxTaskLogResponseBytes)
	}
	return nil
}

func validateFaceResult(v FaceResult) error {
	switch v.Status {
	case FaceResultSucceeded:
		if v.ErrorCode != "" || v.Error != "" {
			return fmt.Errorf("succeeded result must not contain error_code or error")
		}
	case FaceResultFailed, FaceResultCancelled, FaceResultTimedOut:
		if v.ErrorCode == "" || v.Error == "" {
			return fmt.Errorf("non-succeeded result requires error_code and error")
		}
	}
	return nil
}

// ValidateFaceResultData 严格解码并校验支持结构化结果的操作数据。
func ValidateFaceResultData(operation FaceOperation, data json.RawMessage) error {
	var err error
	switch operation {
	case FaceOperationConversationList:
		var v ConversationListResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			if v.Conversations == nil {
				err = fmt.Errorf("conversations is required")
			} else {
				for _, conversation := range v.Conversations {
					if err = validateConversationSummary(conversation); err != nil {
						break
					}
				}
			}
		}
	case FaceOperationConversationCreate:
		var v ConversationCreateResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			err = validateConversationSummary(v.Conversation)
		}
	case FaceOperationConversationRename:
		var v ConversationRenameResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			err = validateConversationSummary(v.Conversation)
		}
	case FaceOperationCapabilitiesGet:
		var v FaceCapabilitiesResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			err = validateFaceCapabilities(v)
		}
	case FaceOperationChatStreamGet:
		var v ChatStreamGetResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			err = validateChatStreamGetResult(v)
		}
	case FaceOperationConversationMessages:
		var v ConversationMessagesResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			err = validateConversationMessagesResult(v)
		}
	case FaceOperationHandList:
		var v HandListResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			if v.Hands == nil {
				err = fmt.Errorf("hands is required")
			} else {
				for _, hand := range v.Hands {
					if err = validateHandSummary(hand); err != nil {
						break
					}
				}
			}
		}
	case FaceOperationHandGet:
		var v HandGetResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			err = validateHandSummary(v.Hand)
		}
	case FaceOperationRunGet:
		var v RunGetResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			err = validateRemoteRunSummary(v.Run, false)
		}
	case FaceOperationTaskList:
		var v TaskListResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			if v.Tasks == nil {
				err = fmt.Errorf("tasks is required")
			} else if len(v.Tasks) > MaxFaceTaskListLimit {
				err = fmt.Errorf("tasks exceeds %d items", MaxFaceTaskListLimit)
			} else {
				for i, task := range v.Tasks {
					if err = validateTaskSummary(task); err != nil {
						break
					}
					if i > 0 && !taskSummaryBefore(v.Tasks[i-1], task) {
						err = fmt.Errorf("tasks must be sorted by updated_at and task_id descending")
						break
					}
				}
			}
		}
	case FaceOperationTaskGet:
		var v TaskGetResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			err = validateTaskSummary(v.Task)
		}
	case FaceOperationTaskLog:
		var v TaskLogResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			err = validateTaskLogResult(v)
		}
	case FaceOperationTaskCancel:
		var v FaceTaskCancelResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			switch v.Outcome {
			case string(TaskCancelCancelled), string(TaskCancelAlreadyDone), string(TaskCancelUnknownTask), string(TaskCancelFailed):
				err = validateTaskSummary(v.Task)
			default:
				err = fmt.Errorf("unknown task cancel outcome %q", v.Outcome)
			}
		}
	case FaceOperationConversationCompact:
		var v FaceCompactResult
		err = decodeFacePayload(data, &v)
		if err == nil {
			err = validateFaceCompactResult(v)
		}
	case FaceOperationCompactStatus:
		var v FaceCompactStatus
		err = requireJSONFields(data,
			"enabled", "automatic", "operation_state", "summary_id", "covered_from_seq", "covered_to_seq",
			"last_seq", "message_count", "context_message_count", "summary_node_count", "summary_storage_bytes",
			"summary_bytes", "source_estimated_tokens", "summary_estimated_tokens", "compression_ratio",
			"generation_mode", "candidate_generation_mode", "configured_summary_provider_id", "configured_summary_model_id",
			"active_summary_provider_id", "active_summary_model_id", "estimated_tokens", "input_budget",
			"reserved_output_tokens", "high_limit", "low_target", "compressible_from_seq", "compressible_to_seq",
			"retained_from_seq", "retained_to_seq", "pending", "pending_attempt", "pending_not_before",
			"summary_input_budget", "required_summary_input_estimated_tokens", "context_version", "projection_version",
			"policy_version", "profile", "active_projection_version", "active_policy_version", "active_profile",
			"compact_degraded", "blocker", "warnings")
		if err == nil {
			err = decodeFacePayload(data, &v)
		}
		if err == nil {
			err = validateFaceCompactStatus(v)
		}
	default:
		return fmt.Errorf("operation %q has no structured result data", operation)
	}
	if err != nil {
		return fmt.Errorf("validate %s result data: %w", operation, err)
	}
	return nil
}

func validateConversationSummary(v ConversationSummary) error {
	if err := requireFields(v.ConversationID, "conversation_id", v.Name, "name", v.Mode, "mode"); err != nil {
		return err
	}
	if v.MessageCount < 0 || v.UpdatedAt.IsZero() {
		return fmt.Errorf("message_count and updated_at are invalid")
	}
	return nil
}

func validateFaceCapabilities(v FaceCapabilitiesResult) error {
	if v.Revision != FaceProtocolRevision {
		return fmt.Errorf("revision must be %d", FaceProtocolRevision)
	}
	if err := requireFields(v.Identity.ID, "identity.id", v.Identity.Label, "identity.label"); err != nil {
		return err
	}
	if err := ValidateFaceScopes(v.Identity.Scopes); err != nil {
		return fmt.Errorf("identity.scopes: %w", err)
	}
	if v.Features == nil {
		return fmt.Errorf("features is required")
	}
	seen := make(map[FaceFeature]struct{}, len(v.Features))
	for _, feature := range v.Features {
		if !validFaceFeature(feature) {
			return fmt.Errorf("unknown feature %q", feature)
		}
		if _, ok := seen[feature]; ok {
			return fmt.Errorf("features must not contain duplicate values")
		}
		seen[feature] = struct{}{}
	}
	limits := v.Limits
	if limits.MaxChatContentBytes != MaxFaceChatContentBytes || limits.MaxChatDeltaBytes != MaxFaceChatDeltaBytes ||
		limits.MaxChatStreamBytes != MaxFaceChatStreamBytes || limits.MaxChatStreamChunks != MaxFaceChatStreamChunks ||
		limits.MaxMessageListLimit != MaxFaceMessageListLimit {
		return fmt.Errorf("limits do not match protocol constants")
	}
	return nil
}

func validateAcceptedFeatures(features []string) error {
	if len(features) > MaxFaceAcceptFeatures {
		return fmt.Errorf("accept_features exceeds %d items", MaxFaceAcceptFeatures)
	}
	seen := make(map[string]struct{}, len(features))
	for _, feature := range features {
		if !validOpenFeature(feature) {
			return fmt.Errorf("invalid accepted feature %q", feature)
		}
		if _, ok := seen[feature]; ok {
			return fmt.Errorf("accept_features must not contain duplicate values")
		}
		seen[feature] = struct{}{}
	}
	return nil
}

func validOpenFeature(feature string) bool {
	if len(feature) == 0 || len(feature) > MaxFaceFeatureBytes || !isLowerFeatureAlphaNum(feature[0]) {
		return false
	}
	for index := 1; index < len(feature); index++ {
		char := feature[index]
		if isLowerFeatureAlphaNum(char) || char == '_' || char == '.' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func isLowerFeatureAlphaNum(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
}

func validateFaceCompactTarget(target FaceCompactTarget) error {
	switch target.Mode {
	case FaceCompactTargetDefault, FaceCompactTargetRebase:
		if target.Ratio != nil || target.KeepMessages != nil {
			return fmt.Errorf("%s target must not contain target-specific fields", target.Mode)
		}
	case FaceCompactTargetRatio:
		if target.Ratio == nil || target.KeepMessages != nil || math.IsNaN(*target.Ratio) || math.IsInf(*target.Ratio, 0) || *target.Ratio < .20 || *target.Ratio >= .95 {
			return fmt.Errorf("ratio target requires ratio between 0.20 and 0.95")
		}
	case FaceCompactTargetKeep:
		if target.KeepMessages == nil || target.Ratio != nil || *target.KeepMessages < 1 || *target.KeepMessages > 10_000 {
			return fmt.Errorf("keep_messages target requires a value between 1 and 10000")
		}
	default:
		return fmt.Errorf("unknown compact target mode %q", target.Mode)
	}
	return nil
}

func validateFaceCompactResult(v FaceCompactResult) error {
	if err := requireFields(v.SummaryID, "summary_id"); err != nil {
		return err
	}
	if v.FromSeq != 1 || v.ToSeq < v.FromSeq || v.BeforeEstimatedTokens < 0 || v.AfterEstimatedTokens < 0 ||
		v.RetainedFromSeq < 0 || v.RetainedToSeq < 0 || v.ContextVersion == 0 || !validCompactGenerationMode(v.GenerationMode, false) {
		return fmt.Errorf("compact result range, estimates, mode or context version is invalid")
	}
	if (v.RetainedFromSeq == 0) != (v.RetainedToSeq == 0) || v.RetainedFromSeq > 0 && (v.RetainedFromSeq != v.ToSeq+1 || v.RetainedToSeq < v.RetainedFromSeq) {
		return fmt.Errorf("retained range is invalid")
	}
	tokenVariant := v.TargetTokens != nil || v.TargetMet != nil
	keepVariant := v.RequestedKeepMessages != nil || v.RetainedMessageCount != nil || v.SafetyRetainedExtra != nil || v.CapacityRetainedExtra != nil
	if tokenVariant == keepVariant {
		return fmt.Errorf("compact result requires exactly one target result variant")
	}
	if tokenVariant {
		if v.TargetTokens == nil || v.TargetMet == nil || *v.TargetTokens < 0 {
			return fmt.Errorf("token target result is incomplete")
		}
		return nil
	}
	if v.RequestedKeepMessages == nil || v.RetainedMessageCount == nil || v.SafetyRetainedExtra == nil || v.CapacityRetainedExtra == nil ||
		*v.RequestedKeepMessages < 1 || *v.RetainedMessageCount < 0 || *v.SafetyRetainedExtra < 0 || *v.CapacityRetainedExtra < 0 ||
		*v.RetainedMessageCount != *v.RequestedKeepMessages+*v.SafetyRetainedExtra+*v.CapacityRetainedExtra {
		return fmt.Errorf("keep target result is invalid")
	}
	return nil
}

func validateFaceCompactStatus(v FaceCompactStatus) error {
	if !validCompactOperationState(v.OperationState) || v.LastSeq < 0 || v.MessageCount < 0 || v.ContextMessageCount < 0 ||
		v.ContextMessageCount > v.MessageCount || v.SummaryNodeCount < 0 || v.SummaryStorageBytes < 0 || v.SummaryBytes < 0 ||
		v.SourceEstimatedTokens < 0 || v.SummaryEstimatedTokens < 0 || math.IsNaN(v.CompressionRatio) || math.IsInf(v.CompressionRatio, 0) || v.CompressionRatio < 0 ||
		v.EstimatedTokens < 0 || v.InputBudget < 0 || v.ReservedOutputTokens < 0 || v.HighLimit < 0 || v.LowTarget < 0 ||
		v.PendingAttempt < 0 || v.PendingNotBefore < 0 || v.SummaryInputBudget < 0 || v.RequiredSummaryInputEstimatedTokens < 0 {
		return fmt.Errorf("compact status contains invalid numeric or operation values")
	}
	if !validCompactGenerationMode(v.GenerationMode, true) || !validCompactGenerationMode(v.CandidateGenerationMode, true) {
		return fmt.Errorf("compact status contains an unknown generation mode")
	}
	if (v.SummaryID == "") != (v.CoveredFromSeq == 0 && v.CoveredToSeq == 0) || v.SummaryID != "" && (v.CoveredFromSeq != 1 || v.CoveredToSeq < 1 || v.SummaryBytes < 1) {
		return fmt.Errorf("active compact summary fields are inconsistent")
	}
	if !validOptionalSeqRange(v.CompressibleFromSeq, v.CompressibleToSeq) || !validOptionalSeqRange(v.RetainedFromSeq, v.RetainedToSeq) {
		return fmt.Errorf("compact candidate ranges are invalid")
	}
	if !v.Pending && (v.PendingAttempt != 0 || v.PendingNotBefore != 0) {
		return fmt.Errorf("compact pending fields are inconsistent")
	}
	if v.Blocker != "" && !validCompactErrorCode(v.Blocker) {
		return fmt.Errorf("unknown compact blocker %q", v.Blocker)
	}
	if v.Warnings == nil {
		return fmt.Errorf("warnings is required")
	}
	seenWarnings := make(map[string]struct{}, len(v.Warnings))
	previous := ""
	for _, warning := range v.Warnings {
		switch warning {
		case "shared_summary_model", "summary_node_count_high", "summary_storage_high":
		default:
			return fmt.Errorf("unknown compact warning %q", warning)
		}
		if _, ok := seenWarnings[warning]; ok || previous > warning {
			return fmt.Errorf("compact warnings must be unique and sorted")
		}
		seenWarnings[warning], previous = struct{}{}, warning
	}
	return nil
}

func validOptionalSeqRange(from, to int) bool {
	return from == 0 && to == 0 || from > 0 && to >= from
}

func validCompactOperationState(state string) bool {
	switch state {
	case "idle", "chat_preparing", "compacting_for_chat", "chat_running", "compacting_manual", "compacting_auto", "mutating_context":
		return true
	default:
		return false
	}
}

func validCompactGenerationMode(mode FaceCompactGenerationMode, allowEmpty bool) bool {
	return allowEmpty && mode == "" || mode == FaceCompactGenerationFull || mode == FaceCompactGenerationIncremental || mode == FaceCompactGenerationRebase
}

func validateChatStreamGetResult(v ChatStreamGetResult) error {
	if err := requireFields(v.TargetRequestID, "target_request_id"); err != nil {
		return err
	}
	if v.LastSeq < 0 || v.LastSeq > MaxFaceChatStreamChunks {
		return fmt.Errorf("last_seq is outside the stream limit")
	}
	if v.Responses == nil {
		return fmt.Errorf("responses is required")
	}
	totalBytes := 0
	for i, response := range v.Responses {
		if response.ResponseIndex != i+1 {
			return fmt.Errorf("responses must have contiguous response_index values")
		}
		if !utf8.ValidString(response.Content) {
			return fmt.Errorf("response content must be valid UTF-8")
		}
		totalBytes += len(response.Content)
		if totalBytes > MaxFaceChatStreamBytes {
			return fmt.Errorf("responses exceed %d bytes", MaxFaceChatStreamBytes)
		}
	}
	if v.Terminal {
		if !validFaceResultStatus(v.Status) {
			return fmt.Errorf("terminal stream requires status")
		}
	} else if v.Status != "" {
		return fmt.Errorf("active stream must not contain status")
	}
	return nil
}

func validateConversationMessagesResult(v ConversationMessagesResult) error {
	if v.Messages == nil {
		return fmt.Errorf("messages is required")
	}
	if len(v.Messages) > MaxFaceMessageListLimit {
		return fmt.Errorf("messages exceeds %d items", MaxFaceMessageListLimit)
	}
	for i, message := range v.Messages {
		if err := validateFaceMessage(message); err != nil {
			return err
		}
		if i > 0 && v.Messages[i-1].Seq >= message.Seq {
			return fmt.Errorf("messages must be sorted by seq ascending")
		}
	}
	if v.NextBeforeSeq < 0 {
		return fmt.Errorf("next_before_seq must not be negative")
	}
	if v.HasMore {
		if len(v.Messages) == 0 || v.NextBeforeSeq != v.Messages[0].Seq {
			return fmt.Errorf("has_more requires next_before_seq at the oldest returned message")
		}
	} else if v.NextBeforeSeq != 0 {
		return fmt.Errorf("next_before_seq requires has_more")
	}
	return nil
}

func validateFaceMessage(v FaceMessage) error {
	if v.ID <= 0 || v.Seq <= 0 || v.CreatedAt.IsZero() {
		return fmt.Errorf("message id, seq and created_at are required")
	}
	switch v.Role {
	case "system", "user", "assistant", "tool":
	default:
		return fmt.Errorf("unknown message role %q", v.Role)
	}
	if !utf8.ValidString(v.Content) {
		return fmt.Errorf("message content must be valid UTF-8")
	}
	return nil
}

func validateHandSummary(v HandSummary) error {
	if err := requireFields(v.HandID, "hand_id", v.Hostname, "hostname", v.OS, "os", v.Arch, "arch"); err != nil {
		return err
	}
	if !v.Connected {
		return fmt.Errorf("hand must be connected")
	}
	return nil
}

func validateRemoteRunSummary(v RemoteRunSummary, activeOnly bool) error {
	if err := requireFields(v.RunID, "run_id", v.RequestID, "request_id", v.HandID, "hand_id", v.Tool, "tool"); err != nil {
		return err
	}
	if !validRunStatus(v.Status) {
		return fmt.Errorf("unknown run status %q", v.Status)
	}
	if activeOnly && IsTerminalRunStatus(v.Status) {
		return fmt.Errorf("active run must have a non-terminal status")
	}
	if IsTerminalRunStatus(v.Status) != (v.FinishedAt != nil) {
		return fmt.Errorf("finished_at must be set exactly for terminal status")
	}
	if v.DurationMs < 0 || v.CreatedAt.IsZero() {
		return fmt.Errorf("duration and created_at are invalid")
	}
	if v.FinishedAt != nil && v.FinishedAt.Before(v.CreatedAt) {
		return fmt.Errorf("finished_at must not precede created_at")
	}
	return nil
}

func validateConversationSnapshot(v ConversationSnapshot) error {
	if err := requireFields(v.ConversationID, "snapshot.conversation_id", v.Name, "snapshot.name", v.Mode, "snapshot.mode"); err != nil {
		return err
	}
	if v.Messages == nil || v.PendingChats == nil || v.PendingApprovals == nil || v.ActiveRuns == nil || v.Tasks == nil {
		return fmt.Errorf("snapshot collections are required")
	}
	if v.SnapshotVersion < 1 {
		return fmt.Errorf("snapshot.snapshot_version must be positive")
	}
	if v.TaskHistoryLimit < 0 || v.TaskHistoryLimit > MaxFaceTaskHistoryLimit {
		return fmt.Errorf("snapshot.task_history_limit must be between 0 and %d", MaxFaceTaskHistoryLimit)
	}
	for i, message := range v.Messages {
		if err := validateFaceMessage(message); err != nil {
			return err
		}
		if i > 0 && v.Messages[i-1].Seq >= message.Seq {
			return fmt.Errorf("snapshot messages must be sorted by seq ascending")
		}
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
		if err := validateRemoteRunSummary(run, true); err != nil {
			return fmt.Errorf("active_runs: %w", err)
		}
	}
	terminalTasks := 0
	for _, task := range v.Tasks {
		if err := validateTaskSummary(task); err != nil {
			return err
		}
		if task.ConversationID != v.ConversationID {
			return fmt.Errorf("task conversation_id must match snapshot conversation_id")
		}
		if IsTerminalTaskStatus(task.Status) {
			terminalTasks++
		}
	}
	if terminalTasks > v.TaskHistoryLimit {
		return fmt.Errorf("terminal tasks exceed task_history_limit")
	}
	if v.TaskHistoryTruncated && v.TaskHistoryLimit > 0 && terminalTasks != v.TaskHistoryLimit {
		return fmt.Errorf("truncated task history must fill task_history_limit")
	}
	return nil
}

func taskSummaryBefore(previous, current TaskSummary) bool {
	if previous.UpdatedAt.Equal(current.UpdatedAt) {
		return previous.TaskID > current.TaskID
	}
	return previous.UpdatedAt.After(current.UpdatedAt)
}

func validateTaskSummary(v TaskSummary) error {
	if err := requireFields(v.TaskID, "task_id", v.ConversationID, "conversation_id", v.HandID, "hand_id", v.Tool, "tool", v.ArgsDigest, "args_digest"); err != nil {
		return err
	}
	if !validTaskStatus(v.Status) {
		return fmt.Errorf("unknown task status %q", v.Status)
	}
	if v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() {
		return fmt.Errorf("created_at and updated_at are required")
	}
	if v.UpdatedAt.Before(v.CreatedAt) {
		return fmt.Errorf("updated_at must not precede created_at")
	}
	if v.StartedAt != nil && v.StartedAt.Before(v.CreatedAt) {
		return fmt.Errorf("started_at must not precede created_at")
	}
	if IsTerminalTaskStatus(v.Status) != (v.FinishedAt != nil) {
		return fmt.Errorf("finished_at must be set exactly for terminal status")
	}
	if v.FinishedAt != nil {
		if v.FinishedAt.Before(v.CreatedAt) || (v.StartedAt != nil && v.FinishedAt.Before(*v.StartedAt)) || v.UpdatedAt.Before(*v.FinishedAt) {
			return fmt.Errorf("finished_at is inconsistent with task timestamps")
		}
	}
	if v.LogBytes < 0 {
		return fmt.Errorf("log_bytes must not be negative")
	}
	wantCode := taskStatusErrorCode(v.Status)
	if v.ErrorCode != wantCode {
		return fmt.Errorf("status %q requires error_code %q", v.Status, wantCode)
	}
	if v.ErrorCode == "" && v.Error != "" {
		return fmt.Errorf("error requires error_code")
	}
	return nil
}

func taskStatusErrorCode(status TaskStatus) FaceErrorCode {
	switch status {
	case TaskFailed:
		return FaceErrorTaskFailed
	case TaskCancelled:
		return FaceErrorTaskCancelled
	case TaskTimedOut:
		return FaceErrorTaskTimedOut
	case TaskLost:
		return FaceErrorTaskLost
	default:
		return ""
	}
}

func validateTaskLogResult(v TaskLogResult) error {
	if err := requireFields(v.TaskID, "task_id"); err != nil {
		return err
	}
	if v.Offset < 0 {
		return fmt.Errorf("offset must not be negative")
	}
	if v.Data == nil {
		return fmt.Errorf("task log data is required")
	}
	if len(v.Data) > MaxTaskLogResponseBytes {
		return fmt.Errorf("task log data exceeds %d bytes", MaxTaskLogResponseBytes)
	}
	if v.Offset > math.MaxInt64-int64(len(v.Data)) {
		return fmt.Errorf("next_offset overflows int64")
	}
	if v.NextOffset != v.Offset+int64(len(v.Data)) {
		return fmt.Errorf("next_offset must equal offset plus data length")
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
	if err := validateFaceEventCorrelation(v); err != nil {
		return err
	}

	switch v.Type {
	case FaceEventChatStarted:
		var data ChatStartedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		return validateEventRequestID(v, data.RequestID)
	case FaceEventChatToolCalled:
		var data ChatToolCalledEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if err := requireFields(data.Tool, "data.tool", data.ArgsDigest, "data.args_digest"); err != nil {
			return err
		}
		return validateEventRequestID(v, data.RequestID)
	case FaceEventChatToolCompleted:
		var data ChatToolCompletedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if err := requireFields(data.Tool, "data.tool"); err != nil {
			return err
		}
		return validateEventRequestID(v, data.RequestID)
	case FaceEventChatCompleted:
		var data ChatCompletedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		return validateEventRequestID(v, data.RequestID)
	case FaceEventChatFailed:
		var data ChatFailedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if err := requireFields(data.RequestID, "data.request_id"); err != nil {
			return err
		}
		if data.RequestID != v.RequestID {
			return fmt.Errorf("data.request_id must match request_id")
		}
		if !validFaceErrorCode(data.Code) {
			return fmt.Errorf("unknown error code %q", data.Code)
		}
	case FaceEventChatCancelled:
		var data ChatCancelledEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		return validateEventRequestID(v, data.RequestID)
	case FaceEventApprovalRequested:
		var data ApprovalRequestedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if err := validateApprovalRequest(data); err != nil {
			return err
		}
		if data.ConversationID != v.ConversationID || data.RequestID != v.RequestID {
			return fmt.Errorf("approval data correlation must match event correlation")
		}
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
		if data.ConversationID != v.ConversationID {
			return fmt.Errorf("data.conversation_id must match conversation_id")
		}
		if data.SnapshotVersion < 1 {
			return fmt.Errorf("data.snapshot_version must be positive")
		}
	case FaceEventTaskChanged:
		var data TaskChangedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if err := validateTaskSummary(data); err != nil {
			return err
		}
		if data.ConversationID != v.ConversationID {
			return fmt.Errorf("data.conversation_id must match conversation_id")
		}
	case FaceEventCompactRequested:
		var data CompactRequestedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if !validCompactTrigger(data.Trigger) {
			return fmt.Errorf("unknown compact trigger %q", data.Trigger)
		}
	case FaceEventCompactStarted:
		var data CompactStartedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if !validCompactTrigger(data.Trigger) || data.FromSeq != 1 || data.ToSeq < data.FromSeq ||
			!validCompactGenerationMode(data.GenerationMode, false) || !validSHA256Digest(data.SourceDigest) || data.Attempt < 1 {
			return fmt.Errorf("compact started data is invalid")
		}
	case FaceEventCompactCompleted:
		var data CompactCompletedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if !validCompactTrigger(data.Trigger) || data.SummaryID == "" || data.FromSeq != 1 || data.ToSeq < data.FromSeq ||
			data.BeforeEstimatedTokens < 0 || data.AfterEstimatedTokens < 0 || data.SummaryBytes < 1 ||
			!validSHA256Digest(data.SourceDigest) || data.DurationMS < 0 || data.ContextVersion < 1 {
			return fmt.Errorf("compact completed data is invalid")
		}
	case FaceEventCompactFailed:
		if err := requireJSONFields(v.Data, "trigger", "reason", "duration_ms", "retry_scheduled"); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		var data CompactFailedEventData
		if err := decodeFacePayload(v.Data, &data); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if !validCompactTrigger(data.Trigger) || !validCompactErrorCode(data.Reason) || data.DurationMS < 0 {
			return fmt.Errorf("compact failed data is invalid")
		}
		hasRange := data.FromSeq != nil || data.ToSeq != nil || data.SourceDigest != ""
		if hasRange && (data.FromSeq == nil || data.ToSeq == nil || *data.FromSeq != 1 || *data.ToSeq < *data.FromSeq || !validSHA256Digest(data.SourceDigest)) {
			return fmt.Errorf("compact failed source fields are incomplete")
		}
		if data.RetryScheduled {
			if data.Reason != FaceErrorCompactRateLimited || data.PendingAttempt == nil || data.RetryNotBefore == nil || *data.PendingAttempt < 0 || *data.RetryNotBefore < 1 {
				return fmt.Errorf("compact failed retry fields are invalid")
			}
		} else if data.PendingAttempt != nil || data.RetryNotBefore != nil {
			return fmt.Errorf("compact failed retry fields require retry_scheduled")
		}
	}
	return nil
}

func validateFaceChatDelta(v FaceChatDelta) error {
	if err := requireFields(v.ConversationID, "conversation_id", v.RequestID, "request_id", v.Delta, "delta"); err != nil {
		return err
	}
	if v.ResponseIndex <= 0 || v.Seq <= 0 || v.Seq > MaxFaceChatStreamChunks || v.Offset < 0 {
		return fmt.Errorf("response_index, seq or offset is invalid")
	}
	if !utf8.ValidString(v.Delta) || len(v.Delta) > MaxFaceChatDeltaBytes {
		return fmt.Errorf("delta must be valid UTF-8 and at most %d bytes", MaxFaceChatDeltaBytes)
	}
	if v.Offset > MaxFaceChatStreamBytes-int64(len(v.Delta)) {
		return fmt.Errorf("delta exceeds stream byte limit")
	}
	return nil
}

func validateFaceChatStreamEnd(v FaceChatStreamEnd) error {
	if err := requireFields(v.ConversationID, "conversation_id", v.RequestID, "request_id"); err != nil {
		return err
	}
	if v.LastSeq < 0 || v.LastSeq > MaxFaceChatStreamChunks || v.ResponseCount < 0 {
		return fmt.Errorf("last_seq or response_count is invalid")
	}
	if !validFaceResultStatus(v.Status) {
		return fmt.Errorf("unknown result status %q", v.Status)
	}
	if v.Complete != (v.Status == FaceResultSucceeded) {
		return fmt.Errorf("complete must match succeeded status")
	}
	return nil
}

func validateFaceRunProgress(v FaceRunProgress) error {
	if err := requireFields(v.ConversationID, "conversation_id", v.RunID, "run_id"); err != nil {
		return err
	}
	return ValidateRPCProgress(RPCProgress{RunID: v.RunID, Seq: v.Seq, Kind: v.Kind, Data: v.Data})
}

func validateEventRequestID(v FaceEvent, requestID string) error {
	if err := requireFields(requestID, "data.request_id"); err != nil {
		return err
	}
	if requestID != v.RequestID {
		return fmt.Errorf("data.request_id must match request_id")
	}
	return nil
}

func validateFaceEventCorrelation(v FaceEvent) error {
	switch v.Type {
	case FaceEventChatStarted, FaceEventChatToolCalled, FaceEventChatToolCompleted,
		FaceEventChatCompleted, FaceEventChatFailed, FaceEventChatCancelled:
		return requireFields(v.ConversationID, "conversation_id", v.RequestID, "request_id")
	case FaceEventApprovalRequested, FaceEventApprovalResolved:
		return requireFields(v.ConversationID, "conversation_id")
	case FaceEventRemoteRunChanged, FaceEventTaskChanged, FaceEventConversationChanged:
		return requireFields(v.ConversationID, "conversation_id")
	case FaceEventCompactRequested, FaceEventCompactStarted, FaceEventCompactCompleted, FaceEventCompactFailed:
		return requireFields(v.ConversationID, "conversation_id", v.RequestID, "request_id")
	case FaceEventHandConnected, FaceEventHandDisconnected:
		if v.ConversationID != "" || v.RequestID != "" {
			return fmt.Errorf("Hand lifecycle event must not contain conversation_id or request_id")
		}
	}
	return nil
}

func validateApprovalRequest(v ApprovalRequest) error {
	if err := requireFields(v.ApprovalID, "approval_id", v.ConversationID, "conversation_id", v.Tool, "tool", v.Reason, "reason", v.ArgsDigest, "args_digest"); err != nil {
		return err
	}
	if v.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at is required")
	}
	if len(v.Reason) > MaxFaceApprovalReasonBytes {
		return fmt.Errorf("approval reason exceeds %d bytes", MaxFaceApprovalReasonBytes)
	}
	return nil
}

func validFaceErrorCode(code FaceErrorCode) bool {
	switch code {
	case FaceErrorInvalidRequest, FaceErrorUnauthorized, FaceErrorForbidden,
		FaceErrorConversationNotFound, FaceErrorRequestConflict, FaceErrorRequestInProgress,
		FaceErrorApprovalNotFound, FaceErrorApprovalExpired, FaceErrorRunNotFound,
		FaceErrorHandNotFound, FaceErrorTaskFailed, FaceErrorTaskCancelled, FaceErrorTaskTimedOut,
		FaceErrorTaskLost, FaceErrorTaskStale, FaceErrorTaskNotFound, FaceErrorHandOffline,
		FaceErrorLogUnavailable, FaceErrorBusy, FaceErrorCancelled, FaceErrorTimeout, FaceErrorInternal:
		return true
	case FaceErrorCompactUnavailable, FaceErrorNothingToCompact, FaceErrorCompactTarget,
		FaceErrorCompactIncrement, FaceErrorCompactRebase, FaceErrorCompactTimeout,
		FaceErrorCompactRateLimited, FaceErrorCompactProvider, FaceErrorCompactResponse,
		FaceErrorCompactConflict, FaceErrorContextLimit, FaceErrorCompactRepair,
		FaceErrorCompactIntegrity, FaceErrorCompactVersion:
		return true
	default:
		return false
	}
}

func validFaceScope(scope FaceScope) bool {
	switch scope {
	case FaceScopeChat, FaceScopeSessionsRead, FaceScopeSessionsWrite, FaceScopeRunsRead,
		FaceScopeRunsCancel, FaceScopeRunsOutput, FaceScopeApprove, FaceScopeHandsRead, FaceScopeTasksRead,
		FaceScopeTasksCancel:
		return true
	default:
		return false
	}
}

func validFaceFeature(feature FaceFeature) bool {
	switch feature {
	case FaceFeatureChatStream, FaceFeatureChatStreamResume, FaceFeatureRunProgress, FaceFeatureMessagePaging,
		FaceFeatureContextCompaction:
		return true
	default:
		return false
	}
}

func validFaceTransientType(typ FaceTransientType) bool {
	switch typ {
	case FaceTransientChatDelta, FaceTransientRunProgress:
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
		FaceEventHandConnected, FaceEventHandDisconnected, FaceEventConversationChanged,
		FaceEventTaskChanged, FaceEventCompactRequested, FaceEventCompactStarted,
		FaceEventCompactCompleted, FaceEventCompactFailed:
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
		FaceOperationChatStreamGet, FaceOperationCapabilitiesGet,
		FaceOperationConversationCreate, FaceOperationConversationSnapshot,
		FaceOperationConversationRename, FaceOperationConversationMessages, FaceOperationSubscribe, FaceOperationApprovalResolve,
		FaceOperationRunGet, FaceOperationRunCancel, FaceOperationHandList, FaceOperationHandGet,
		FaceOperationTaskList, FaceOperationTaskGet, FaceOperationTaskLog, FaceOperationTaskCancel,
		FaceOperationConversationCompact, FaceOperationCompactStatus:
		return true
	default:
		return false
	}
}

func validCompactTrigger(trigger FaceCompactTrigger) bool {
	return trigger == FaceCompactTriggerManual || trigger == FaceCompactTriggerAutomatic
}

func validCompactErrorCode(code FaceErrorCode) bool {
	switch code {
	case FaceErrorCompactUnavailable, FaceErrorNothingToCompact, FaceErrorCompactTarget,
		FaceErrorCompactIncrement, FaceErrorCompactRebase, FaceErrorCompactTimeout,
		FaceErrorCompactRateLimited, FaceErrorCompactProvider, FaceErrorCompactResponse,
		FaceErrorCompactConflict, FaceErrorContextLimit, FaceErrorCompactRepair,
		FaceErrorCompactIntegrity, FaceErrorCompactVersion, FaceErrorInternal:
		return true
	default:
		return false
	}
}

func validSHA256Digest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, char := range value[len("sha256:"):] {
		if char < '0' || char > '9' && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
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
