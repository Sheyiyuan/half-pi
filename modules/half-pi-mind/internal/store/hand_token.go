package store

import (
	"database/sql"
	"fmt"
	"time"
)

// HandToken Hand 认证令牌。
type HandToken struct {
	ID        int64
	Label     string
	HandID    string
	Token     string
	CreatedAt time.Time
}

// LegacyHandTokenCount 返回尚未迁移且不会参与认证的旧 Hand token 行数。
func (s *Store) LegacyHandTokenCount() (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM hand_tokens`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count legacy Hand tokens: %w", err)
	}
	return count, nil
}

// GenerateToken 生成 16 字节 hex 令牌。
func GenerateToken() string {
	token, err := generateCredentialSecret()
	if err != nil {
		panic(fmt.Sprintf("generate token: %v", err))
	}
	return token
}

// AddHandToken 添加一个 Hand 令牌并返回记录。
// Deprecated: 使用 AddHandCredential。
func (s *Store) AddHandToken(label string) (*HandToken, error) {
	credential, err := s.AddHandCredential(label)
	if err != nil {
		return nil, err
	}
	return legacyHandToken(credential), nil
}

// ListHandTokens 返回所有 Hand 令牌。
// Deprecated: 使用 ListHandCredentials。
func (s *Store) ListHandTokens() ([]HandToken, error) {
	credentials, err := s.ListHandCredentials()
	if err != nil {
		return nil, err
	}
	tokens := make([]HandToken, 0, len(credentials))
	for i := range credentials {
		tokens = append(tokens, *legacyHandToken(&credentials[i]))
	}
	return tokens, nil
}

// RemoveHandToken 按 id 删除 Hand 令牌。
// Deprecated: 使用 RemoveHandCredential。
func (s *Store) RemoveHandToken(id int64) error {
	return s.RemoveHandCredential(id)
}

// ValidateHandToken 验证令牌是否有效，返回匹配的 HandToken 或错误。
// Deprecated: 使用 AuthenticateHandCredential。
func (s *Store) ValidateHandToken(token string) (*HandToken, error) {
	if err := validateCredentialSecret("token", token); err != nil {
		return nil, fmt.Errorf("invalid token")
	}
	row := s.db.QueryRow(
		`SELECT id, label, token, application_key, created_at FROM hand_credentials WHERE token = ?`, token,
	)
	credential, err := scanCredential(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("invalid token")
	}
	if err != nil {
		return nil, fmt.Errorf("validate token: %w", err)
	}
	return legacyHandToken(&credential), nil
}

// AuthenticateHandToken 验证 token 是否绑定到声明的 Hand ID。
// Deprecated: 使用 AuthenticateHandCredential。
func (s *Store) AuthenticateHandToken(token, handID string) (*HandToken, error) {
	credential, err := s.AuthenticateHandCredential(handID, token)
	if err != nil {
		return nil, err
	}
	return legacyHandToken(credential), nil
}

// FindHandTokenByToken 按令牌值查找记录（用于注册返回）。
// Deprecated: 使用 AuthenticateHandCredential。
func (s *Store) FindHandTokenByToken(token string) (*HandToken, error) {
	return s.ValidateHandToken(token)
}

func legacyHandToken(credential *Credential) *HandToken {
	return &HandToken{
		ID:        credential.ID,
		Label:     credential.Label,
		HandID:    credential.Label,
		Token:     credential.Token,
		CreatedAt: credential.CreatedAt,
	}
}
