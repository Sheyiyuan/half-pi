package facegateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/conversation"
)

const (
	chatRequestRetention = 10 * time.Minute
	maxTerminalRequests  = 256
)

type requestKey struct {
	identityID string
	requestID  string
}

type requestRecord struct {
	key             requestKey
	operation       protocol.FaceOperation
	digest          [sha256.Size]byte
	conversationID  string
	accepted        protocol.FaceAccepted
	result          protocol.FaceResult
	startedAt       time.Time
	completedAt     time.Time
	terminal        bool
	cancelRequested bool
	ctx             context.Context
	cancel          context.CancelFunc
	origin          *connection
	streamResponses []streamResponseState
	streamSeq       int64
	streamBytes     int
	lease           *conversation.OperationLease
	compactErrors   bool
}

type streamResponseState struct {
	content  string
	complete bool
}

type requestAdmission struct {
	record    *requestRecord
	accepted  *protocol.FaceAccepted
	result    *protocol.FaceResult
	code      protocol.FaceErrorCode
	message   string
	retryable bool
}

type cancelAdmission struct {
	requestAdmission
	target          *requestRecord
	alreadyTerminal bool
}

type chatRegistry struct {
	mu       sync.Mutex
	requests map[requestKey]*requestRecord
	active   map[string]*requestRecord
}

func newChatRegistry() *chatRegistry {
	return &chatRegistry{
		requests: make(map[requestKey]*requestRecord),
		active:   make(map[string]*requestRecord),
	}
}

func faceCommandDigest(operation protocol.FaceOperation, payload any) ([sha256.Size]byte, error) {
	data, err := json.Marshal(struct {
		Operation protocol.FaceOperation `json:"operation"`
		Payload   any                    `json:"payload"`
	}{Operation: operation, Payload: payload})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(data), nil
}

func (r *chatRegistry) beginChat(identity protocol.FaceIdentity, request protocol.FaceChat, digest [sha256.Size]byte, origin *connection, actor *conversation.Actor, compactErrors bool) requestAdmission {
	now := time.Now().UTC()
	key := requestKey{identityID: identity.ID, requestID: request.RequestID}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	if existing := r.requests[key]; existing != nil {
		return replayRequest(existing, protocol.FaceOperationChat, digest)
	}
	var lease *conversation.OperationLease
	if actor != nil {
		var ok bool
		lease, ok = actor.AdmitChat(request.RequestID)
		if !ok {
			return requestAdmission{code: protocol.FaceErrorBusy, message: "conversation is busy", retryable: true}
		}
	} else if r.active[request.ConversationID] != nil {
		return requestAdmission{code: protocol.FaceErrorBusy, message: "conversation is busy", retryable: true}
	}
	ctx, cancel := context.WithCancel(context.Background())
	record := &requestRecord{
		key: key, operation: protocol.FaceOperationChat, digest: digest,
		conversationID: request.ConversationID,
		accepted: protocol.FaceAccepted{
			RequestID: request.RequestID, ConversationID: request.ConversationID,
			Operation: protocol.FaceOperationChat,
		},
		startedAt: now, ctx: ctx, cancel: cancel, origin: origin, lease: lease, compactErrors: compactErrors,
	}
	r.requests[key] = record
	r.active[request.ConversationID] = record
	return requestAdmission{record: record}
}

func (r *chatRegistry) beginCompact(identity protocol.FaceIdentity, request protocol.FaceConversationCompact, digest [sha256.Size]byte, origin *connection, actor *conversation.Actor) requestAdmission {
	now := time.Now().UTC()
	key := requestKey{identityID: identity.ID, requestID: request.RequestID}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	if existing := r.requests[key]; existing != nil {
		return replayRequest(existing, protocol.FaceOperationConversationCompact, digest)
	}
	lease, ok := actor.AdmitCompact(request.RequestID)
	if !ok {
		return requestAdmission{code: protocol.FaceErrorBusy, message: "conversation is busy", retryable: true}
	}
	record := &requestRecord{
		key: key, operation: protocol.FaceOperationConversationCompact, digest: digest,
		conversationID: request.ConversationID,
		accepted: protocol.FaceAccepted{
			RequestID: request.RequestID, ConversationID: request.ConversationID,
			Operation: protocol.FaceOperationConversationCompact,
		},
		startedAt: now, ctx: context.Background(), origin: origin, lease: lease, compactErrors: true,
	}
	r.requests[key] = record
	return requestAdmission{record: record}
}

