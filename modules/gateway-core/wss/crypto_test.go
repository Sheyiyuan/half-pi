package wss

import (
	"bytes"
	"testing"

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
	payload := protocol.RPC{ID: "rpc-1", Tool: "read_file", Args: map[string]any{"path": "README.md"}}

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
	if got.ID != payload.ID || got.Tool != payload.Tool {
		t.Fatalf("payload mismatch: %+v", got)
	}

	tampered := env
	tampered.Seq = 2
	if _, err := DecryptEnvelopePayload[protocol.RPC](c, &tampered); err == nil {
		t.Fatal("tampered header should fail AAD authentication")
	}
}
