package store

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

const credentialBytes = 16

var credentialLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Credential 是 Hand 和 Face 共用的长期凭据。
type Credential struct {
	ID             int64
	Label          string
	Token          string
	ApplicationKey string
	CreatedAt      time.Time
}

// GenerateCredential 为 label 生成独立的 token 和 application key。
func GenerateCredential(label string) (*Credential, error) {
	if err := validateCredentialLabel(label); err != nil {
		return nil, err
	}
	token, err := generateCredentialSecret()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	applicationKey, err := generateCredentialSecret()
	if err != nil {
		return nil, fmt.Errorf("generate application key: %w", err)
	}
	for applicationKey == token {
		applicationKey, err = generateCredentialSecret()
		if err != nil {
			return nil, fmt.Errorf("generate application key: %w", err)
		}
	}
	return &Credential{Label: label, Token: token, ApplicationKey: applicationKey}, nil
}

// ValidateCredential 校验凭据中不可变身份和秘密的规范格式。
func ValidateCredential(credential Credential) error {
	if err := validateCredentialLabel(credential.Label); err != nil {
		return err
	}
	if err := validateCredentialSecret("token", credential.Token); err != nil {
		return err
	}
	if err := validateCredentialSecret("application key", credential.ApplicationKey); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(credential.Token), []byte(credential.ApplicationKey)) == 1 {
		return fmt.Errorf("token and application key must differ")
	}
	return nil
}

func validateCredentialLabel(label string) error {
	if !credentialLabelPattern.MatchString(label) {
		return fmt.Errorf("invalid credential label %q", label)
	}
	return nil
}

func validateCredentialSecret(name, secret string) error {
	if len(secret) != credentialBytes*2 {
		return fmt.Errorf("invalid %s", name)
	}
	decoded, err := hex.DecodeString(secret)
	if err != nil || len(decoded) != credentialBytes || hex.EncodeToString(decoded) != secret {
		return fmt.Errorf("invalid %s", name)
	}
	return nil
}

func generateCredentialSecret() (string, error) {
	b := make([]byte, credentialBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func credentialMatches(gotLabel, gotToken, wantLabel, wantToken string) bool {
	labelMatch := subtle.ConstantTimeCompare([]byte(gotLabel), []byte(wantLabel))
	tokenMatch := subtle.ConstantTimeCompare([]byte(gotToken), []byte(wantToken))
	return labelMatch&tokenMatch == 1
}

func validateCredentialRecord(credential Credential, createdAt string) (time.Time, error) {
	if credential.ID <= 0 {
		return time.Time{}, fmt.Errorf("invalid credential ID")
	}
	if err := ValidateCredential(credential); err != nil {
		return time.Time{}, err
	}
	parsed, err := time.Parse(sqliteTimeFormat, createdAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid credential creation time: %w", err)
	}
	return parsed, nil
}
