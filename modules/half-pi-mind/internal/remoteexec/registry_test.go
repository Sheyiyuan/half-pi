package remoteexec

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func createSentRun(t *testing.T, registry *Registry, id, handID string) {
	t.Helper()
	if err := registry.Create(id, "session-1", handID, "exec_command"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition(id, protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition(id, protocol.RunSent); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrySuccessLifecycle(t *testing.T) {
	registry := NewRegistry()
	createSentRun(t, registry, "run-1", "hand-1")
	done, _ := registry.Done("run-1")
	if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{RunID: "run-1", StartedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyResult("hand-1", protocol.RPCResult{RunID: "run-1", Success: true, Output: "ok"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	default:
		t.Fatal("terminal run did not close done channel")
	}
	run, _ := registry.Snapshot("run-1")
	if run.Status != protocol.RunSucceeded || run.Result == nil || run.Result.Output != "ok" {
		t.Fatalf("unexpected run: %+v", run)
	}
}

func TestRegistryRejectsWrongHand(t *testing.T) {
	registry := NewRegistry()
	createSentRun(t, registry, "run-1", "hand-1")
	if err := registry.ApplyResult("hand-2", protocol.RPCResult{RunID: "run-1", Success: true}); err == nil {
		t.Fatal("wrong Hand result must be rejected")
	}
	run, _ := registry.Snapshot("run-1")
	if run.Status != protocol.RunSent {
		t.Fatalf("status changed after wrong Hand result: %s", run.Status)
	}
}

func TestRegistryTimeoutCancellation(t *testing.T) {
	registry := NewRegistry()
	createSentRun(t, registry, "run-1", "hand-1")
	if !registry.RequestCancel("run-1", "timeout") {
		t.Fatal("RequestCancel returned false")
	}
	if err := registry.ApplyCancelResult("hand-1", protocol.RPCCancelResult{RunID: "run-1", Status: protocol.CancelCancelled}); err != nil {
		t.Fatal(err)
	}
	run, _ := registry.Snapshot("run-1")
	if run.Status != protocol.RunTimedOut {
		t.Fatalf("status = %s, want timed_out", run.Status)
	}
}

func TestRegistryUnconfirmedUserCancelBecomesLost(t *testing.T) {
	registry := NewRegistry()
	createSentRun(t, registry, "run-1", "hand-1")
	if !registry.RequestCancel("run-1", "user") {
		t.Fatal("RequestCancel returned false")
	}
	registry.MarkCancelUnconfirmed("run-1")
	run, _ := registry.Snapshot("run-1")
	if run.Status != protocol.RunLost {
		t.Fatalf("status = %s, want lost", run.Status)
	}
}

func TestRegistryDisconnectMarksOnlyMatchingRunsLost(t *testing.T) {
	registry := NewRegistry()
	createSentRun(t, registry, "run-1", "hand-1")
	createSentRun(t, registry, "run-2", "hand-2")
	registry.MarkLostByHand("hand-1")
	first, _ := registry.Snapshot("run-1")
	second, _ := registry.Snapshot("run-2")
	if first.Status != protocol.RunLost {
		t.Fatalf("hand-1 status = %s, want lost", first.Status)
	}
	if second.Status != protocol.RunSent {
		t.Fatalf("hand-2 status = %s, want sent", second.Status)
	}
}

func TestRegistryResultCancelRaceHasOneTerminalState(t *testing.T) {
	for i := 0; i < 100; i++ {
		registry := NewRegistry()
		createSentRun(t, registry, "run-1", "hand-1")
		if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{RunID: "run-1", StartedAt: time.Now().UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		if !registry.RequestCancel("run-1", "user") {
			t.Fatal("RequestCancel returned false")
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = registry.ApplyResult("hand-1", protocol.RPCResult{RunID: "run-1", Success: true})
		}()
		go func() {
			defer wg.Done()
			_ = registry.ApplyCancelResult("hand-1", protocol.RPCCancelResult{RunID: "run-1", Status: protocol.CancelCancelled})
		}()
		wg.Wait()
		run, _ := registry.Snapshot("run-1")
		if run.Status != protocol.RunSucceeded && run.Status != protocol.RunCancelled {
			t.Fatalf("unexpected terminal status: %s", run.Status)
		}
	}
}

func TestRegistryBoundsTerminalRetention(t *testing.T) {
	registry := NewRegistry()
	for i := 0; i <= maxTerminalRuns; i++ {
		id := fmt.Sprintf("run-%03d", i)
		createSentRun(t, registry, id, "hand-1")
		if err := registry.ApplyRejected("hand-1", protocol.RPCRejected{RunID: id, Code: protocol.RejectUnknownTool}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := registry.Snapshot("run-000"); ok {
		t.Fatal("oldest terminal run should have been pruned")
	}
	if _, ok := registry.Snapshot(fmt.Sprintf("run-%03d", maxTerminalRuns)); !ok {
		t.Fatal("newest terminal run should be retained")
	}
}

func TestRegistryDoesNotPruneActiveWaiter(t *testing.T) {
	registry := NewRegistry()
	createSentRun(t, registry, "waited", "hand-1")
	_, release, ok := registry.Wait("waited")
	if !ok {
		t.Fatal("Wait returned false")
	}
	defer release()
	if err := registry.ApplyRejected("hand-1", protocol.RPCRejected{RunID: "waited", Code: protocol.RejectUnknownTool}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= maxTerminalRuns; i++ {
		id := fmt.Sprintf("other-%03d", i)
		createSentRun(t, registry, id, "hand-1")
		if err := registry.ApplyRejected("hand-1", protocol.RPCRejected{RunID: id, Code: protocol.RejectUnknownTool}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := registry.Snapshot("waited"); !ok {
		t.Fatal("terminal run with active waiter was pruned")
	}
}
