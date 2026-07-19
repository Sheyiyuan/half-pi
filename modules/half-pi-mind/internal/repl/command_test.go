package repl

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/management"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func TestHandListCommandKeepsLegacyAlias(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "repl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := &Repl{store: db, management: management.New(db, nil, management.Runtime{Mode: "test"})}

	if !r.handleCommand("/hand") {
		t.Fatal("legacy /hand command was not handled")
	}
	if !r.handleCommand("/hand list") {
		t.Fatal("/hand list command was not handled")
	}
}

func TestFaceAddCommandSupportsProfileAndTOML(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "repl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := &Repl{management: management.New(db, nil, management.Runtime{Mode: "test"})}
	var output bytes.Buffer

	r.handleFaceAdd("terminal --profile operator --format toml", &output)

	if !strings.Contains(output.String(), "[face]\nid = \"terminal\"\nmode = \"tui\"") ||
		!strings.Contains(output.String(), "[server]\ntoken = ") ||
		!strings.Contains(output.String(), "application_key = ") {
		t.Fatalf("TOML output = %q", output.String())
	}
	credentials, err := r.management.ListFaces()
	if err != nil {
		t.Fatal(err)
	}
	wantScopes, err := management.ExpandProfile(management.ProfileOperator)
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0].Label != "terminal" || credentials[0].Scopes == nil {
		t.Fatalf("credentials = %+v", credentials)
	}
	if got, want := strings.Join(credentials[0].Scopes, ","), joinScopes(wantScopes); got != want {
		t.Fatalf("scopes = %q, want %q", got, want)
	}
}

func TestParseFaceAddOptionsMatchesCLIContract(t *testing.T) {
	options, err := parseFaceAddOptions("viewer --scopes face:runs:read,face:sessions:read --format=json")
	if err != nil {
		t.Fatal(err)
	}
	if options.label != "viewer" || options.format != "json" {
		t.Fatalf("options = %+v", options)
	}
	wantScopes := []protocol.FaceScope{protocol.FaceScopeRunsRead, protocol.FaceScopeSessionsRead}
	if got, want := joinScopes(options.scopes), joinScopes(wantScopes); got != want {
		t.Fatalf("scopes = %q, want %q", got, want)
	}

	for _, args := range []string{
		"viewer",
		"viewer --profile observer --scopes face:sessions:read",
		"viewer --profile admin",
		"viewer --profile observer --format xml",
	} {
		if _, err := parseFaceAddOptions(args); err == nil {
			t.Fatalf("parseFaceAddOptions(%q) succeeded", args)
		}
	}
}

func TestWriteFaceCredentialJSONUsesCLIEnvelope(t *testing.T) {
	credential := management.SecretCredentialDTO{
		ID:             7,
		Label:          "terminal",
		Token:          strings.Repeat("1", 32),
		ApplicationKey: strings.Repeat("2", 32),
		Scopes:         []string{string(protocol.FaceScopeChat)},
	}
	var output bytes.Buffer
	if err := writeFaceCredential(&output, credential, "json"); err != nil {
		t.Fatal(err)
	}
	var response struct {
		OK     bool                           `json:"ok"`
		Result management.SecretCredentialDTO `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode output: %v: %s", err, output.String())
	}
	if !response.OK || response.Result.Label != credential.Label || response.Result.Token != credential.Token {
		t.Fatalf("response = %+v", response)
	}
}