func (r *chatRegistry) appendStreamText(record *requestRecord, responseIndex int, delta string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.requests[record.key] != record || record.terminal {
		return 0, fmt.Errorf("Chat stream is no longer active")
	}
	if responseIndex <= 0 || responseIndex > len(record.streamResponses)+1 {
		return 0, fmt.Errorf("response index is not contiguous")
	}
	if responseIndex == len(record.streamResponses)+1 {
		record.streamResponses = append(record.streamResponses, streamResponseState{})
	}
	response := &record.streamResponses[responseIndex-1]
	if response.complete {
		return 0, fmt.Errorf("response is already complete")
	}
	if delta == "" || record.streamBytes > protocol.MaxFaceChatStreamBytes-len(delta) {
		return 0, fmt.Errorf("Chat stream exceeds %d bytes", protocol.MaxFaceChatStreamBytes)
	}
	offset := int64(len(response.content))
	response.content += delta
	record.streamBytes += len(delta)
	return offset, nil
}

func (r *chatRegistry) emitStreamChunk(record *requestRecord, responseIndex int, offset int64, delta string) (protocol.FaceChatDelta, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.requests[record.key] != record || record.terminal {
		return protocol.FaceChatDelta{}, fmt.Errorf("Chat stream is no longer active")
	}
	if responseIndex <= 0 || responseIndex > len(record.streamResponses) || delta == "" || len(delta) > protocol.MaxFaceChatDeltaBytes {
		return protocol.FaceChatDelta{}, fmt.Errorf("invalid Chat stream chunk")
	}
	response := record.streamResponses[responseIndex-1]
	if offset < 0 || offset > int64(len(response.content))-int64(len(delta)) || response.content[offset:offset+int64(len(delta))] != delta {
		return protocol.FaceChatDelta{}, fmt.Errorf("Chat stream chunk does not match retained content")
	}
	if record.streamSeq >= protocol.MaxFaceChatStreamChunks {
		return protocol.FaceChatDelta{}, fmt.Errorf("Chat stream exceeds %d chunks", protocol.MaxFaceChatStreamChunks)
	}
	record.streamSeq++
	return protocol.FaceChatDelta{
		ConversationID: record.conversationID, RequestID: record.key.requestID,
		ResponseIndex: responseIndex, Seq: record.streamSeq, Offset: offset, Delta: delta,
	}, nil
}

func (r *chatRegistry) completeStreamResponse(record *requestRecord, responseIndex int, content string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.requests[record.key] != record || record.terminal || responseIndex <= 0 || responseIndex > len(record.streamResponses)+1 {
		return fmt.Errorf("Chat stream response is unavailable")
	}
	if responseIndex == len(record.streamResponses)+1 {
		record.streamResponses = append(record.streamResponses, streamResponseState{})
	}
	response := &record.streamResponses[responseIndex-1]
	if response.complete || response.content != content {
		return fmt.Errorf("Chat stream response content mismatch")
	}
	response.complete = true
	return nil
}

func (r *chatRegistry) streamSnapshot(conversationID, requestID string) (protocol.ChatStreamGetResult, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(time.Now().UTC())
	for _, record := range r.requests {
		if record.operation != protocol.FaceOperationChat || record.conversationID != conversationID || record.key.requestID != requestID {
			continue
		}
		result := protocol.ChatStreamGetResult{
			TargetRequestID: requestID, LastSeq: record.streamSeq,
			Responses: make([]protocol.ChatStreamResponse, len(record.streamResponses)),
			Terminal:  record.terminal,
		}
		for index, response := range record.streamResponses {
			result.Responses[index] = protocol.ChatStreamResponse{
				ResponseIndex: index + 1, Content: response.content, Complete: response.complete,
			}
		}
		if record.terminal {
			result.Status = record.result.Status
		}
		return result, true
	}
	return protocol.ChatStreamGetResult{}, false
}

func streamEndLocked(record *requestRecord, result protocol.FaceResult) protocol.FaceChatStreamEnd {
	return protocol.FaceChatStreamEnd{
		ConversationID: record.conversationID, RequestID: record.key.requestID,
		LastSeq: record.streamSeq, ResponseCount: len(record.streamResponses),
		Complete: result.Status == protocol.FaceResultSucceeded, Status: result.Status,
	}
}

func (r *chatRegistry) beginCancel(identity protocol.FaceIdentity, request protocol.FaceChatCancel, digest [sha256.Size]byte, origin *connection) cancelAdmission {
	now := time.Now().UTC()
	key := requestKey{identityID: identity.ID, requestID: request.RequestID}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	if existing := r.requests[key]; existing != nil {
		return cancelAdmission{requestAdmission: replayRequest(existing, protocol.FaceOperationChatCancel, digest)}
	}

	target := r.active[request.ConversationID]
	if target != nil && target.key.requestID != request.TargetRequestID {
		target = nil
	}
	alreadyTerminal := false
	if target == nil {
		for _, candidate := range r.requests {
			if candidate.operation == protocol.FaceOperationChat && candidate.conversationID == request.ConversationID &&
				candidate.key.requestID == request.TargetRequestID && candidate.terminal {
				target = candidate
				alreadyTerminal = true
				break
			}
		}
	}
	if target == nil {
		return cancelAdmission{requestAdmission: requestAdmission{
			code: protocol.FaceErrorInvalidRequest, message: "target Chat was not found",
		}}
	}
	if !alreadyTerminal && target.cancelRequested {
		return cancelAdmission{requestAdmission: requestAdmission{
			code: protocol.FaceErrorRequestInProgress, message: "Chat cancellation is already in progress", retryable: true,
		}}
	}
	record := &requestRecord{
		key: key, operation: protocol.FaceOperationChatCancel, digest: digest,
		conversationID: request.ConversationID,
		accepted: protocol.FaceAccepted{
			RequestID: request.RequestID, ConversationID: request.ConversationID,
			Operation: protocol.FaceOperationChatCancel,
		},
		startedAt: now, origin: origin,
	}
	r.requests[key] = record
	if !alreadyTerminal {
		target.cancelRequested = true
	}
	return cancelAdmission{requestAdmission: requestAdmission{record: record}, target: target, alreadyTerminal: alreadyTerminal}
}

