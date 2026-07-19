package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/management"
)

func TestRunUnknownRootCommandDoesNotInitializeRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var output, logs bytes.Buffer
	err := run([]string{"unknown"}, &output, &logs)
	var cliErr *cliError
	if !errors.As(err, &cliErr) || cliErr.exit != 2 {
		t.Fatalf("unknown command error = %#v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".half-pi")); !os.IsNotExist(statErr) {
		t.Fatalf("unknown command initialized runtime: %v", statErr)
	}
}

func TestCLIRejectsInvalidAddFormatBeforeInitializing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var output, logs bytes.Buffer
	err := run([]string{"hand", "add", "office", "--format", "xml"}, &output, &logs)
	var cliErr *cliError
	if !errors.As(err, &cliErr) || cliErr.exit != 2 {
		t.Fatalf("invalid format error = %#v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".half-pi")); !os.IsNotExist(statErr) {
		t.Fatalf("invalid format initialized runtime: %v", statErr)
	}
}

func TestCLIOfflineHandAddListAndRemove(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var output, logs bytes.Buffer
	if err := run([]string{"hand", "add", "office", "--format", "json"}, &output, &logs); err != nil {
		t.Fatal(err)
	}
	var added struct {
		OK     bool `json:"ok"`
		Result struct {
			ID             int64  `json:"id"`
			Label          string `json:"label"`
			Token          string `json:"token"`
			ApplicationKey string `json:"application_key"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &added); err != nil {
		t.Fatalf("add JSON: %v: %s", err, output.String())
	}
	if !added.OK || added.Result.Label != "office" || added.Result.Token == "" || added.Result.ApplicationKey == "" {
		t.Fatalf("add result = %+v", added)
	}

	output.Reset()
	if err := run([]string{"hand", "list", "--format", "json"}, &output, &logs); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), added.Result.Token) || strings.Contains(output.String(), added.Result.ApplicationKey) {
		t.Fatalf("list leaked secrets: %s", output.String())
	}

	output.Reset()
	if err := run([]string{"hand", "remove", "--label", "office", "--yes"}, &output, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `credential "office" removed`) {
		t.Fatalf("remove output = %q", output.String())
	}
}

func TestCLIRemoveRequiresYes(t *testing.T) {
	var output, logs bytes.Buffer
	err := runCredentialRemove(
		"hand",
		[]string{"--label", "office"},
		&output,
		strings.NewReader(""),
		&logs,
		false,
	)
	if got, ok := err.(*cliError); !ok || got.exit != 2 {
		t.Fatalf("remove without --yes error = %#v", err)
	}
}

func TestConfirmRemoval(t *testing.T) {
	var prompt bytes.Buffer
	if err := confirmRemoval(strings.NewReader("yes\n"), &prompt, "hand", 7, "office"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.String(), `Remove hand credential (label "office")?`) {
		t.Fatalf("prompt = %q", prompt.String())
	}
	if err := confirmRemoval(strings.NewReader("n\n"), io.Discard, "face", 7, ""); err == nil {
		t.Fatal("negative confirmation accepted")
	}
}

func TestCredentialRemoveValidatesSelectorBeforeConfirmation(t *testing.T) {
	err := runCredentialRemove("hand", []string{}, io.Discard, strings.NewReader("yes\n"), io.Discard, true)
	var cliErr *cliError
	if !errors.As(err, &cliErr) || cliErr.code != "invalid_argument" || cliErr.exit != 3 {
		t.Fatalf("missing selector error = %#v", err)
	}
}

func TestExitForCodeMapping(t *testing.T) {
	tests := map[string]int{
		"usage": 2, "invalid_argument": 3, "invalid_config": 3,
		"mind_not_running": 4, "hub_disabled": 4,
		"state_busy": 5, "control_unavailable": 5, "result_unknown": 5,
		"not_found": 6, "conflict": 6, "permission_denied": 6,
	}
	for code, want := range tests {
		if got := exitForCode(code); got != want {
			t.Errorf("exitForCode(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestWriteRunErrorUsesJSONOnStderr(t *testing.T) {
	var output bytes.Buffer
	exit := writeRunError(
		[]string{"hand", "list", "--format", "json"},
		&output,
		&cliError{code: "control_unavailable", exit: 5, msg: "control channel failed"},
	)
	if exit != 5 {
		t.Fatalf("exit = %d, want 5", exit)
	}
	var response struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode JSON error: %v: %s", err, output.String())
	}
	if response.OK || response.Error.Code != "control_unavailable" || response.Error.Message != "control channel failed" {
		t.Fatalf("JSON error = %+v", response)
	}
}

func TestWriteRunErrorUsesTextByDefault(t *testing.T) {
	var output bytes.Buffer
	exit := writeRunError([]string{"hand", "list"}, &output, usage("bad arguments"))
	if exit != 2 || output.String() != "usage: bad arguments\n" {
		t.Fatalf("text error exit=%d output=%q", exit, output.String())
	}
}

func TestValidateConfigRejectsUnsupportedAdapter(t *testing.T) {
	if err := management.ValidateConfig(&config.Config{}); err == nil {
		t.Fatal("empty config accepted")
	}
}
