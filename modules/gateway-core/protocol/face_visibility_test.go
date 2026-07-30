package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFaceV3ToolVisibilityPayloads(t *testing.T) {
	if FaceProtocolRevision != 3 {
		t.Fatalf("FaceProtocolRevision = %d", FaceProtocolRevision)
	}
	assertFacePayloadValidity(t, TypeFaceSubscribe, FaceSubscribe{RequestID: "subscribe", DetailMode: FaceDetailModeTransparent}, true)
	assertFacePayloadValidity(t, TypeFaceSubscribe, json.RawMessage(`{"request_id":"subscribe","detail_mode":"raw"}`), false)

	args := &ToolArgsView{
		ProjectionVersion: "tool-display.v1", Bytes: 12, Fields: map[string]ToolFieldView{
			"command": {State: ToolDisplayShow, Value: "pwd", Bytes: 5},
			"token":   {State: ToolDisplayMask, Value: "[masked]", Bytes: 8},
		}, Warnings: []string{},
	}
	event := FaceEvent{
		EventSeq: 1, ConversationID: "conversation", RequestID: "chat", Type: FaceEventChatToolCalled,
		Source: "chat", Level: FaceEventLevelInfo, Message: "called", Timestamp: time.Now().UTC(),
		Data: mustVisibilityJSON(t, ChatToolCalledEventData{
			RequestID: "chat", Tool: "exec_command", ArgsDigest: "sha256:args", Args: args,
			ProjectionVersion: args.ProjectionVersion, ScanWarnings: []string{}, ArgsBytes: args.Bytes,
		}),
	}
	assertFacePayloadValidity(t, TypeFaceEvent, event, true)
	args.Fields["token"] = ToolFieldView{State: "redact"}
	event.Data = mustVisibilityJSON(t, ChatToolCalledEventData{
		RequestID: "chat", Tool: "exec_command", ArgsDigest: "sha256:args", Args: args,
		ProjectionVersion: args.ProjectionVersion,
	})
	assertFacePayloadValidity(t, TypeFaceEvent, event, false)

	assertFacePayloadValidity(t, TypeFaceChatToolProgress, FaceChatToolProgress{
		ConversationID: "conversation", RequestID: "chat", Tool: "exec_command",
		Seq: 1, Kind: "stdout", Data: "chunk",
	}, true)

	output := &ToolOutputView{
		Stdout: "ok", StdoutBytes: 2, OutputBytes: 2,
		Digest: "sha256:0000000000000000000000000000000000000000000000000000000000000000", Warnings: []string{},
	}
	event.Type = FaceEventChatToolCompleted
	event.Data = mustVisibilityJSON(t, ChatToolCompletedEventData{
		RequestID: "chat", Tool: "exec_command", Success: true, Result: output,
		ProjectionVersion: FaceToolDisplayProjectionVersion, OutputBytes: output.OutputBytes,
		OutputDigest: output.Digest, ScanWarnings: []string{},
	})
	assertFacePayloadValidity(t, TypeFaceEvent, event, true)
	output.Digest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	event.Data = mustVisibilityJSON(t, ChatToolCompletedEventData{
		RequestID: "chat", Tool: "exec_command", Success: true, Result: output,
		ProjectionVersion: FaceToolDisplayProjectionVersion, OutputBytes: output.OutputBytes,
		OutputDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000", ScanWarnings: []string{},
	})
	assertFacePayloadValidity(t, TypeFaceEvent, event, false)
}

func mustVisibilityJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
