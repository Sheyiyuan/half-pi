package wss

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestGenerateKey(t *testing.T) {
	k1, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if len(k1) != KeySize {
		t.Errorf("key length = %d, want %d", len(k1), KeySize)
	}
	k2, _ := GenerateKey()
	if bytes.Equal(k1, k2) {
		t.Error("two generated keys should not be equal")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, _ := GenerateKey()
	c, err := NewAES128GCM(key)
	if err != nil {
		t.Fatalf("NewAES128GCM: %v", err)
	}

	plain := []byte("hello encrypted world!")
	aad := []byte("msg-1|ping|face1|mind")

	enc, err := c.Encrypt(plain, aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(enc, plain) {
		t.Error("encrypted data should differ from plaintext")
	}

	dec, err := c.Decrypt(enc, aad)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(dec, plain) {
		t.Errorf("decrypted = %q, want %q", dec, plain)
	}
}

func TestDecryptTamperedAAD(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := NewAES128GCM(key)

	enc, _ := c.Encrypt([]byte("secret"), []byte("legit-aad"))

	// Wrong AAD should fail
	_, err := c.Decrypt(enc, []byte("wrong-aad"))
	if err == nil {
		t.Error("wrong AAD should fail decryption")
	}
}

func TestDecryptTampered(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := NewAES128GCM(key)

	aad := []byte("ctx")
	enc, _ := c.Encrypt([]byte("secret"), aad)
	// Flip a byte of ciphertext
	enc[len(enc)-1] ^= 0x01

	_, err := c.Decrypt(enc, aad)
	if err == nil {
		t.Error("tampered ciphertext should fail decryption")
	}
}

func TestDecryptTooShort(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := NewAES128GCM(key)

	_, err := c.Decrypt([]byte("x"), nil)
	if err == nil {
		t.Error("short ciphertext should fail decryption")
	}
}

func TestNewAES128GCMBadKey(t *testing.T) {
	_, err := NewAES128GCM([]byte("short"))
	if err == nil {
		t.Error("short key should fail")
	}
}

func TestEncryptDecryptEmptyAAD(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := NewAES128GCM(key)

	enc, _ := c.Encrypt([]byte("data"), nil)
	dec, err := c.Decrypt(enc, nil)
	if err != nil {
		t.Fatalf("decrypt with nil AAD: %v", err)
	}
	if !bytes.Equal(dec, []byte("data")) {
		t.Error("empty AAD round-trip failed")
	}
}

func TestEncryptDecryptEnvelopePayload(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := NewAES128GCM(key)

	env := protocol.Envelope{
		MsgID:     "msg-1",
		Type:      protocol.TypeRPC,
		SessionID: "session-1",
		From:      "mind",
		To:        "hand-1",
		Seq:       1,
	}
	payload := protocol.RPC{
		RunID:      "rpc-1",
		Tool:       "read_file",
		Args:       map[string]any{"path": "README.md"},
		DeadlineAt: time.Now().Add(time.Minute).UnixMilli(),
	}

	if err := c.EncryptEnvelopePayload(&env, payload); err != nil {
		t.Fatalf("EncryptEnvelopePayload: %v", err)
	}
	if bytes.Contains(env.Payload, []byte("README.md")) {
		t.Fatal("encrypted payload should not contain plaintext")
	}

	got, err := DecryptEnvelopePayload[protocol.RPC](c, &env)
	if err != nil {
		t.Fatalf("DecryptEnvelopePayload: %v", err)
	}
	if got.RunID != payload.RunID || got.Tool != payload.Tool {
		t.Fatalf("payload mismatch: %+v", got)
	}

	tampered := env
	tampered.Seq = 2
	if _, err := DecryptEnvelopePayload[protocol.RPC](c, &tampered); err == nil {
		t.Fatal("tampered header should fail AAD authentication")
	}
}

func TestDecryptEnvelopePayloadStrictAndBounded(t *testing.T) {
	key, _ := GenerateKey()
	cipher, _ := NewAES128GCM(key)
	base := protocol.Envelope{MsgID: "strict", Type: protocol.TypePing, SessionID: "session", From: "face", To: "mind", Seq: 1}

	unknown := base
	if err := cipher.EncryptEnvelopePayload(&unknown, json.RawMessage(`{"ts":1,"unknown":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptEnvelopePayload[protocol.Ping](cipher, &unknown); err == nil {
		t.Fatal("unknown plaintext field was accepted")
	}

	trailing := base
	ciphertext, err := cipher.Encrypt([]byte(`{"ts":1} {}`), trailing.AAD())
	if err != nil {
		t.Fatal(err)
	}
	trailing.Payload, err = protocol.NewEncryptedPayload(AlgAES128GCM, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptEnvelopePayload[protocol.Ping](cipher, &trailing); err == nil {
		t.Fatal("trailing plaintext JSON was accepted")
	}

	tooLarge := strings.Repeat("x", MaxPlaintextPayload+1)
	if err := cipher.EncryptEnvelopePayload(&base, tooLarge); err == nil {
		t.Fatal("oversized plaintext was encrypted")
	}
}

func TestWorstCaseOneMiBRPCResultFitsWireLimits(t *testing.T) {
	key, _ := GenerateKey()
	cipher, _ := NewAES128GCM(key)
	env := protocol.Envelope{MsgID: "worst-case", Type: protocol.TypeRPCResult, SessionID: "session", From: "hand", To: "mind", Seq: 1}
	result := protocol.RPCResult{RunID: "run-1", Success: true, Output: strings.Repeat("\x00", 1<<20)}
	if err := cipher.EncryptEnvelopePayload(&env, result); err != nil {
		t.Fatalf("encrypt worst-case result: %v", err)
	}
	wire, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) > MaxFrameSize {
		t.Fatalf("worst-case frame = %d bytes, limit %d", len(wire), MaxFrameSize)
	}
	got, err := DecryptEnvelopePayload[protocol.RPCResult](cipher, &env)
	if err != nil || got.Output != result.Output {
		t.Fatalf("worst-case result round trip failed: %v", err)
	}
}

func TestDecryptEnvelopePayloadRejectsMalformedEncryption(t *testing.T) {
	key, _ := GenerateKey()
	cipher, _ := NewAES128GCM(key)
	base := protocol.Envelope{MsgID: "bad", Type: protocol.TypePing, SessionID: "session", From: "face", To: "mind", Seq: 1}
	for name, payload := range map[string]string{
		"plaintext":      `{"ts":1}`,
		"unknown alg":    `{"alg":"other","data":"AAAA"}`,
		"invalid base64": `{"alg":"AES-128-GCM","data":"***"}`,
		"short cipher":   `{"alg":"AES-128-GCM","data":"AA=="}`,
	} {
		t.Run(name, func(t *testing.T) {
			env := base
			env.Payload = []byte(payload)
			if _, err := DecryptEnvelopePayload[protocol.Ping](cipher, &env); err == nil {
				t.Fatal("malformed encrypted payload was accepted")
			}
		})
	}
}

func TestEnvelopeEncryptionBindsEveryHeader(t *testing.T) {
	key, _ := GenerateKey()
	cipher, _ := NewAES128GCM(key)
	base := protocol.Envelope{MsgID: "msg", Type: protocol.TypePing, SessionID: "session", From: "face", To: "mind", Seq: 1}
	if err := cipher.EncryptEnvelopePayload(&base, protocol.Ping{Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	mutations := []protocol.Envelope{base, base, base, base, base, base}
	mutations[0].MsgID = "other"
	mutations[1].Type = protocol.TypePong
	mutations[2].SessionID = "other"
	mutations[3].From = "other"
	mutations[4].To = "other"
	mutations[5].Seq = 2
	for _, mutation := range mutations {
		if _, err := DecryptEnvelopePayload[protocol.Ping](cipher, &mutation); err == nil {
			t.Fatalf("tampered header decrypted: %+v", mutation)
		}
	}
}
