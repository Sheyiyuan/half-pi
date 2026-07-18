//go:build !windows

package e2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

const faceMessageTimeout = 15 * time.Second

type faceClient struct {
	process *testProcess
	encoder *json.Encoder
	backlog []protocol.Envelope
}

func startHeadlessFace(t *testing.T, binary, dir, home, configPath, name string) *faceClient {
	t.Helper()
	process := startTestProcess(t, name, binary, dir, home, "--config", configPath)
	client := &faceClient{process: process, encoder: json.NewEncoder(process.stdin)}
	registered := awaitPayload(t, client, protocol.TypeRegistered, func(value protocol.Registered) bool {
		return value.ClientID != "" && value.SessionID != ""
	})
	if registered.ProtocolVersion != protocol.ProtocolVersion {
		t.Fatalf("%s registered protocol version = %d", name, registered.ProtocolVersion)
	}
	return client
}

func (c *faceClient) send(t *testing.T, typ string, payload any) {
	t.Helper()
	envelope, err := protocol.NewEnvelope("", typ, payload)
	if err != nil {
		t.Fatalf("create %s command: %v", typ, err)
	}
	if err := protocol.ValidateFacePayload(typ, envelope.Payload); err != nil {
		t.Fatalf("validate %s command: %v", typ, err)
	}
	if err := c.encoder.Encode(envelope); err != nil {
		t.Fatalf("send %s command: %v\n%s", typ, err, c.process.diagnostics())
	}
}

func (c *faceClient) await(t *testing.T, typ string, predicate func(protocol.Envelope) bool) protocol.Envelope {
	t.Helper()
	for index, envelope := range c.backlog {
		if envelope.Type == typ && (predicate == nil || predicate(envelope)) {
			c.backlog = append(c.backlog[:index], c.backlog[index+1:]...)
			return envelope
		}
	}
	deadline := time.Now().Add(faceMessageTimeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("%s timed out waiting for %s\n%s", c.process.name, typ, c.process.diagnostics())
		}
		line, err := c.process.stdout.next(remaining)
		if err != nil {
			waitErr := c.process.wait(time.Second)
			t.Fatalf("%s read %s: %v (process: %v)\n%s", c.process.name, typ, err, waitErr, c.process.diagnostics())
		}
		envelope, err := protocol.StrictDecode[protocol.Envelope]([]byte(line))
		if err != nil {
			t.Fatalf("%s emitted invalid JSONL envelope: %v\nline: %s\n%s", c.process.name, err, line, c.process.diagnostics())
		}
		if envelope.Type != protocol.TypeRegistered {
			if !protocol.IsFaceServerMessageType(envelope.Type) {
				t.Fatalf("%s emitted unexpected message type %q", c.process.name, envelope.Type)
			}
			if err := protocol.ValidateFacePayload(envelope.Type, envelope.Payload); err != nil {
				t.Fatalf("%s emitted invalid %s payload: %v", c.process.name, envelope.Type, err)
			}
		}
		if envelope.Type == typ && (predicate == nil || predicate(envelope)) {
			return envelope
		}
		c.backlog = append(c.backlog, envelope)
	}
}

func (c *faceClient) stop(t *testing.T) {
	t.Helper()
	if err := c.process.closeInputAndWait(); err != nil {
		t.Fatalf("stop %s: %v\n%s", c.process.name, err, c.process.diagnostics())
	}
}

func awaitPayload[T any](t *testing.T, client *faceClient, typ string, predicate func(T) bool) T {
	t.Helper()
	var matched T
	client.await(t, typ, func(envelope protocol.Envelope) bool {
		value, err := protocol.DecodePayload[T](&envelope)
		if err != nil {
			t.Fatalf("decode %s payload: %v", typ, err)
		}
		if predicate != nil && !predicate(value) {
			return false
		}
		matched = value
		return true
	})
	return matched
}

func awaitAccepted(t *testing.T, client *faceClient, requestID string, operation protocol.FaceOperation) protocol.FaceAccepted {
	t.Helper()
	return awaitPayload(t, client, protocol.TypeFaceAccepted, func(value protocol.FaceAccepted) bool {
		return value.RequestID == requestID && value.Operation == operation
	})
}

func awaitResult(t *testing.T, client *faceClient, requestID string) protocol.FaceResult {
	t.Helper()
	return awaitPayload(t, client, protocol.TypeFaceResult, func(value protocol.FaceResult) bool {
		return value.RequestID == requestID
	})
}

func awaitFaceError(t *testing.T, client *faceClient, requestID string) protocol.FaceError {
	t.Helper()
	return awaitPayload(t, client, protocol.TypeFaceError, func(value protocol.FaceError) bool {
		return value.RequestID == requestID
	})
}

func awaitSnapshot(t *testing.T, client *faceClient, requestID string) protocol.ConversationSnapshot {
	t.Helper()
	payload := awaitPayload(t, client, protocol.TypeFaceSnapshot, func(value protocol.FaceSnapshot) bool {
		return value.RequestID == requestID
	})
	return payload.Snapshot
}

func awaitEvent(t *testing.T, client *faceClient, typ protocol.FaceEventType, predicate func(protocol.FaceEvent) bool) protocol.FaceEvent {
	t.Helper()
	return awaitPayload(t, client, protocol.TypeFaceEvent, func(value protocol.FaceEvent) bool {
		return value.Type == typ && (predicate == nil || predicate(value))
	})
}

func decodeEventData[T any](t *testing.T, event protocol.FaceEvent) T {
	t.Helper()
	value, err := protocol.StrictDecode[T](event.Data)
	if err != nil {
		t.Fatalf("decode %s event data: %v", event.Type, err)
	}
	return value
}

func requireSuccessfulResult(t *testing.T, result protocol.FaceResult, operation protocol.FaceOperation) {
	t.Helper()
	if result.Status != protocol.FaceResultSucceeded {
		t.Fatalf("%s result = %+v", operation, result)
	}
	if len(result.Data) > 0 {
		if err := protocol.ValidateFaceResultData(operation, result.Data); err != nil {
			t.Fatalf("%s result data: %v", operation, err)
		}
	}
}
