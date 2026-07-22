package facegateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestChatRegistryAcceptedCancellationWinsCompletionRace(t *testing.T) {
	registry := newChatRegistry()
	identity := chatIdentity()
	chat := protocol.FaceChat{RequestID: "chat-race", ConversationID: "conv-race", Content: "finish"}
	chatDigest, err := faceCommandDigest(protocol.FaceOperationChat, chat)
	if err != nil {
		t.Fatal(err)
	}
	chatAdmission := registry.beginChat(identity, chat, chatDigest, nil, nil, false)
	cancel := protocol.FaceChatCancel{
		RequestID: "cancel-race", TargetRequestID: chat.RequestID, ConversationID: chat.ConversationID,
	}
	cancelDigest, err := faceCommandDigest(protocol.FaceOperationChatCancel, cancel)
	if err != nil {
		t.Fatal(err)
	}
	cancelAdmission := registry.beginCancel(identity, cancel, cancelDigest, nil)
	if cancelAdmission.record == nil || cancelAdmission.target != chatAdmission.record || cancelAdmission.alreadyTerminal {
		t.Fatalf("cancel admission = %+v", cancelAdmission)
	}

	candidate := protocol.FaceResult{
		RequestID: chat.RequestID, ConversationID: chat.ConversationID,
		Status: protocol.FaceResultSucceeded, Content: "too late",
	}
	published := false
	publishedTerminal := false
	result, _, completed := registry.completeChat(chatAdmission.record, candidate, func(finalResult protocol.FaceResult) {
		published = true
		publishedTerminal = chatAdmission.record.terminal && finalResult.Status == protocol.FaceResultCancelled
	})
	if !completed || result.Status != protocol.FaceResultCancelled || result.ErrorCode != protocol.FaceErrorCancelled || result.Content != "" {
		t.Fatalf("completion race result = %+v, completed = %t", result, completed)
	}
	if !published || !publishedTerminal {
		t.Fatalf("terminal publish state = published %t, terminal %t", published, publishedTerminal)
	}
	if active := registry.activeChats(chat.ConversationID); len(active) != 0 {
		t.Fatalf("terminal Chat remained active: %+v", active)
	}
}

func TestChatRegistryPrunesExpiredAndExcessTerminalRequests(t *testing.T) {
	registry := newChatRegistry()
	now := time.Now().UTC()
	expiredKey := requestKey{identityID: "principal", requestID: "expired"}
	registry.requests[expiredKey] = &requestRecord{
		key: expiredKey, terminal: true, completedAt: now.Add(-chatRequestRetention - time.Second),
	}
	for index := 0; index < maxTerminalRequests+5; index++ {
		key := requestKey{identityID: "principal", requestID: fmt.Sprintf("recent-%03d", index)}
		registry.requests[key] = &requestRecord{
			key: key, terminal: true, completedAt: now.Add(time.Duration(index) * time.Millisecond),
		}
	}

	registry.mu.Lock()
	registry.pruneLocked(now)
	registry.mu.Unlock()
	if len(registry.requests) != maxTerminalRequests {
		t.Fatalf("retained terminal requests = %d, want %d", len(registry.requests), maxTerminalRequests)
	}
	if registry.requests[expiredKey] != nil {
		t.Fatal("expired terminal request was retained")
	}
	newestKey := requestKey{identityID: "principal", requestID: fmt.Sprintf("recent-%03d", maxTerminalRequests+4)}
	if registry.requests[newestKey] == nil {
		t.Fatal("newest terminal request was pruned")
	}
}
