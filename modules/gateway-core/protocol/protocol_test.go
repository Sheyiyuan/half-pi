package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEnvelopeAADBindsHeader(t *testing.T) {
	base := Envelope{
		MsgID:     "msg-1",
		Type:      TypeRPC,
		SessionID: "session-1",
		From:      "mind",
		To:        "hand-1",
		Seq:       1,
	}

	same := base
	if !bytes.Equal(base.AAD(), same.AAD()) {
		t.Fatal("same envelope header should produce stable AAD")
	}

	changedMsgID := base
	changedMsgID.MsgID = "msg-2"
	if bytes.Equal(base.AAD(), changedMsgID.AAD()) {
		t.Fatal("AAD should change when msg_id changes")
	}

	changedSeq := base
	changedSeq.Seq = 2
	if bytes.Equal(base.AAD(), changedSeq.AAD()) {
		t.Fatal("AAD should change when seq changes")
	}
}

func TestRPCMessagesRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	digest, err := ApprovalDigest("run-1", "hand-1", "write_file", map[string]any{"path": "a", "content": "b"})
	if err != nil {
		t.Fatal(err)
	}
	messages := []struct {
		typ     string
		payload any
	}{
		{TypeRPC, RPC{RunID: "run-1", Tool: "write_file", Args: map[string]any{"path": "a", "content": "b"}, DeadlineAt: now.Add(time.Minute).UnixMilli(), Approval: &Approval{Approved: true, Source: "user", OneShot: true, ArgsDigest: digest, ApprovedAt: now.UnixMilli(), ExpiresAt: now.Add(time.Minute).UnixMilli()}}},
		{TypeRPCAccepted, RPCAccepted{RunID: "run-1", StartedAt: now.UnixMilli()}},
		{TypeRPCRejected, RPCRejected{RunID: "run-1", Code: RejectDenyTools, Reason: "denied"}},
		{TypeRPCProgress, RPCProgress{RunID: "run-1", Seq: 1, Kind: ProgressStdout, Data: "working"}},
		{TypeRPCResult, RPCResult{RunID: "run-1", Success: true, Output: "ok", Truncated: true}},
		{TypeRPCCancel, RPCCancel{RunID: "run-1", Reason: "user"}},
		{TypeRPCCancelResult, RPCCancelResult{RunID: "run-1", Status: CancelCancelled}},
	}
	for _, tt := range messages {
		t.Run(tt.typ, func(t *testing.T) {
			env, err := NewEnvelope("", tt.typ, tt.payload)
			if err != nil {
				t.Fatal(err)
			}
			var got any
			switch tt.typ {
			case TypeRPC:
				got, err = DecodePayload[RPC](env)
			case TypeRPCAccepted:
				got, err = DecodePayload[RPCAccepted](env)
			case TypeRPCRejected:
				got, err = DecodePayload[RPCRejected](env)
			case TypeRPCProgress:
				got, err = DecodePayload[RPCProgress](env)
			case TypeRPCResult:
				got, err = DecodePayload[RPCResult](env)
			case TypeRPCCancel:
				got, err = DecodePayload[RPCCancel](env)
			case TypeRPCCancelResult:
				got, err = DecodePayload[RPCCancelResult](env)
			}
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

func TestApprovalDigestContract(t *testing.T) {
	first, err := ApprovalDigest("run-1", "hand-1", "tool", map[string]any{"b": 2, "a": 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ApprovalDigest("run-1", "hand-1", "tool", map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("map key order must not change digest")
	}
	const want = "cf0a3ad6b9917e2bfdf4dc42a44c86b4051f228dd1b0186d1d6b415cbbc1b73e"
	if first != want {
		t.Fatalf("digest = %q, want fixed vector %q", first, want)
	}
	changes := []struct {
		runID, handID, tool string
		args                map[string]any
	}{
		{"run-2", "hand-1", "tool", map[string]any{"a": 1, "b": 2}},
		{"run-1", "hand-2", "tool", map[string]any{"a": 1, "b": 2}},
		{"run-1", "hand-1", "other", map[string]any{"a": 1, "b": 2}},
		{"run-1", "hand-1", "tool", map[string]any{"a": 1, "b": 3}},
	}
	for _, change := range changes {
		got, err := ApprovalDigest(change.runID, change.handID, change.tool, change.args)
		if err != nil {
			t.Fatal(err)
		}
		if got == first {
			t.Fatalf("changed approval scope produced same digest: %+v", change)
		}
	}
}

func TestApprovalDigestPreservesLargeJSONNumber(t *testing.T) {
	const payload = `{"run_id":"run-large","tool":"tool","args":{"value":9007199254740993},"deadline_at":9999999999999}`
	env := Envelope{Payload: json.RawMessage(payload)}
	rpc, err := DecodePayload[RPC](&env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ApprovalDigest(rpc.RunID, "hand-1", rpc.Tool, rpc.Args)
	if err != nil {
		t.Fatal(err)
	}
	want, err := ApprovalDigest("run-large", "hand-1", "tool", map[string]any{"value": json.Number("9007199254740993")})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("large-number digest mismatch: got %s, want %s", got, want)
	}
}

func TestValidateApproval(t *testing.T) {
	now := time.Unix(100, 0)
	args := map[string]any{"path": "README.md"}
	digest, _ := ApprovalDigest("run-1", "hand-1", "read_file", args)
	valid := &Approval{Approved: true, Source: "user", OneShot: true, ArgsDigest: digest, ApprovedAt: now.Add(-time.Second).UnixMilli(), ExpiresAt: now.Add(time.Second).UnixMilli()}
	if err := ValidateApproval(valid, "run-1", "hand-1", "read_file", args, now); err != nil {
		t.Fatalf("valid approval rejected: %v", err)
	}
	if err := ValidateApproval(nil, "run-1", "hand-1", "read_file", args, now); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("nil approval error = %v", err)
	}
	expired := *valid
	expired.ExpiresAt = now.Add(-time.Millisecond).UnixMilli()
	if err := ValidateApproval(&expired, "run-1", "hand-1", "read_file", args, now); !errors.Is(err, ErrApprovalExpired) {
		t.Fatalf("expired approval error = %v", err)
	}
	future := *valid
	future.ApprovedAt = now.Add(time.Second).UnixMilli()
	future.ExpiresAt = now.Add(2 * time.Second).UnixMilli()
	if err := ValidateApproval(&future, "run-1", "hand-1", "read_file", args, now); !errors.Is(err, ErrApprovalExpired) {
		t.Fatalf("future approval error = %v", err)
	}
	if err := ValidateApproval(valid, "run-1", "hand-2", "read_file", args, now); !errors.Is(err, ErrApprovalDigestMismatch) {
		t.Fatalf("wrong scope error = %v", err)
	}
}

func TestRunStatusTransitions(t *testing.T) {
	statuses := []RunStatus{
		RunCreated, RunApproved, RunSent, RunAccepted, RunRunning,
		RunSucceeded, RunFailed, RunRejected, RunCancelRequested,
		RunCancelled, RunTimedOut, RunLost,
	}
	want := map[[2]RunStatus]bool{
		{RunCreated, RunApproved}: true, {RunCreated, RunRejected}: true,
		{RunApproved, RunSent}: true, {RunApproved, RunRejected}: true,
		{RunSent, RunAccepted}: true, {RunSent, RunRejected}: true,
		{RunSent, RunCancelRequested}: true, {RunSent, RunTimedOut}: true, {RunSent, RunLost}: true,
		{RunAccepted, RunRunning}: true, {RunAccepted, RunSucceeded}: true, {RunAccepted, RunFailed}: true,
		{RunAccepted, RunCancelRequested}: true, {RunAccepted, RunTimedOut}: true, {RunAccepted, RunLost}: true,
		{RunRunning, RunSucceeded}: true, {RunRunning, RunFailed}: true,
		{RunRunning, RunCancelRequested}: true, {RunRunning, RunTimedOut}: true, {RunRunning, RunLost}: true,
		{RunCancelRequested, RunSucceeded}: true, {RunCancelRequested, RunFailed}: true,
		{RunCancelRequested, RunRejected}: true, {RunCancelRequested, RunCancelled}: true,
		{RunCancelRequested, RunTimedOut}: true, {RunCancelRequested, RunLost}: true,
	}
	for _, from := range statuses {
		for _, to := range statuses {
			if got := CanTransitionRun(from, to); got != want[[2]RunStatus{from, to}] {
				t.Errorf("CanTransitionRun(%s, %s) = %v", from, to, got)
			}
		}
	}
	terminals := []RunStatus{RunSucceeded, RunFailed, RunRejected, RunCancelled, RunTimedOut, RunLost}
	for _, terminal := range terminals {
		if !IsTerminalRunStatus(terminal) {
			t.Errorf("%s should be terminal", terminal)
		}
		for _, next := range terminals {
			if CanTransitionRun(terminal, next) {
				t.Errorf("terminal transition %s -> %s must be illegal", terminal, next)
			}
		}
	}
}

func TestMessageValidatorsRejectUnknownCodes(t *testing.T) {
	if err := ValidateRPCRejected(RPCRejected{RunID: "run-1", Code: "other"}); err == nil {
		t.Fatal("unknown rejection code must be rejected")
	}
	if err := ValidateRPCCancelResult(RPCCancelResult{RunID: "run-1", Status: "other"}); err == nil {
		t.Fatal("unknown cancel status must be rejected")
	}
}

func TestValidateRPCProgress(t *testing.T) {
	valid := RPCProgress{RunID: "run-1", Seq: 1, Kind: ProgressStdout, Data: "你好"}
	if err := ValidateRPCProgress(valid); err != nil {
		t.Fatalf("valid progress rejected: %v", err)
	}
	invalid := []RPCProgress{
		{Seq: 1, Kind: ProgressStdout, Data: "x"},
		{RunID: "run-1", Kind: ProgressStdout, Data: "x"},
		{RunID: "run-1", Seq: 1, Kind: "status", Data: "x"},
		{RunID: "run-1", Seq: 1, Kind: ProgressStdout},
		{RunID: "run-1", Seq: 1, Kind: ProgressStdout, Data: string([]byte{0xff})},
		{RunID: "run-1", Seq: 1, Kind: ProgressStdout, Data: strings.Repeat("x", MaxRPCProgressChunkBytes+1)},
	}
	for _, msg := range invalid {
		if err := ValidateRPCProgress(msg); err == nil {
			t.Fatalf("invalid progress accepted: %+v", msg)
		}
	}
}

func TestValidateRPCRejectsMalformedPayload(t *testing.T) {
	now := time.Now()
	if err := ValidateRPC(RPC{Tool: "read_file", Args: map[string]any{}, DeadlineAt: now.Add(time.Minute).UnixMilli()}, now); err == nil {
		t.Fatal("missing run_id must be rejected")
	}
	if err := ValidateRPC(RPC{RunID: "run-1", Tool: "read_file", Args: map[string]any{}, DeadlineAt: now.Add(-time.Second).UnixMilli()}, now); err == nil {
		t.Fatal("expired deadline must be rejected")
	}
}

func TestSessionStampAndAccept(t *testing.T) {
	client, err := NewSession("hand-1", "mind", "session-1")
	if err != nil {
		t.Fatalf("client NewSession: %v", err)
	}
	server, err := NewSession("mind", "hand-1", "session-1")
	if err != nil {
		t.Fatalf("server NewSession: %v", err)
	}

	first, err := client.Stamp(Envelope{Type: TypePing})
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	if first.MsgID == "" {
		t.Fatal("Stamp should generate msg_id")
	}
	if first.From != "hand-1" || first.To != "mind" || first.SessionID != "session-1" || first.Seq != 1 {
		t.Fatalf("bad stamped envelope: %+v", first)
	}
	if err := server.Accept(first); err != nil {
		t.Fatalf("server Accept: %v", err)
	}
	if err := server.Accept(first); err == nil {
		t.Fatal("server should reject replayed seq")
	}

	second, err := client.Stamp(Envelope{Type: TypePing})
	if err != nil {
		t.Fatalf("second Stamp: %v", err)
	}
	if second.Seq != 2 {
		t.Fatalf("second seq = %d, want 2", second.Seq)
	}
	if err := server.Accept(second); err != nil {
		t.Fatalf("server Accept second: %v", err)
	}
}

func TestEncryptedPayloadEncoding(t *testing.T) {
	raw := []byte{0, 1, 2, 3, 255}
	payload, err := NewEncryptedPayload("test-alg", raw)
	if err != nil {
		t.Fatalf("NewEncryptedPayload: %v", err)
	}
	env := Envelope{Payload: payload}
	decoded, data, err := DecodeEncryptedPayload(&env)
	if err != nil {
		t.Fatalf("DecodeEncryptedPayload: %v", err)
	}
	if decoded.Alg != "test-alg" {
		t.Fatalf("Alg = %q, want test-alg", decoded.Alg)
	}
	if !bytes.Equal(data, raw) {
		t.Fatalf("decoded data = %v, want %v", data, raw)
	}
}
