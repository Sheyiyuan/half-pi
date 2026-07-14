package protocol

import (
	"bytes"
	"testing"
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