func replayRequest(existing *requestRecord, operation protocol.FaceOperation, digest [sha256.Size]byte) requestAdmission {
	if existing.operation != operation || existing.digest != digest {
		return requestAdmission{code: protocol.FaceErrorRequestConflict, message: "request ID is already bound to different payload"}
	}
	if existing.terminal {
		result := existing.result
		return requestAdmission{result: &result}
	}
	accepted := existing.accepted
	return requestAdmission{accepted: &accepted}
}

func (r *chatRegistry) abortChat(record *requestRecord) {
	r.mu.Lock()
	if r.requests[record.key] == record && !record.terminal {
		delete(r.requests, record.key)
		if r.active[record.conversationID] == record {
			delete(r.active, record.conversationID)
		}
	}
	r.mu.Unlock()
	if record.cancel != nil {
		record.cancel()
	}
	if record.lease != nil {
		record.lease.Release()
	}
}

func (r *chatRegistry) abortCompact(record *requestRecord) { r.abortChat(record) }

func (r *chatRegistry) abortCancel(record, target *requestRecord) {
	r.mu.Lock()
	if r.requests[record.key] == record && !record.terminal {
		delete(r.requests, record.key)
		if target != nil && !target.terminal {
			target.cancelRequested = false
		}
	}
	r.mu.Unlock()
}

func (r *chatRegistry) complete(record *requestRecord, result protocol.FaceResult) (*connection, bool) {
	r.mu.Lock()
	if r.requests[record.key] != record || record.terminal {
		r.mu.Unlock()
		return nil, false
	}
	origin, cancel := r.completeLocked(record, result)
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return origin, true
}

func (r *chatRegistry) completeChat(record *requestRecord, result protocol.FaceResult, publish func(protocol.FaceResult)) (protocol.FaceResult, *connection, bool) {
	r.mu.Lock()
	if r.requests[record.key] != record || record.terminal {
		r.mu.Unlock()
		return protocol.FaceResult{}, nil, false
	}
	if record.cancelRequested {
		result = protocol.FaceResult{
			RequestID: record.key.requestID, ConversationID: record.conversationID,
			Status: protocol.FaceResultCancelled, ErrorCode: protocol.FaceErrorCancelled,
			Error: "Chat was cancelled",
		}
	}
	origin, cancel := r.completeLocked(record, result)
	// 保持终态事件先于任何并发 replay result 入队；回调不得重新进入 registry。
	if publish != nil {
		publish(result)
	}
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return result, origin, true
}

func (r *chatRegistry) completeLocked(record *requestRecord, result protocol.FaceResult) (*connection, context.CancelFunc) {
	record.result = result
	record.terminal = true
	record.completedAt = time.Now().UTC()
	if r.active[record.conversationID] == record {
		delete(r.active, record.conversationID)
	}
	origin, cancel := record.origin, record.cancel
	record.origin, record.ctx, record.cancel = nil, nil, nil
	record.lease = nil
	return origin, cancel
}

func (r *chatRegistry) cancelTarget(record *requestRecord) bool {
	r.mu.Lock()
	if record.terminal || record.cancel == nil {
		r.mu.Unlock()
		return false
	}
	cancel := record.cancel
	r.mu.Unlock()
	cancel()
	return true
}

func (r *chatRegistry) activeChats(conversationID string) []protocol.ChatSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.active[conversationID]
	if record == nil {
		return []protocol.ChatSummary{}
	}
	return []protocol.ChatSummary{{RequestID: record.key.requestID, StartedAt: record.startedAt}}
}

func (r *chatRegistry) pruneLocked(now time.Time) {
	terminal := make([]*requestRecord, 0)
	for key, record := range r.requests {
		if !record.terminal {
			continue
		}
		if now.Sub(record.completedAt) > chatRequestRetention {
			delete(r.requests, key)
			continue
		}
		terminal = append(terminal, record)
	}
	if len(terminal) <= maxTerminalRequests {
		return
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].completedAt.Before(terminal[j].completedAt) })
	for _, record := range terminal[:len(terminal)-maxTerminalRequests] {
		delete(r.requests, record.key)
	}
}
