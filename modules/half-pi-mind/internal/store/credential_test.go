package store

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func newCredentialTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credential.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New(%q): %v", path, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestGenerateCredential(t *testing.T) {
	first, err := GenerateCredential("office.pc-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateCredential("office.pc-2")
	if err != nil {
		t.Fatal(err)
	}
	for name, secret := range map[string]string{
		"token":           first.Token,
		"application key": first.ApplicationKey,
	} {
		decoded, err := hex.DecodeString(secret)
		if err != nil || len(decoded) != credentialBytes {
			t.Errorf("%s = %q, want %d-byte lowercase hex: %v", name, secret, credentialBytes, err)
		}
		if secret != strings.ToLower(secret) {
			t.Errorf("%s is not lowercase: %q", name, secret)
		}
	}
	if first.Token == first.ApplicationKey {
		t.Fatal("token and application key must be independent")
	}
	if first.Token == second.Token || first.ApplicationKey == second.ApplicationKey {
		t.Fatal("separately generated credentials reused a secret")
	}
	if err := ValidateCredential(*first); err != nil {
		t.Fatalf("ValidateCredential: %v", err)
	}
}

func TestCredentialLabels(t *testing.T) {
	valid := []string{"a", "A0", "hand.one_two-three", strings.Repeat("a", 64)}
	for _, label := range valid {
		if _, err := GenerateCredential(label); err != nil {
			t.Errorf("GenerateCredential(%q): %v", label, err)
		}
	}

	invalid := []string{"", "-hand", ".face", "under score", "face/one", "é", strings.Repeat("a", 65)}
	for _, label := range invalid {
		if _, err := GenerateCredential(label); err == nil {
			t.Errorf("GenerateCredential(%q) succeeded", label)
		}
	}
}

func TestValidateCredentialRejectsBadSecrets(t *testing.T) {
	credential, err := GenerateCredential("valid")
	if err != nil {
		t.Fatal(err)
	}
	tests := []Credential{
		{Label: credential.Label, Token: "", ApplicationKey: credential.ApplicationKey},
		{Label: credential.Label, Token: strings.Repeat("g", 32), ApplicationKey: credential.ApplicationKey},
		{Label: credential.Label, Token: strings.ToUpper(credential.Token), ApplicationKey: credential.ApplicationKey},
		{Label: credential.Label, Token: credential.Token, ApplicationKey: "abcd"},
		{Label: credential.Label, Token: credential.Token, ApplicationKey: credential.Token},
	}
	for _, test := range tests {
		if err := ValidateCredential(test); err == nil {
			t.Errorf("ValidateCredential(%+v) succeeded", test)
		}
	}
}

func TestCanonicalFaceScopes(t *testing.T) {
	input := []protocol.FaceScope{
		protocol.FaceScopeTasksRead,
		protocol.FaceScopeRunsRead,
		protocol.FaceScopeChat,
		protocol.FaceScopeRunsRead,
		protocol.FaceScopeTasksCancel,
	}
	want := []protocol.FaceScope{
		protocol.FaceScopeChat,
		protocol.FaceScopeRunsRead,
		protocol.FaceScopeTasksCancel,
		protocol.FaceScopeTasksRead,
	}
	got, err := CanonicalFaceScopes(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalFaceScopes = %v, want %v", got, want)
	}
	if _, err := CanonicalFaceScopes(nil); err == nil {
		t.Fatal("empty scopes accepted")
	}
	if _, err := CanonicalFaceScopes([]protocol.FaceScope{"face:unknown"}); err == nil {
		t.Fatal("unknown scope accepted")
	}
}

