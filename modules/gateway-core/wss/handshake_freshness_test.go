package wss_test

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
)

// deriveOnce 用固定的长期秘密和固定 transcript 骨架完成一次 ECDH 派生，
// 只有每次新生成的 ephemeral share 不同。
func deriveOnce(t *testing.T) wss.SessionKeys {
	t.Helper()
	_, clientShare, err := wss.NewEphemeralShare()
	if err != nil {
		t.Fatal(err)
	}
	serverPriv, serverShare, err := wss.NewEphemeralShare()
	if err != nil {
		t.Fatal(err)
	}
	transcript := protocol.HandshakeTranscript{
		ProtocolVersion: protocol.ProtocolVersion,
		PeerType:        protocol.PeerHand,
		Label:           "fresh-hand",
		HandshakeID:     "00112233445566778899aabbccddeeff",
		ServerID:        hub.DefaultHubID,
		SessionID:       "ffeeddccbbaa99887766554433221100",
		Challenge:       "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		ExpiresAt:       1700000000000,
		Algorithm:       protocol.HandshakeAlgorithm,
		ClientShare:     clientShare,
		ServerShare:     serverShare,
	}
	shared, err := wss.ComputeSharedSecret(serverPriv, clientShare)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := wss.DeriveSessionKeys(testToken, testKey, shared, transcript)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// TestSameCredentialsProduceFreshSessionKeys 断言同一对长期秘密的两次握手派生出
// 完全不同的会话密钥。
func TestSameCredentialsProduceFreshSessionKeys(t *testing.T) {
	first, second := deriveOnce(t), deriveOnce(t)
	for name, pair := range map[string][2][]byte{
		"proof":            {first.Proof[:], second.Proof[:]},
		"client_to_server": {first.ClientToServer[:], second.ClientToServer[:]},
		"server_to_client": {first.ServerToClient[:], second.ServerToClient[:]},
	} {
		if bytes.Equal(pair[0], pair[1]) {
			t.Fatalf("同一凭据的两次握手派生出相同的 %s 密钥", name)
		}
	}
}

// TestSharedSecretIsRequiredForDerivation 断言 ECDH 共享秘密确实参与派生。
// 前向保密的充分条件是"密钥不可由 PSK + 公开 transcript 重算"：固定 transcript
// 而只改变 shared 必须改变全部三把密钥。否则掌握 PSK 者可从录制流量重算密钥。
func TestSharedSecretIsRequiredForDerivation(t *testing.T) {
	transcript := protocol.HandshakeTranscript{
		ProtocolVersion: protocol.ProtocolVersion,
		PeerType:        protocol.PeerHand,
		Label:           "psk-hand",
		HandshakeID:     "00112233445566778899aabbccddeeff",
		ServerID:        hub.DefaultHubID,
		SessionID:       "ffeeddccbbaa99887766554433221100",
		Challenge:       "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		ExpiresAt:       1700000000000,
		Algorithm:       protocol.HandshakeAlgorithm,
		ClientShare:     "ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8=",
		ServerShare:     "QEFCQ0RFRkdISUpLTE1OT1BRUlNUVVZXWFlaW1xdXl8=",
	}
	sharedA := bytes.Repeat([]byte{0xAA}, wss.ShareSize)
	sharedB := bytes.Repeat([]byte{0xBB}, wss.ShareSize)
	first, err := wss.DeriveSessionKeys(testToken, testKey, sharedA, transcript)
	if err != nil {
		t.Fatal(err)
	}
	second, err := wss.DeriveSessionKeys(testToken, testKey, sharedB, transcript)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Proof[:], second.Proof[:]) ||
		bytes.Equal(first.ClientToServer[:], second.ClientToServer[:]) ||
		bytes.Equal(first.ServerToClient[:], second.ServerToClient[:]) {
		t.Fatal("ECDH 共享秘密未参与密钥派生：PSK 持有者可从录制流量重算会话密钥")
	}
}

// TestEphemeralShareEmitsPublicKeyNotPrivate 断言 NewEphemeralShare 上线的是公钥而非
// 私钥。client.go 与 hub.go 都只把该返回串放入握手帧，因此只要它等于公钥、不等于私钥
// 种子，就保证 ephemeral 私钥不进入任何明文帧 —— 私钥泄露会让前向保密失效。
func TestEphemeralShareEmitsPublicKeyNotPrivate(t *testing.T) {
	private, share, err := wss.NewEphemeralShare()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(share)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, private.PublicKey().Bytes()) {
		t.Fatal("share 不是 ephemeral 公钥")
	}
	if bytes.Equal(raw, private.Bytes()) {
		t.Fatal("share 暴露了 ephemeral 私钥种子")
	}
	if len(private.Bytes()) != wss.ShareSize {
		t.Fatalf("私钥种子长度 = %d，期望 %d", len(private.Bytes()), wss.ShareSize)
	}
}

// TestHandshakeWirePublishesPublicSharesOnly 录制真实会话，断言公钥 share 按预期出现在
// 明文帧中（公开无害），且两个长期秘密不出现。私钥由客户端内部生成、从不离开进程。
func TestHandshakeWirePublishesPublicSharesOnly(t *testing.T) {
	recorded := recordSession(t, "ephemeral-hand")
	if !bytes.Contains(recorded.challenge, []byte(`"server_share"`)) {
		t.Fatal("challenge 未携带 server_share")
	}
	wire := bytes.Join([][]byte{recorded.challenge, recorded.registered, recorded.business}, nil)
	for name, secret := range map[string]string{"token": testToken, "application key": testKey} {
		if bytes.Contains(wire, []byte(secret)) {
			t.Fatalf("明文帧暴露 %s", name)
		}
	}
	// 明文 challenge 中的 server_share 必须是规范 X25519 公钥。
	var env protocol.Envelope
	if err := json.Unmarshal(recorded.challenge, &env); err != nil {
		t.Fatal(err)
	}
	var challenge protocol.RegisterChallenge
	if err := json.Unmarshal(env.Payload, &challenge); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(challenge.ServerShare)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ecdh.X25519().NewPublicKey(raw); err != nil {
		t.Fatalf("server_share 不是合法 X25519 公钥: %v", err)
	}
}
