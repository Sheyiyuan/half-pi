package wss

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

const (
	proofInfo = "half-pi/v1/register-proof/"
	c2sInfo   = "half-pi/v1/client-to-server/"
	s2cInfo   = "half-pi/v1/server-to-client/"
)

// SessionKeys 是注册 transcript 派生的三把用途隔离密钥。
type SessionKeys struct {
	Proof          [KeySize]byte
	ClientToServer [KeySize]byte
	ServerToClient [KeySize]byte
}

// DeriveSessionKeys 按 v1 transcript 规范派生 proof、C→S 和 S→C 密钥。
func DeriveSessionKeys(applicationKey string, transcript protocol.HandshakeTranscript) (SessionKeys, error) {
	var keys SessionKeys
	root, err := hex.DecodeString(applicationKey)
	if err != nil || len(root) != KeySize {
		return keys, fmt.Errorf("application key must be 32 lowercase hex characters")
	}
	if applicationKey != hex.EncodeToString(root) {
		return keys, fmt.Errorf("application key must be lowercase hex")
	}
	challenge, err := base64.StdEncoding.DecodeString(transcript.Challenge)
	if err != nil || len(challenge) != 32 || base64.StdEncoding.EncodeToString(challenge) != transcript.Challenge {
		return keys, fmt.Errorf("challenge must be canonical base64 of 32 bytes")
	}
	transcriptJSON, err := json.Marshal(transcript)
	if err != nil {
		return keys, fmt.Errorf("marshal handshake transcript: %w", err)
	}
	hash := sha256.Sum256(transcriptJSON)
	derive := func(info string) ([]byte, error) {
		return hkdf.Key(sha256.New, root, challenge, info+string(hash[:]), KeySize)
	}
	proof, err := derive(proofInfo)
	if err != nil {
		return keys, fmt.Errorf("derive proof key: %w", err)
	}
	c2s, err := derive(c2sInfo)
	if err != nil {
		return keys, fmt.Errorf("derive client-to-server key: %w", err)
	}
	s2c, err := derive(s2cInfo)
	if err != nil {
		return keys, fmt.Errorf("derive server-to-client key: %w", err)
	}
	copy(keys.Proof[:], proof)
	copy(keys.ClientToServer[:], c2s)
	copy(keys.ServerToClient[:], s2c)
	return keys, nil
}

// NewRegisterProof 创建绑定 transcript 的 v1 GCM proof。
func NewRegisterProof(keys SessionKeys, transcript protocol.HandshakeTranscript) (protocol.RegisterProof, error) {
	challenge, err := base64.StdEncoding.DecodeString(transcript.Challenge)
	if err != nil || len(challenge) != 32 {
		return protocol.RegisterProof{}, fmt.Errorf("decode challenge")
	}
	aad, err := proofAAD(transcript)
	if err != nil {
		return protocol.RegisterProof{}, err
	}
	cipher, err := NewAES128GCM(keys.Proof[:])
	if err != nil {
		return protocol.RegisterProof{}, err
	}
	proof, err := cipher.Encrypt(challenge, aad)
	if err != nil {
		return protocol.RegisterProof{}, err
	}
	return protocol.RegisterProof{
		ProtocolVersion: protocol.ProtocolVersion,
		HandshakeID:     transcript.HandshakeID,
		Algorithm:       protocol.HandshakeAlgorithm,
		Proof:           base64.StdEncoding.EncodeToString(proof),
	}, nil
}

// VerifyRegisterProof 验证 proof 的字段、规范 base64、AAD 和挑战明文。
func VerifyRegisterProof(keys SessionKeys, transcript protocol.HandshakeTranscript, proof protocol.RegisterProof) error {
	if proof.ProtocolVersion != protocol.ProtocolVersion || proof.HandshakeID != transcript.HandshakeID || proof.Algorithm != protocol.HandshakeAlgorithm {
		return fmt.Errorf("proof metadata mismatch")
	}
	data, err := base64.StdEncoding.DecodeString(proof.Proof)
	if err != nil || base64.StdEncoding.EncodeToString(data) != proof.Proof {
		return fmt.Errorf("invalid proof encoding")
	}
	aad, err := proofAAD(transcript)
	if err != nil {
		return err
	}
	cipher, err := NewAES128GCM(keys.Proof[:])
	if err != nil {
		return err
	}
	plain, err := cipher.Decrypt(data, aad)
	if err != nil {
		return err
	}
	want, _ := base64.StdEncoding.DecodeString(transcript.Challenge)
	if len(plain) != len(want) {
		return fmt.Errorf("proof challenge mismatch")
	}
	var different byte
	for i := range plain {
		different |= plain[i] ^ want[i]
	}
	if different != 0 {
		return fmt.Errorf("proof challenge mismatch")
	}
	return nil
}

func proofAAD(transcript protocol.HandshakeTranscript) ([]byte, error) {
	return json.Marshal(protocol.RegisterProofAAD{
		ProtocolVersion: transcript.ProtocolVersion,
		Type:            protocol.TypeRegisterProof,
		PeerType:        transcript.PeerType,
		Label:           transcript.Label,
		HandshakeID:     transcript.HandshakeID,
		ServerID:        transcript.ServerID,
		SessionID:       transcript.SessionID,
	})
}
