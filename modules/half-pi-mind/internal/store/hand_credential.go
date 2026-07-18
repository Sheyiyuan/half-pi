package store

import (
	"database/sql"
	"fmt"
)

// AddHandCredential 创建绑定到 label 的 Hand 凭据。
func (s *Store) AddHandCredential(label string) (*Credential, error) {
	credential, err := GenerateCredential(label)
	if err != nil {
		return nil, err
	}
	result, err := s.db.Exec(
		`INSERT INTO hand_credentials (label, token, application_key) VALUES (?, ?, ?)`,
		credential.Label, credential.Token, credential.ApplicationKey,
	)
	if err != nil {
		return nil, fmt.Errorf("add hand credential: %w", err)
	}
	credential.ID, err = result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("read hand credential ID: %w", err)
	}
	return s.handCredentialByLabel(label)
}

// ListHandCredentials 返回全部 Hand 凭据。
func (s *Store) ListHandCredentials() ([]Credential, error) {
	rows, err := s.db.Query(
		`SELECT id, label, token, application_key, created_at FROM hand_credentials ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list hand credentials: %w", err)
	}
	defer rows.Close()

	var credentials []Credential
	for rows.Next() {
		credential, err := scanCredential(rows)
		if err != nil {
			return nil, fmt.Errorf("scan hand credential: %w", err)
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list hand credentials: %w", err)
	}
	return credentials, nil
}

// RemoveHandCredential 按 ID 删除 Hand 凭据。
func (s *Store) RemoveHandCredential(id int64) error {
	return removeCredential(s.db, "hand_credentials", "id", id, "hand credential")
}

// RemoveHandCredentialByLabel 按 label 删除 Hand 凭据。
func (s *Store) RemoveHandCredentialByLabel(label string) error {
	if err := validateCredentialLabel(label); err != nil {
		return err
	}
	return removeCredential(s.db, "hand_credentials", "label", label, "hand credential")
}

// AuthenticateHandCredential 验证 label 和 token，并返回绑定的 Hand 凭据。
func (s *Store) AuthenticateHandCredential(label, token string) (*Credential, error) {
	if err := validateCredentialLabel(label); err != nil {
		return nil, fmt.Errorf("authentication failed")
	}
	if err := validateCredentialSecret("token", token); err != nil {
		return nil, fmt.Errorf("authentication failed")
	}
	credential, err := s.handCredentialByLabel(label)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("authentication failed")
		}
		return nil, fmt.Errorf("authenticate hand credential: %w", err)
	}
	if !credentialMatches(credential.Label, credential.Token, label, token) {
		return nil, fmt.Errorf("authentication failed")
	}
	return credential, nil
}

func (s *Store) handCredentialByLabel(label string) (*Credential, error) {
	row := s.db.QueryRow(
		`SELECT id, label, token, application_key, created_at FROM hand_credentials WHERE label = ?`, label,
	)
	credential, err := scanCredential(row)
	if err != nil {
		return nil, err
	}
	return &credential, nil
}

type credentialScanner interface {
	Scan(dest ...any) error
}

func scanCredential(scanner credentialScanner) (Credential, error) {
	var credential Credential
	var createdAt string
	if err := scanner.Scan(
		&credential.ID,
		&credential.Label,
		&credential.Token,
		&credential.ApplicationKey,
		&createdAt,
	); err != nil {
		return Credential{}, err
	}
	parsed, err := validateCredentialRecord(credential, createdAt)
	if err != nil {
		return Credential{}, err
	}
	credential.CreatedAt = parsed
	return credential, nil
}

func removeCredential(db *sql.DB, table, column string, value any, name string) error {
	result, err := db.Exec(`DELETE FROM `+table+` WHERE `+column+` = ?`, value)
	if err != nil {
		return fmt.Errorf("remove %s: %w", name, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read removed %s count: %w", name, err)
	}
	if count == 0 {
		return fmt.Errorf("%s not found", name)
	}
	return nil
}
