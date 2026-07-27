package wss

import (
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

const (
	proofInfo = "half-pi/v3/register-proof/"
	c2sInfo   = "half-pi/v3/client-to-server/"
	s2cInfo   = "half-pi/v3/server-to-client/"

	// challengeSize 是服务端挑战的固定字节数。
	challengeSize = 32
	// ShareSize 是 X25519 公钥和共享秘密的固定字节数。
	ShareSize = 32
)

// NewEphemeralShare 生成一次性 X25519 密钥对，返回私钥及其标准 base64 公钥。
// 私钥用后即弃，使长期秘密事后泄露也无法重算历史会话密钥。
func NewEphemeralShare() (*ecdh.PrivateKey, string, error) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("generate ephemeral share: %w", err)
	}
	return private, base64.StdEncoding.EncodeToString(private.PublicKey().Bytes()), nil
}

// ComputeSharedSecret 校验对端公钥编码并计算 X25519 共享秘密。
// crypto/ecdh 对低阶点在 ECDH 阶段返回错误，因此无需额外的小子群检查。
func ComputeSharedSecret(private *ecdh.PrivateKey, peerShare string) ([]byte, error) {
	if private == nil {
		return nil, fmt.Errorf("ephemeral private key is required")
	}
	raw, err := decodeCanonicalBase64(peerShare, ShareSize)
	if err != nil {
		return nil, fmt.Errorf("peer share must be canonical base64 of %d bytes", ShareSize)
	}
	public, err := ecdh.X25519().NewPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid peer share: %w", err)
	}
	shared, err := private.ECDH(public)
	if err != nil {
		return nil, fmt.Errorf("compute shared secret: %w", err)
	}
	return shared, nil
}

// decodeCanonicalBase64 解码标准 base64 并要求编码规范且长度精确匹配。
func decodeCanonicalBase64(value string, size int) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != size || base64.StdEncoding.EncodeToString(raw) != value {
		return nil, fmt.Errorf("value must be canonical base64 of %d bytes", size)
	}
	return raw, nil
}

// SessionKeys 是注册 transcript 派生的三把用途隔离密钥。
type SessionKeys struct {
	Proof          [KeySize]byte
	ClientToServer [KeySize]byte
	ServerToClient [KeySize]byte
}

// DeriveSessionKeys 按 v3 transcript、ECDH 共享秘密和两项长期秘密
// 派生 proof、C→S 和 S→C 密钥。
//
// shared 必须是 32 字节 X25519 输出。它使会话密钥无法仅由长期秘密重算，
// 从而同时提供前向保密和握手新鲜性。
func DeriveSessionKeys(token, applicationKey string, shared []byte, transcript protocol.HandshakeTranscript) (SessionKeys, error) {
	var keys SessionKeys
	if len(shared) != ShareSize {
		return keys, fmt.Errorf("shared secret must be %d bytes", ShareSize)
	}
	tokenBytes, err := decodeHandshakeSecret("token", token)
	if err != nil {
		return keys, err
	}
	applicationKeyBytes, err := decodeHandshakeSecret("application key", applicationKey)
	if err != nil {
		return keys, err
	}
	// 三段均为定长（16 + 16 + 32），拼接无歧义。
	root := make([]byte, 0, len(tokenBytes)+len(applicationKeyBytes)+len(shared))
	root = append(root, tokenBytes...)
	root = append(root, applicationKeyBytes...)
	root = append(root, shared...)
	defer clear(root)
	challenge, err := decodeCanonicalBase64(transcript.Challenge, challengeSize)
	if err != nil {
		return keys, fmt.Errorf("challenge must be canonical base64 of %d bytes", challengeSize)
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

// NewRegisterProof 创建绑定 transcript 且加密身份声明的 v2 GCM proof。
func NewRegisterProof(keys SessionKeys, transcript protocol.HandshakeTranscript, handInfo *protocol.HandInfo) (protocol.RegisterProof, error) {
	claims := protocol.RegisterProofClaims{Challenge: transcript.Challenge, HandInfo: handInfo}
	if err := validateProofClaims(transcript.PeerType, claims, handInfo != nil); err != nil {
		return protocol.RegisterProof{}, err
	}
	aad, err := proofAAD(transcript)
	if err != nil {
		return protocol.RegisterProof{}, err
	}
	cipher, err := NewAES128GCM(keys.Proof[:])
	if err != nil {
		return protocol.RegisterProof{}, err
	}
	plain, err := json.Marshal(claims)
	if err != nil {
		return protocol.RegisterProof{}, fmt.Errorf("marshal register proof claims: %w", err)
	}
	proof, err := cipher.Encrypt(plain, aad)
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

// VerifyRegisterProof 验证并解密 proof，返回通过角色约束检查的身份声明。
func VerifyRegisterProof(keys SessionKeys, transcript protocol.HandshakeTranscript, proof protocol.RegisterProof) (protocol.RegisterProofClaims, error) {
	var zero protocol.RegisterProofClaims
	if proof.ProtocolVersion != protocol.ProtocolVersion || proof.HandshakeID != transcript.HandshakeID || proof.Algorithm != protocol.HandshakeAlgorithm {
		return zero, fmt.Errorf("proof metadata mismatch")
	}
	data, err := base64.StdEncoding.DecodeString(proof.Proof)
	if err != nil || base64.StdEncoding.EncodeToString(data) != proof.Proof {
		return zero, fmt.Errorf("invalid proof encoding")
	}
	aad, err := proofAAD(transcript)
	if err != nil {
		return zero, err
	}
	cipher, err := NewAES128GCM(keys.Proof[:])
	if err != nil {
		return zero, err
	}
	plain, err := cipher.Decrypt(data, aad)
	if err != nil {
		return zero, err
	}
	claims, err := protocol.StrictDecode[protocol.RegisterProofClaims](plain)
	if err != nil {
		return zero, fmt.Errorf("decode register proof claims: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(plain, &fields); err != nil {
		return zero, fmt.Errorf("decode register proof fields: %w", err)
	}
	_, handInfoPresent := fields["hand_info"]
	if err := validateProofClaims(transcript.PeerType, claims, handInfoPresent); err != nil {
		return zero, err
	}
	if claims.Challenge != transcript.Challenge {
		return zero, fmt.Errorf("proof challenge mismatch")
	}
	return claims, nil
}

func decodeHandshakeSecret(name, value string) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != KeySize || hex.EncodeToString(decoded) != value {
		return nil, fmt.Errorf("%s must be 32 lowercase hex characters", name)
	}
	return decoded, nil
}

func validateProofClaims(peerType protocol.PeerType, claims protocol.RegisterProofClaims, handInfoPresent bool) error {
	if _, err := decodeCanonicalBase64(claims.Challenge, challengeSize); err != nil {
		return fmt.Errorf("invalid proof challenge")
	}
	switch peerType {
	case protocol.PeerFace:
		if handInfoPresent {
			return fmt.Errorf("face proof must omit hand info")
		}
	case protocol.PeerHand:
		if !handInfoPresent || claims.HandInfo == nil || claims.HandInfo.OS == "" || claims.HandInfo.Arch == "" || claims.HandInfo.Hostname == "" {
			return fmt.Errorf("hand proof requires hand info")
		}
	default:
		return fmt.Errorf("invalid peer type")
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
