//go:build !windows

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/management"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/statelock"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type cliCommandResult struct {
	stdout string
	stderr string
	exit   int
}

type cliResult[T any] struct {
	OK     bool `json:"ok"`
	Result T    `json:"result"`
}

type cliFailure struct {
	OK    bool `json:"ok"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestMindManagementCLIRealProcessE2E(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	binaries := buildTestBinaries(t, filepath.Join(root, "bin"))

	t.Run("offline online and immediate disconnect", func(t *testing.T) {
		home, workDir := prepareManagementRuntime(t, filepath.Join(root, "online"), true)
		created := runMindCLI(t, binaries.mind, workDir, home,
			"face", "add", "managed-face", "--profile", "operator", "--format", "json")
		requireCLIExit(t, created, 0)
		credential := decodeCLIResult[management.SecretCredentialDTO](t, created.stdout)
		if credential.Token == "" || credential.ApplicationKey == "" {
			t.Fatalf("offline add did not return one-time secrets: %+v", credential)
		}

		mind := startTestProcess(t, "management-mind", binaries.mind, workDir, home)
		ready := awaitReady(t, mind, func(value mindReady) bool { return value.Type == "mind.ready" })
		faceConfig := filepath.Join(root, "online", "face", "config.toml")
		if err := os.MkdirAll(filepath.Dir(faceConfig), 0700); err != nil {
			t.Fatal(err)
		}
		writeFaceConfig(t, faceConfig, ready.WSURL, faceCredentialFromDTO(credential), "headless")
		face := startHeadlessFace(t, binaries.face, workDir, home, faceConfig, "managed-face")

		status := runMindCLI(t, binaries.mind, workDir, home, "status", "--format", "json")
		requireCLIExit(t, status, 0)
		if got := decodeCLIResult[management.StatusResult](t, status.stdout); got.State != "running" || got.PID != mind.cmd.Process.Pid {
			t.Fatalf("online status = %+v", got)
		}
		peers := runMindCLI(t, binaries.mind, workDir, home, "peers", "--format", "json")
		requireCLIExit(t, peers, 0)
		if got := decodeCLIResult[[]management.PeerDTO](t, peers.stdout); !containsPeer(got, "face", "managed-face") {
			t.Fatalf("online peers = %+v", got)
		}

		addedHand := runMindCLI(t, binaries.mind, workDir, home,
			"hand", "add", "managed-hand", "--format", "json")
		requireCLIExit(t, addedHand, 0)
		handCredential := decodeCLIResult[management.SecretCredentialDTO](t, addedHand.stdout)
		listedHands := runMindCLI(t, binaries.mind, workDir, home, "hand", "list", "--format", "json")
		requireCLIExit(t, listedHands, 0)
		if strings.Contains(listedHands.stdout, handCredential.Token) || strings.Contains(listedHands.stdout, handCredential.ApplicationKey) {
			t.Fatalf("hand list leaked one-time secrets: %s", listedHands.stdout)
		}
		removedHand := runMindCLI(t, binaries.mind, workDir, home,
			"hand", "remove", "--label", "managed-hand", "--yes", "--format", "json")
		requireCLIExit(t, removedHand, 0)

		removedFace := runMindCLI(t, binaries.mind, workDir, home,
			"face", "remove", "--label", "managed-face", "--yes", "--format", "json")
		requireCLIExit(t, removedFace, 0)
		if got := decodeCLIResult[management.RemoveResult](t, removedFace.stdout); !got.Disconnected {
			t.Fatalf("online Face removal did not report disconnect: %+v", got)
		}
		if err := face.process.wait(5 * time.Second); err != nil && strings.Contains(err.Error(), "did not exit") {
			t.Fatalf("revoked Face remained connected: %v\n%s", err, face.process.diagnostics())
		}
		if err := mind.interruptAndWait(); err != nil {
			t.Fatalf("stop management Mind: %v\n%s", err, mind.diagnostics())
		}
	})

	t.Run("hub disabled", func(t *testing.T) {
		home, workDir := prepareManagementRuntime(t, filepath.Join(root, "disabled"), false)
		mind := startTestProcess(t, "management-disabled", binaries.mind, workDir, home)
		waitForMindState(t, binaries.mind, workDir, home, "running")

		added := runMindCLI(t, binaries.mind, workDir, home,
			"hand", "add", "disabled-hand", "--format", "json")
		requireCLIExit(t, added, 0)
		removed := runMindCLI(t, binaries.mind, workDir, home,
			"hand", "remove", "--label", "disabled-hand", "--yes", "--format", "json")
		requireCLIExit(t, removed, 0)

		peers := runMindCLI(t, binaries.mind, workDir, home, "peers", "--format", "json")
		requireCLIExit(t, peers, 4)
		failure := decodeCLIFailure(t, peers.stderr)
		if failure.Error.Code != "hub_disabled" {
			t.Fatalf("Hub-disabled error = %+v", failure)
		}
		if err := mind.interruptAndWait(); err != nil {
			t.Fatalf("stop Hub-disabled Mind: %v\n%s", err, mind.diagnostics())
		}
	})

	t.Run("REPL and CLI consistency", func(t *testing.T) {
		home, workDir := prepareManagementRuntime(t, filepath.Join(root, "repl"), true)
		repl := startTestProcess(t, "management-repl", binaries.mind, workDir, home, "--repl")
		waitForMindState(t, binaries.mind, workDir, home, "running")
		added := runMindCLI(t, binaries.mind, workDir, home,
			"hand", "add", "repl-shared", "--format", "json")
		requireCLIExit(t, added, 0)
		if _, err := fmt.Fprintln(repl.stdin, "/hand list"); err != nil {
			t.Fatal(err)
		}
		awaitOutputContains(t, repl, "repl-shared")
		if _, err := fmt.Fprintln(repl.stdin, "exit"); err != nil {
			t.Fatal(err)
		}
		if err := repl.closeInputAndWait(); err != nil {
			t.Fatalf("stop management REPL: %v\n%s", err, repl.diagnostics())
		}
	})

	t.Run("state lock without IPC fails closed", func(t *testing.T) {
		home := filepath.Join(root, "blocked", "home")
		workDir := filepath.Join(root, "blocked", "work")
		if err := os.MkdirAll(workDir, 0700); err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(home, ".half-pi", "run", "mind.lock")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		lock, err := statelock.Acquire(ctx, lockPath, statelock.Info{Mode: "test-holder"})
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()

		blocked := runMindCLI(t, binaries.mind, workDir, home,
			"hand", "add", "must-not-exist", "--format", "json")
		requireCLIExit(t, blocked, 5)
		failure := decodeCLIFailure(t, blocked.stderr)
		if failure.Error.Code != "state_busy" {
			t.Fatalf("blocked mutation error = %+v", failure)
		}
		if _, err := os.Stat(filepath.Join(home, ".half-pi", "db", "half-pi.db")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("blocked CLI opened state database: %v", err)
		}
	})
}

func prepareManagementRuntime(t *testing.T, root string, hubEnabled bool) (string, string) {
	t.Helper()
	home := filepath.Join(root, "home")
	workDir := filepath.Join(root, "work")
	halfPiHome := filepath.Join(home, ".half-pi")
	fixtureDir := filepath.Join(halfPiHome, "fixtures")
	if err := os.MkdirAll(fixtureDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workDir, 0700); err != nil {
		t.Fatal(err)
	}
	config := mindConfig()
	if !hubEnabled {
		config = strings.Replace(config, "enabled = true", "enabled = false", 1)
	}
	writeFile(t, filepath.Join(halfPiHome, "config.toml"), config, 0600)
	writeFile(t, filepath.Join(fixtureDir, "face-e2e.json"), scriptedFixture(), 0600)
	return home, workDir
}

func runMindCLI(t *testing.T, binary, dir, home string, args ...string) cliCommandResult {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Dir = dir
	command.Env = environmentWithHome(home)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := cliCommandResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.exit = exitErr.ExitCode()
		return result
	}
	t.Fatalf("run Mind CLI %v: %v", args, err)
	return cliCommandResult{}
}

func requireCLIExit(t *testing.T, result cliCommandResult, want int) {
	t.Helper()
	if result.exit != want {
		t.Fatalf("CLI exit = %d, want %d\nstdout:\n%sstderr:\n%s", result.exit, want, result.stdout, result.stderr)
	}
}

func decodeCLIResult[T any](t *testing.T, raw string) T {
	t.Helper()
	var response cliResult[T]
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatalf("decode CLI result: %v\n%s", err, raw)
	}
	if !response.OK {
		t.Fatalf("CLI result was not successful: %s", raw)
	}
	return response.Result
}

func decodeCLIFailure(t *testing.T, raw string) cliFailure {
	t.Helper()
	var response cliFailure
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatalf("decode CLI failure: %v\n%s", err, raw)
	}
	if response.OK || response.Error.Code == "" || response.Error.Message == "" {
		t.Fatalf("invalid CLI failure: %+v", response)
	}
	return response
}

func waitForMindState(t *testing.T, binary, dir, home, state string) {
	t.Helper()
	deadline := time.Now().Add(faceMessageTimeout)
	for time.Now().Before(deadline) {
		result := runMindCLI(t, binary, dir, home, "status", "--format", "json")
		if result.exit == 0 {
			status := decodeCLIResult[management.StatusResult](t, result.stdout)
			if status.State == state {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("Mind did not reach state %q", state)
}

func containsPeer(peers []management.PeerDTO, typ, label string) bool {
	for _, peer := range peers {
		if peer.Type == typ && peer.Label == label {
			return true
		}
	}
	return false
}

func faceCredentialFromDTO(credential management.SecretCredentialDTO) *store.FaceCredential {
	return &store.FaceCredential{Credential: store.Credential{
		ID: credential.ID, Label: credential.Label, Token: credential.Token,
		ApplicationKey: credential.ApplicationKey, CreatedAt: credential.CreatedAt,
	}}
}
