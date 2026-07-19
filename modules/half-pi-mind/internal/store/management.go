package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// ManagementAudit 是本地管理操作的无秘密审计记录。
type ManagementAudit struct {
	RequestID   string
	Source      string
	Actor       string
	Operation   string
	TargetType  string
	TargetID    string
	TargetLabel string
	Status      string
	Code        string
	Message     string
	CreatedAt   time.Time
}

// RemovedCredential 是被撤销凭据的无秘密摘要。
type RemovedCredential struct {
	ID    int64
	Label string
}

// AddManagementAudit 写入一条无秘密管理审计。
func (s *Store) AddManagementAudit(record ManagementAudit) error {
	if err := validateManagementAudit(record); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO management_audits
		(request_id, source, actor, operation, target_type, target_id, target_label, status, code, message, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.RequestID, record.Source, record.Actor, record.Operation, record.TargetType,
		record.TargetID, record.TargetLabel, record.Status, record.Code, record.Message, record.CreatedAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("write management audit: %w", err)
	}
	return nil
}

// AddHandCredentialAudited 创建 Hand 凭据并在同一事务写入成功审计。
func (s *Store) AddHandCredentialAudited(label string, audit ManagementAudit) (*Credential, error) {
	credential, err := GenerateCredential(label)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(
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
	added, err := handCredentialByLabelTx(tx, label)
	if err != nil {
		return nil, fmt.Errorf("read added hand credential: %w", err)
	}
	audit.TargetID = strconv.FormatInt(added.ID, 10)
	audit.TargetLabel = added.Label
	audit.Status = "succeeded"
	if err := insertManagementAudit(tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit hand credential: %w", err)
	}
	return added, nil
}

// AddFaceTokenAudited 创建 Face 凭据并在同一事务写入成功审计。
func (s *Store) AddFaceTokenAudited(label string, scopes []protocol.FaceScope, audit ManagementAudit) (*FaceCredential, error) {
	credential, err := GenerateCredential(label)
	if err != nil {
		return nil, err
	}
	canonical, encoded, err := encodeFaceScopes(scopes)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(
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
	added, err := faceTokenByLabelTx(tx, label)
	if err != nil {
		return nil, fmt.Errorf("read added face token: %w", err)
	}
	added.Scopes = canonical
	audit.TargetID = strconv.FormatInt(added.ID, 10)
	audit.TargetLabel = added.Label
	audit.Status = "succeeded"
	if err := insertManagementAudit(tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit face credential: %w", err)
	}
	return added, nil
}

// RemoveHandCredentialAudited 删除 Hand 凭据并在同一事务写入成功审计。
func (s *Store) RemoveHandCredentialAudited(selector, value string, audit ManagementAudit) (RemovedCredential, error) {
	return removeCredentialAudited(s.db, "hand_credentials", selector, value, audit)
}

// RemoveFaceTokenAudited 删除 Face 凭据并在同一事务写入成功审计。
func (s *Store) RemoveFaceTokenAudited(selector, value string, audit ManagementAudit) (RemovedCredential, error) {
	return removeCredentialAudited(s.db, "face_tokens", selector, value, audit)
}

func removeCredentialAudited(db *sql.DB, table, selector, value string, audit ManagementAudit) (RemovedCredential, error) {
	if selector != "id" && selector != "label" {
		return RemovedCredential{}, fmt.Errorf("invalid credential selector")
	}
	tx, err := db.Begin()
	if err != nil {
		return RemovedCredential{}, err
	}
	defer tx.Rollback()
	removed, err := selectCredentialForUpdate(tx, table, selector, value)
	if err != nil {
		return RemovedCredential{}, err
	}
	result, err := tx.Exec(`DELETE FROM `+table+` WHERE id = ?`, removed.ID)
	if err != nil {
		return RemovedCredential{}, fmt.Errorf("remove credential: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return RemovedCredential{}, fmt.Errorf("read removed credential count: %w", err)
	}
	if count != 1 {
		return RemovedCredential{}, fmt.Errorf("credential not found")
	}
	audit.TargetID = strconv.FormatInt(removed.ID, 10)
	audit.TargetLabel = removed.Label
	audit.Status = "succeeded"
	if err := insertManagementAudit(tx, audit); err != nil {
		return RemovedCredential{}, err
	}
	if err := tx.Commit(); err != nil {
		return RemovedCredential{}, fmt.Errorf("commit remove credential: %w", err)
	}
	return removed, nil
}

func selectCredentialForUpdate(tx *sql.Tx, table, selector, value string) (RemovedCredential, error) {
	var row *sql.Row
	if selector == "id" {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return RemovedCredential{}, fmt.Errorf("invalid credential ID")
		}
		row = tx.QueryRow(`SELECT id, label FROM `+table+` WHERE id = ?`, id)
	} else {
		if err := validateCredentialLabel(value); err != nil {
			return RemovedCredential{}, err
		}
		row = tx.QueryRow(`SELECT id, label FROM `+table+` WHERE label = ?`, value)
	}
	var removed RemovedCredential
	if err := row.Scan(&removed.ID, &removed.Label); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RemovedCredential{}, fmt.Errorf("credential not found")
		}
		return RemovedCredential{}, err
	}
	return removed, nil
}

func handCredentialByLabelTx(tx *sql.Tx, label string) (*Credential, error) {
	row := tx.QueryRow(
		`SELECT id, label, token, application_key, created_at FROM hand_credentials WHERE label = ?`, label,
	)
	credential, err := scanCredential(row)
	if err != nil {
		return nil, err
	}
	return &credential, nil
}

func faceTokenByLabelTx(tx *sql.Tx, label string) (*FaceCredential, error) {
	row := tx.QueryRow(
		`SELECT id, label, token, application_key, scopes, created_at FROM face_tokens WHERE label = ?`, label,
	)
	credential, err := scanFaceCredential(row)
	if err != nil {
		return nil, err
	}
	return &credential, nil
}

func insertManagementAudit(tx *sql.Tx, record ManagementAudit) error {
	if err := validateManagementAudit(record); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO management_audits
		(request_id, source, actor, operation, target_type, target_id, target_label, status, code, message, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.RequestID, record.Source, record.Actor, record.Operation, record.TargetType,
		record.TargetID, record.TargetLabel, record.Status, record.Code, record.Message, record.CreatedAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("write management audit: %w", err)
	}
	return nil
}

func validateManagementAudit(record ManagementAudit) error {
	if record.RequestID == "" || record.Source == "" || record.Actor == "" || record.Operation == "" || record.TargetType == "" ||
		record.Status == "" || record.CreatedAt.IsZero() {
		return fmt.Errorf("management audit fields are required")
	}
	switch record.Source {
	case "repl", "ipc", "offline_cli":
	default:
		return fmt.Errorf("management audit source is invalid")
	}
	if len(record.Message) > 1024 {
		return fmt.Errorf("management audit message is too long")
	}
	return nil
}
