package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// HandToken Hand 认证令牌。
type HandToken struct {
	ID        int64
	Label     string
	Token     string
	CreatedAt time.Time
}

// GenerateToken 生成 16 字节 hex 令牌。
func GenerateToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("generate token: %v", err))
	}
	return hex.EncodeToString(b)
}

// AddHandToken 添加一个 Hand 令牌并返回记录。
func (s *Store) AddHandToken(label string) (*HandToken, error) {
	token := GenerateToken()
	_, err := s.db.Exec(
		`INSERT INTO hand_tokens (label, token) VALUES (?, ?)`,
		label, token,
	)
	if err != nil {
		return nil, fmt.Errorf("add hand token: %w", err)
	}
	return s.FindHandTokenByToken(token)
}

// ListHandTokens 返回所有 Hand 令牌。
func (s *Store) ListHandTokens() ([]HandToken, error) {
	rows, err := s.db.Query(
		`SELECT id, label, token, created_at FROM hand_tokens ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list hand tokens: %w", err)
	}
	defer rows.Close()

	var tokens []HandToken
	for rows.Next() {
		var ht HandToken
		var ca string
		if err := rows.Scan(&ht.ID, &ht.Label, &ht.Token, &ca); err != nil {
			return nil, fmt.Errorf("scan hand token: %w", err)
		}
		ht.CreatedAt = parseTime(ca)
		tokens = append(tokens, ht)
	}
	return tokens, rows.Err()
}

// RemoveHandToken 按 id 删除 Hand 令牌。
func (s *Store) RemoveHandToken(id int64) error {
	result, err := s.db.Exec(`DELETE FROM hand_tokens WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("remove hand token: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("hand token %d not found", id)
	}
	return nil
}

// ValidateHandToken 验证令牌是否有效，返回匹配的 HandToken 或错误。
func (s *Store) ValidateHandToken(token string) (*HandToken, error) {
	if token == "" {
		return nil, fmt.Errorf("empty token")
	}
	row := s.db.QueryRow(
		`SELECT id, label, token, created_at FROM hand_tokens WHERE token = ?`, token,
	)
	var ht HandToken
	var ca string
	err := row.Scan(&ht.ID, &ht.Label, &ht.Token, &ca)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("invalid token")
	}
	if err != nil {
		return nil, fmt.Errorf("validate token: %w", err)
	}
	ht.CreatedAt = parseTime(ca)
	return &ht, nil
}

// FindHandTokenByToken 按令牌值查找记录（用于注册返回）。
func (s *Store) FindHandTokenByToken(token string) (*HandToken, error) {
	return s.ValidateHandToken(token)
}
