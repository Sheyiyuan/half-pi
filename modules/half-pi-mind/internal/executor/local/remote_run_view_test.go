package local

import (
	"context"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

func TestRemoteRunOperationsRejectCrossSession(t *testing.T) {
	runs := remoteexec.NewRegistry()
	if err := runs.Create("run-1", "session-a", "hand-1", "exec_command"); err != nil {
		t.Fatal(err)
	}
	bridge := &RemoteBridge{Hub: hub.New(), Runs: runs, SessionID: func() string { return "session-b" }}
	if _, err := RemoteRunSnapshot(bridge, "run-1"); err == nil {
		t.Fatal("cross-session query should fail")
	}
	if _, err := CancelRemoteRun(context.Background(), bridge, "run-1"); err == nil {
		t.Fatal("cross-session cancellation should fail")
	}
}

func TestRemoteRunViewHasStableFields(t *testing.T) {
	now := time.Now()
	run := remoteexec.Run{
		ID: "run-1", SessionID: "session-1", HandID: "hand-1", Tool: "exec_command",
		Status: protocol.RunSucceeded, CreatedAt: now.Add(-time.Second), FinishedAt: now,
		Result: &protocol.RPCResult{RunID: "run-1", Success: true, Output: "ok", Truncated: true},
	}
	view := remoteRunView(run)
	if view.RunID != "run-1" || view.Status != protocol.RunSucceeded || view.DurationMs < 1000 || !view.Truncated || view.Output != "ok" {
		t.Fatalf("unexpected view: %+v", view)
	}
}

func TestRemoteRunFailureIncludesRunIdentity(t *testing.T) {
	runs := remoteexec.NewRegistry()
	if err := runs.Create("run-failure", "session-1", "hand-1", "exec_command"); err != nil {
		t.Fatal(err)
	}
	bridge := &RemoteBridge{Runs: runs, SessionID: func() string { return "session-1" }}
	result := remoteRunFailure(bridge, "run-failure", "audit failed")
	view, ok := result.Data.(RemoteRunView)
	if result.Error == "" || !ok || view.RunID != "run-failure" || view.Status != protocol.RunCreated {
		t.Fatalf("unstable failure result: %+v", result)
	}
}
