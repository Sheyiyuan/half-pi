// Package wss 提供应用层加密 WebSocket 通信，用于 Face-Mind-Hand 消息交换。
//
// 加密在应用层完成（AES-128-GCM），不依赖 TLS 证书。
// ⚠️ 实验性：当前仅提供 Encrypt/Decrypt 原语，缺少 key exchange、key ID。
// 重放防护由 protocol.Envelope 的 session/seq 和 hub.Accept 提供。
// 生产环境请配合 TLS 使用。
package wss

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

const (
	KeySize      = 16
	AlgAES128GCM = "AES-128-GCM"
)

// GenerateKey 生成 16 字节 AES-128 随机密钥。
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return key, nil
}

// AES128GCM 封装 AES-128-GCM 加解密。
type AES128GCM struct {
	aead cipher.AEAD
}

// NewAES128GCM 用 16 字节密钥创建加密器。
func NewAES128GCM(key []byte) (*AES128GCM, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &AES128GCM{aead: aead}, nil
}

// Encrypt 加密明文，aad 会参与认证但不放入密文。
// 返回 nonce || ciphertext。
func (c *AES128GCM) Encrypt(plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("encrypt nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plain, aad), nil
}

// Decrypt 解密密文，aad 必须与加密时一致，否则因 GCM tag 验证失败返回错误。
// 密文必须至少 NonceSize() 字节。
func (c *AES128GCM) Decrypt(data, aad []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(data) < ns {
		return nil, fmt.Errorf("ciphertext too short (%d bytes)", len(data))
	}
	nonce, ciphertext := data[:ns], data[ns:]
	plain, err := c.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plain, nil
}

// EncryptEnvelopePayload 将 payload 序列化后加密写入 env.Payload。
// 调用前 env 必须已经带有最终的 msg_id/type/session_id/from/to/seq。
func (c *AES128GCM) EncryptEnvelopePayload(env *protocol.Envelope, payload any) error {
	plain, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	ciphertext, err := c.Encrypt(plain, env.AAD())
	if err != nil {
		return err
	}
	encoded, err := protocol.NewEncryptedPayload(AlgAES128GCM, ciphertext)
	if err != nil {
		return err
	}
	env.Payload = encoded
	return nil
}

// DecryptEnvelopePayload 解密 env.Payload 并反序列化为目标类型。
func DecryptEnvelopePayload[T any](c *AES128GCM, env *protocol.Envelope) (T, error) {
	var zero T
	payload, ciphertext, err := protocol.DecodeEncryptedPayload(env)
	if err != nil {
		return zero, err
	}
	if payload.Alg != AlgAES128GCM {
		return zero, fmt.Errorf("unsupported payload alg: %s", payload.Alg)
	}
	plain, err := c.Decrypt(ciphertext, env.AAD())
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(plain, &out); err != nil {
		return zero, fmt.Errorf("unmarshal payload: %w", err)
	}
	return out, nil
}
