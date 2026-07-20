package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

var validFaceScopes = map[protocol.FaceScope]struct{}{
	protocol.FaceScopeChat:          {},
	protocol.FaceScopeSessionsRead:  {},
	protocol.FaceScopeSessionsWrite: {},
	protocol.FaceScopeRunsRead:      {},
	protocol.FaceScopeRunsCancel:    {},
	protocol.FaceScopeRunsOutput:    {},
	protocol.FaceScopeApprove:       {},
	protocol.FaceScopeHandsRead:     {},
	protocol.FaceScopeTasksRead:     {},
	protocol.FaceScopeTasksCancel:   {},
}

// FaceCredential 是带有规范权限集合的 Face 凭据。
type FaceCredential struct {
	Credential
	Scopes []protocol.FaceScope
}

// FaceIdentityByLabel 返回不含凭据秘密的 Face 身份；不存在时返回 nil。
func (s *Store) FaceIdentityByLabel(label string) (*protocol.FaceIdentity, error) {
	if err := validateCredentialLabel(label); err != nil {
		return nil, nil
	}
	var id int64
	var storedLabel, scopesJSON string
	err := s.db.QueryRow(`SELECT id, label, scopes FROM face_tokens WHERE label = ?`, label).Scan(&id, &storedLabel, &scopesJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get Face identity: %w", err)
	}
	var scopes []protocol.FaceScope
	if err := json.Unmarshal([]byte(scopesJSON), &scopes); err != nil {
		return nil, fmt.Errorf("decode Face identity scopes: %w", err)
	}
	canonical, encoded, err := encodeFaceScopes(scopes)
	if err != nil {
		return nil, fmt.Errorf("validate Face identity scopes: %w", err)
	}
	if encoded != scopesJSON {
		return nil, fmt.Errorf("Face identity scopes are not canonical")
	}
	return &protocol.FaceIdentity{ID: strconv.FormatInt(id, 10), Label: storedLabel, Scopes: canonical}, nil
}

// CanonicalFaceScopes 校验、去重并按字典序排列 Face scopes。
func CanonicalFaceScopes(scopes []protocol.FaceScope) ([]protocol.FaceScope, error) {
	if len(scopes) == 0 {
		return nil, fmt.Errorf("face scopes must not be empty")
	}
	seen := make(map[protocol.FaceScope]struct{}, len(scopes))
	canonical := make([]protocol.FaceScope, 0, len(scopes))
	for _, scope := range scopes {
		if _, ok := validFaceScopes[scope]; !ok {
			return nil, fmt.Errorf("unknown face scope %q", scope)
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		canonical = append(canonical, scope)
	}
	slices.Sort(canonical)
	return canonical, nil
}

// AddFaceToken 创建绑定到 label 和 scopes 的 Face 凭据。
func (s *Store) AddFaceToken(label string, scopes []protocol.FaceScope) (*FaceCredential, error) {
	credential, err := GenerateCredential(label)
	if err != nil {
		return nil, err
	}
	canonical, encoded, err := encodeFaceScopes(scopes)
	if err != nil {
		return nil, err
	}
	result, err := s.db.Exec(
		`INSERT INTO face_tokens (label, token, application_key, scopes) VALUES (?, ?, ?, ?)`,
		credential.Label, credential.Token, credential.ApplicationKey, encoded,
	)
	if err != nil {
		return nil, fmt.Errorf("add face token: %w", err)
	}
	credential.ID, err = result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("read face token ID: %w", err)
	}
	face, err := s.faceTokenByLabel(label)
	if err != nil {
		return nil, fmt.Errorf("read added face token: %w", err)
	}
	face.Scopes = canonical
	return face, nil
}

// ListFaceTokens 返回全部 Face 凭据及其规范权限。
func (s *Store) ListFaceTokens() ([]FaceCredential, error) {
	rows, err := s.db.Query(
		`SELECT id, label, token, application_key, scopes, created_at FROM face_tokens ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list face tokens: %w", err)
	}
	defer rows.Close()

	var tokens []FaceCredential
	for rows.Next() {
		token, err := scanFaceCredential(rows)
		if err != nil {
			return nil, fmt.Errorf("scan face token: %w", err)
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list face tokens: %w", err)
	}
	return tokens, nil
}

// RemoveFaceToken 按 ID 删除 Face 凭据。
func (s *Store) RemoveFaceToken(id int64) error {
	return removeCredential(s.db, "face_tokens", "id", id, "face token")
}

// RemoveFaceTokenByLabel 按 label 删除 Face 凭据。
func (s *Store) RemoveFaceTokenByLabel(label string) error {
	if err := validateCredentialLabel(label); err != nil {
		return err
	}
	return removeCredential(s.db, "face_tokens", "label", label, "face token")
}

// AuthenticateFaceToken 验证 label 和 token，并返回绑定的 Face 凭据及 scopes。
func (s *Store) AuthenticateFaceToken(label, token string) (*FaceCredential, error) {
	if err := validateCredentialLabel(label); err != nil {
		return nil, fmt.Errorf("authentication failed")
	}
	if err := validateCredentialSecret("token", token); err != nil {
		return nil, fmt.Errorf("authentication failed")
	}
	credential, err := s.faceTokenByLabel(label)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("authentication failed")
		}
		return nil, fmt.Errorf("authenticate face token: %w", err)
	}
	if !credentialMatches(credential.Label, credential.Token, label, token) {
		return nil, fmt.Errorf("authentication failed")
	}
	return credential, nil
}

func (s *Store) faceTokenByLabel(label string) (*FaceCredential, error) {
	row := s.db.QueryRow(
		`SELECT id, label, token, application_key, scopes, created_at FROM face_tokens WHERE label = ?`, label,
	)
	credential, err := scanFaceCredential(row)
	if err != nil {
		return nil, err
	}
	return &credential, nil
}

func scanFaceCredential(scanner credentialScanner) (FaceCredential, error) {
	var credential FaceCredential
	var scopesJSON, createdAt string
	if err := scanner.Scan(
		&credential.ID,
		&credential.Label,
		&credential.Token,
		&credential.ApplicationKey,
		&scopesJSON,
		&createdAt,
	); err != nil {
		return FaceCredential{}, err
	}
	parsed, err := validateCredentialRecord(credential.Credential, createdAt)
	if err != nil {
		return FaceCredential{}, err
	}
	credential.CreatedAt = parsed

	var scopes []protocol.FaceScope
	if err := json.Unmarshal([]byte(scopesJSON), &scopes); err != nil {
		return FaceCredential{}, fmt.Errorf("invalid face scopes JSON: %w", err)
	}
	canonical, encoded, err := encodeFaceScopes(scopes)
	if err != nil {
		return FaceCredential{}, err
	}
	if encoded != scopesJSON {
		return FaceCredential{}, fmt.Errorf("face scopes are not canonical")
	}
	credential.Scopes = canonical
	return credential, nil
}

func encodeFaceScopes(scopes []protocol.FaceScope) ([]protocol.FaceScope, string, error) {
	canonical, err := CanonicalFaceScopes(scopes)
	if err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", fmt.Errorf("encode face scopes: %w", err)
	}
	return canonical, string(encoded), nil
}