func TestCanonicalFaceScopesAcceptsCompleteSet(t *testing.T) {
	scopes := []protocol.FaceScope{
		protocol.FaceScopeChat,
		protocol.FaceScopeSessionsRead,
		protocol.FaceScopeSessionsWrite,
		protocol.FaceScopeRunsRead,
		protocol.FaceScopeRunsCancel,
		protocol.FaceScopeApprove,
		protocol.FaceScopeHandsRead,
		protocol.FaceScopeTasksRead,
		protocol.FaceScopeTasksCancel,
	}
	got, err := CanonicalFaceScopes(scopes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(scopes) {
		t.Fatalf("canonical scope count = %d, want %d", len(got), len(scopes))
	}
}

func TestHandCredentialCRUDAndAuthentication(t *testing.T) {
	s := newCredentialTestStore(t)
	credential, err := s.AddHandCredential("office-pc")
	if err != nil {
		t.Fatal(err)
	}
	if credential.ID <= 0 || credential.CreatedAt.IsZero() {
		t.Fatalf("incomplete credential: %+v", credential)
	}
	if _, err := s.AddHandCredential("office-pc"); err == nil {
		t.Fatal("duplicate Hand label accepted")
	}
	got, err := s.AuthenticateHandCredential(credential.Label, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, credential) {
		t.Fatalf("authenticated credential = %+v, want %+v", got, credential)
	}
	for _, attempt := range [][2]string{
		{"other-hand", credential.Token},
		{credential.Label, strings.Repeat("0", 32)},
		{"bad label", credential.Token},
		{credential.Label, "not-hex"},
	} {
		if _, err := s.AuthenticateHandCredential(attempt[0], attempt[1]); err == nil {
			t.Errorf("AuthenticateHandCredential(%q, %q) succeeded", attempt[0], attempt[1])
		}
	}
	list, err := s.ListHandCredentials()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListHandCredentials = %v, %v", list, err)
	}
	if err := s.RemoveHandCredentialByLabel(credential.Label); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveHandCredential(credential.ID); err == nil {
		t.Fatal("removed Hand credential a second time")
	}
	second, err := s.AddHandCredential("remove-by-id")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveHandCredential(second.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFaceTokenCRUDCanonicalScopesAndAuthentication(t *testing.T) {
	s := newCredentialTestStore(t)
	face, err := s.AddFaceToken("operator", []protocol.FaceScope{
		protocol.FaceScopeRunsRead,
		protocol.FaceScopeChat,
		protocol.FaceScopeRunsRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantScopes := []protocol.FaceScope{protocol.FaceScopeChat, protocol.FaceScopeRunsRead}
	if !reflect.DeepEqual(face.Scopes, wantScopes) {
		t.Fatalf("scopes = %v, want %v", face.Scopes, wantScopes)
	}
	var encoded string
	if err := s.db.QueryRow(`SELECT scopes FROM face_tokens WHERE id = ?`, face.ID).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if encoded != `["face:chat","face:runs:read"]` {
		t.Fatalf("stored scopes = %q", encoded)
	}
	got, err := s.AuthenticateFaceToken(face.Label, face.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, face) {
		t.Fatalf("authenticated Face token = %+v, want %+v", got, face)
	}
	identity, err := s.FaceIdentityByLabel(face.Label)
	if err != nil {
		t.Fatalf("FaceIdentityByLabel: %v", err)
	}
	if identity == nil || identity.ID != fmt.Sprint(face.ID) || identity.Label != face.Label || !reflect.DeepEqual(identity.Scopes, wantScopes) {
		t.Fatalf("FaceIdentityByLabel = %+v", identity)
	}
	missing, err := s.FaceIdentityByLabel("missing")
	if err != nil || missing != nil {
		t.Fatalf("missing FaceIdentityByLabel = %+v, %v", missing, err)
	}
	if _, err := s.AddFaceToken("operator", wantScopes); err == nil {
		t.Fatal("duplicate Face label accepted")
	}
	if _, err := s.AddFaceToken("empty-scopes", nil); err == nil {
		t.Fatal("empty Face scopes accepted")
	}
	if _, err := s.AuthenticateFaceToken(face.Label, strings.Repeat("0", 32)); err == nil {
		t.Fatal("wrong Face token authenticated")
	}
	list, err := s.ListFaceTokens()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListFaceTokens = %v, %v", list, err)
	}
	if err := s.RemoveFaceToken(face.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFaceTokenByLabel(face.Label); err == nil {
		t.Fatal("removed Face token a second time")
	}
	second, err := s.AddFaceToken("remove-by-label", []protocol.FaceScope{protocol.FaceScopeChat})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFaceTokenByLabel(second.Label); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialNamespacesAllowSameLabel(t *testing.T) {
	s := newCredentialTestStore(t)
	hand, err := s.AddHandCredential("shared-label")
	if err != nil {
		t.Fatal(err)
	}
	face, err := s.AddFaceToken("shared-label", []protocol.FaceScope{protocol.FaceScopeChat})
	if err != nil {
		t.Fatal(err)
	}
	if hand.Token == face.Token || hand.ApplicationKey == face.ApplicationKey {
		t.Fatal("Hand and Face credentials reused a secret")
	}
}

func TestCredentialSecretUniqueness(t *testing.T) {
	s := newCredentialTestStore(t)
	hand, err := s.AddHandCredential("hand-one")
	if err != nil {
		t.Fatal(err)
	}
	validOther := strings.Repeat("1", 32)
	if _, err := s.db.Exec(
		`INSERT INTO hand_credentials (label, token, application_key) VALUES (?, ?, ?)`,
		"hand-two", hand.Token, validOther,
	); err == nil {
		t.Fatal("duplicate Hand token accepted")
	}
	if _, err := s.db.Exec(
		`INSERT INTO hand_credentials (label, token, application_key) VALUES (?, ?, ?)`,
		"hand-three", validOther, hand.ApplicationKey,
	); err == nil {
		t.Fatal("duplicate Hand application key accepted")
	}

	face, err := s.AddFaceToken("face-one", []protocol.FaceScope{protocol.FaceScopeChat})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO face_tokens (label, token, application_key, scopes) VALUES (?, ?, ?, ?)`,
		"face-two", face.Token, validOther, `["face:chat"]`,
	); err == nil {
		t.Fatal("duplicate Face token accepted")
	}
	if _, err := s.db.Exec(
		`INSERT INTO face_tokens (label, token, application_key, scopes) VALUES (?, ?, ?, ?)`,
		"face-three", validOther, face.ApplicationKey, `["face:chat"]`,
	); err == nil {
		t.Fatal("duplicate Face application key accepted")
	}
}

func TestBadCredentialRecordsFailClosed(t *testing.T) {
	tests := []struct {
		name      string
		label     string
		token     string
		key       string
		scopes    string
		createdAt string
	}{
		{name: "label", label: "bad label", token: strings.Repeat("1", 32), key: strings.Repeat("2", 32), scopes: `["face:chat"]`, createdAt: "2026-07-18 12:00:00"},
		{name: "token", label: "bad-token", token: "invalid", key: strings.Repeat("2", 32), scopes: `["face:chat"]`, createdAt: "2026-07-18 12:00:00"},
		{name: "key", label: "bad-key", token: strings.Repeat("1", 32), key: "invalid", scopes: `["face:chat"]`, createdAt: "2026-07-18 12:00:00"},
		{name: "JSON", label: "bad-json", token: strings.Repeat("1", 32), key: strings.Repeat("2", 32), scopes: `{`, createdAt: "2026-07-18 12:00:00"},
		{name: "empty scopes", label: "empty", token: strings.Repeat("1", 32), key: strings.Repeat("2", 32), scopes: `[]`, createdAt: "2026-07-18 12:00:00"},
		{name: "unknown scope", label: "unknown", token: strings.Repeat("1", 32), key: strings.Repeat("2", 32), scopes: `["face:unknown"]`, createdAt: "2026-07-18 12:00:00"},
		{name: "unsorted", label: "unsorted", token: strings.Repeat("1", 32), key: strings.Repeat("2", 32), scopes: `["face:runs:read","face:chat"]`, createdAt: "2026-07-18 12:00:00"},
		{name: "duplicate scope", label: "duplicate", token: strings.Repeat("1", 32), key: strings.Repeat("2", 32), scopes: `["face:chat","face:chat"]`, createdAt: "2026-07-18 12:00:00"},
		{name: "whitespace", label: "whitespace", token: strings.Repeat("1", 32), key: strings.Repeat("2", 32), scopes: `[ "face:chat" ]`, createdAt: "2026-07-18 12:00:00"},
		{name: "created at", label: "bad-time", token: strings.Repeat("1", 32), key: strings.Repeat("2", 32), scopes: `["face:chat"]`, createdAt: "not-a-time"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := newCredentialTestStore(t)
			if _, err := s.db.Exec(
				`INSERT INTO face_tokens (label, token, application_key, scopes, created_at) VALUES (?, ?, ?, ?, ?)`,
				test.label, test.token, test.key, test.scopes, test.createdAt,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ListFaceTokens(); err == nil {
				t.Fatal("bad Face record was listed")
			}
			if _, err := s.AuthenticateFaceToken(test.label, test.token); err == nil {
				t.Fatal("bad Face record authenticated")
			}
		})
	}
}

func TestBadHandCredentialRecordFailsClosed(t *testing.T) {
	s := newCredentialTestStore(t)
	if _, err := s.db.Exec(
		`INSERT INTO hand_credentials (label, token, application_key) VALUES (?, ?, ?)`,
		"bad-hand", strings.Repeat("1", 32), "bad-key",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListHandCredentials(); err == nil {
		t.Fatal("bad Hand credential was listed")
	}
	if _, err := s.AuthenticateHandCredential("bad-hand", strings.Repeat("1", 32)); err == nil {
		t.Fatal("bad Hand credential authenticated")
	}
}

func TestCredentialMigrationIsIdempotentAndDoesNotCopyLegacyTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	legacyToken := strings.Repeat("a", 32)
	if _, err := s.db.Exec(
		`INSERT INTO hand_tokens (label, hand_id, token) VALUES (?, ?, ?)`,
		"legacy", "legacy", legacyToken,
	); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = New(path)
	if err != nil {
		t.Fatalf("reopen and migrate: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	for _, table := range []string{"hand_credentials", "face_tokens"} {
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s has %d copied records, want 0", table, count)
		}
	}
	if _, err := s.AuthenticateHandCredential("legacy", legacyToken); err == nil {
		t.Fatal("legacy hand_tokens record authenticated through new API")
	}
	if _, err := s.AuthenticateHandToken(legacyToken, "legacy"); err == nil {
		t.Fatal("legacy hand_tokens record authenticated through compatibility API")
	}
	var legacyCount int
	if err := s.db.QueryRow(`SELECT count(*) FROM hand_tokens`).Scan(&legacyCount); err != nil {
		t.Fatal(err)
	}
	if legacyCount != 1 {
		t.Fatalf("legacy record count = %d, want 1", legacyCount)
	}
}
