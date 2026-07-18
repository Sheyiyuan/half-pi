package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTaskMessagesRoundTrip(t *testing.T) {
	messages := []struct {
		typ     string
		payload any
		decode  func(*Envelope) (any, error)
	}{
		{TypeTaskStatusReq, TaskStatusReq{ID: "req-1", TaskID: "run-1"}, decodePayloadAs[TaskStatusReq]},
		{TypeTaskStatusResp, TaskStatusResp{ID: "req-1", TaskID: "run-1", Tool: "exec_command", Status: TaskRunning, CreatedAt: 1000, StartedAt: 1001}, decodePayloadAs[TaskStatusResp]},
		{TypeTaskLogReq, TaskLogReq{ID: "req-2", TaskID: "run-1", Offset: 7, Limit: 1024}, decodePayloadAs[TaskLogReq]},
		{TypeTaskLogResp, TaskLogResp{ID: "req-2", TaskID: "run-1", Offset: 7, NextOffset: 10, Data: []byte{0, 1, 0xff}, EOF: true, Truncated: true}, decodePayloadAs[TaskLogResp]},
		{TypeTaskCancel, TaskCancel{ID: "req-3", TaskID: "run-1", Reason: "user"}, decodePayloadAs[TaskCancel]},
		{TypeTaskCancelResult, TaskCancelResult{ID: "req-3", TaskID: "run-1", Status: TaskCancelCancelled}, decodePayloadAs[TaskCancelResult]},
	}

	for _, tt := range messages {
		t.Run(tt.typ, func(t *testing.T) {
			env, err := NewEnvelope("", tt.typ, tt.payload)
			if err != nil {
				t.Fatal(err)
			}
			got, err := tt.decode(env)
			if err != nil {
				t.Fatal(err)
			}
			wantJSON, _ := json.Marshal(tt.payload)
			gotJSON, _ := json.Marshal(got)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("round trip mismatch: got %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestTaskLogWireUsesBase64Bytes(t *testing.T) {
	env, err := NewEnvelope("msg-1", TypeTaskLogResp, TaskLogResp{
		ID: "req-1", TaskID: "run-1", Offset: 0, NextOffset: 3, Data: []byte{0, 1, 0xff}, EOF: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(env.Payload, []byte(`"data":"AAH/"`)) {
		t.Fatalf("payload does not contain base64 data: %s", env.Payload)
	}
}

func TestTaskValidators(t *testing.T) {
	if err := ValidateTaskStatusReq(TaskStatusReq{ID: "req-1", TaskID: "run-1"}); err != nil {
		t.Fatalf("valid status request rejected: %v", err)
	}
	if err := ValidateTaskStatusReq(TaskStatusReq{}); err == nil {
		t.Fatal("missing task_id must be rejected")
	}

	validRunning := TaskStatusResp{ID: "req-1", TaskID: "run-1", Tool: "exec_command", Status: TaskRunning, CreatedAt: 1000, StartedAt: 1001}
	if err := ValidateTaskStatusResp(validRunning); err != nil {
		t.Fatalf("valid running status rejected: %v", err)
	}
	validFinished := TaskStatusResp{ID: "req-1", TaskID: "run-1", Tool: "exec_command", Status: TaskSucceeded, CreatedAt: 1000, StartedAt: 1001, FinishedAt: 1002}
	if err := ValidateTaskStatusResp(validFinished); err != nil {
		t.Fatalf("valid terminal status rejected: %v", err)
	}
	invalidStatuses := []TaskStatusResp{
		{ID: "req-1", TaskID: "run-1", Tool: "exec_command", Status: "unknown", CreatedAt: 1000},
		{ID: "req-1", TaskID: "run-1", Tool: "exec_command", Status: TaskPending},
		{ID: "req-1", TaskID: "run-1", Tool: "exec_command", Status: TaskRunning, CreatedAt: 1000, FinishedAt: 1001},
		{ID: "req-1", TaskID: "run-1", Tool: "exec_command", Status: TaskSucceeded, CreatedAt: 1000, StartedAt: 1001},
		{ID: "req-1", TaskID: "run-1", Tool: "exec_command", Status: TaskSucceeded, CreatedAt: 1000, StartedAt: 999, FinishedAt: 1002},
	}
	for _, msg := range invalidStatuses {
		if err := ValidateTaskStatusResp(msg); err == nil {
			t.Fatalf("invalid task status accepted: %+v", msg)
		}
	}

	if err := ValidateTaskLogReq(TaskLogReq{ID: "req-1", TaskID: "run-1", Offset: 0, Limit: MaxTaskLogResponseBytes}); err != nil {
		t.Fatalf("maximum valid log request rejected: %v", err)
	}
	invalidLogRequests := []TaskLogReq{
		{ID: "req-1", TaskID: "run-1", Offset: -1, Limit: 1},
		{ID: "req-1", TaskID: "run-1", Limit: 0},
		{ID: "req-1", TaskID: "run-1", Limit: MaxTaskLogResponseBytes + 1},
	}
	for _, msg := range invalidLogRequests {
		if err := ValidateTaskLogReq(msg); err == nil {
			t.Fatalf("invalid log request accepted: %+v", msg)
		}
	}

	data := bytes.Repeat([]byte("x"), MaxTaskLogResponseBytes)
	if err := ValidateTaskLogResp(TaskLogResp{ID: "req-1", TaskID: "run-1", Offset: 9, NextOffset: 9 + int64(len(data)), Data: data}); err != nil {
		t.Fatalf("maximum valid log response rejected: %v", err)
	}
	invalidLogResponses := []TaskLogResp{
		{ID: "req-1", TaskID: "run-1", Offset: -1},
		{ID: "req-1", TaskID: "run-1", Data: nil},
		{ID: "req-1", TaskID: "run-1", Offset: 9, NextOffset: 10, Data: []byte("xx")},
		{ID: "req-1", TaskID: "run-1", Data: bytes.Repeat([]byte("x"), MaxTaskLogResponseBytes+1), NextOffset: MaxTaskLogResponseBytes + 1},
		{ID: "req-1", TaskID: "run-1", Offset: int64(^uint64(0) >> 1), NextOffset: 0, Data: []byte("x")},
	}
	for _, msg := range invalidLogResponses {
		if err := ValidateTaskLogResp(msg); err == nil {
			t.Fatalf("invalid log response accepted: offset=%d next=%d len=%d", msg.Offset, msg.NextOffset, len(msg.Data))
		}
	}

	if err := ValidateTaskCancel(TaskCancel{ID: "req-1", TaskID: "run-1"}); err != nil {
		t.Fatalf("valid cancel rejected: %v", err)
	}
	if err := ValidateTaskCancelResult(TaskCancelResult{ID: "req-1", TaskID: "run-1", Status: TaskCancelUnknownTask}); err != nil {
		t.Fatalf("valid cancel result rejected: %v", err)
	}
	if err := ValidateTaskCancelResult(TaskCancelResult{ID: "req-1", TaskID: "run-1", Status: "unknown"}); err == nil {
		t.Fatal("unknown cancel status must be rejected")
	}
}

func TestValidateRPCBackgroundBounds(t *testing.T) {
	now := time.Unix(100, 0)
	base := RPC{RunID: "run-1", Tool: "tool", Args: map[string]any{}, DeadlineAt: now.Add(time.Minute).UnixMilli()}
	valid := base
	valid.Background = &RPCBackgroundOptions{TaskID: valid.RunID, MaxRuntimeMS: MaxTaskRuntimeMS}
	if err := ValidateRPC(valid, now); err != nil {
		t.Fatalf("maximum valid background RPC rejected: %v", err)
	}

	invalid := []RPC{base, base, base}
	invalid[0].Background = &RPCBackgroundOptions{TaskID: "task-2", MaxRuntimeMS: 1}
	invalid[1].Background = &RPCBackgroundOptions{TaskID: base.RunID}
	invalid[2].Background = &RPCBackgroundOptions{TaskID: base.RunID, MaxRuntimeMS: MaxTaskRuntimeMS + 1}
	for _, rpc := range invalid {
		if err := ValidateRPC(rpc, now); err == nil {
			t.Fatalf("invalid background RPC accepted: %+v", rpc.Background)
		}
	}
}

func TestRPCApprovalDigestBindsBackgroundContract(t *testing.T) {
	foreground := RPC{RunID: "run-1", Tool: "tool", Args: map[string]any{"a": 1}}
	background := foreground
	background.Background = &RPCBackgroundOptions{TaskID: background.RunID, MaxRuntimeMS: 1000}
	digest, err := RPCApprovalDigest(background, "hand-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	approval := &Approval{
		Approved: true, Source: "user", OneShot: true, ArgsDigest: digest,
		ApprovedAt: now.Add(-time.Second).UnixMilli(), ExpiresAt: now.Add(time.Second).UnixMilli(),
	}
	if err := ValidateRPCApproval(approval, background, "hand-1", now); err != nil {
		t.Fatalf("valid background approval rejected: %v", err)
	}

	changes := []RPC{foreground, background, background}
	changes[1].Background = &RPCBackgroundOptions{TaskID: "run-2", MaxRuntimeMS: 1000}
	changes[2].Background = &RPCBackgroundOptions{TaskID: "run-1", MaxRuntimeMS: 1001}
	for _, changed := range changes {
		if err := ValidateRPCApproval(approval, changed, "hand-1", now); !errors.Is(err, ErrApprovalDigestMismatch) {
			t.Fatalf("changed background scope error = %v", err)
		}
	}

	foregroundDigest, err := RPCApprovalDigest(foreground, "hand-1")
	if err != nil {
		t.Fatal(err)
	}
	if digest == foregroundDigest {
		t.Fatal("foreground and background RPC must have different digests")
	}
	legacyDigest, err := ApprovalDigest(foreground.RunID, "hand-1", foreground.Tool, foreground.Args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(legacyDigest) == "" || legacyDigest != foregroundDigest {
		t.Fatal("legacy foreground digest must remain unchanged")
	}
}

func TestValidateRPCResultTaskFields(t *testing.T) {
	if err := ValidateRPCResult(RPCResult{RunID: "run-1", Success: true}); err != nil {
		t.Fatalf("foreground result rejected: %v", err)
	}
	if err := ValidateRPCResult(RPCResult{RunID: "run-1", Success: true, TaskID: "run-1", TaskStatus: TaskRunning}); err != nil {
		t.Fatalf("background result rejected: %v", err)
	}
	invalid := []RPCResult{
		{RunID: "run-1", TaskID: "run-1"},
		{RunID: "run-1", TaskStatus: TaskRunning},
		{RunID: "run-1", TaskID: "run-2", TaskStatus: TaskRunning},
		{RunID: "run-1", TaskID: "run-1", TaskStatus: "unknown"},
	}
	for _, msg := range invalid {
		if err := ValidateRPCResult(msg); err == nil {
			t.Fatalf("invalid RPC result accepted: %+v", msg)
		}
	}
}

func decodePayloadAs[T any](env *Envelope) (any, error) {
	return DecodePayload[T](env)
}
