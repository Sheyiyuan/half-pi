package remoteexec

import (
	"sync"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestRegistryTimeoutCancellation(t *testing.T) {
	registry := NewRegistry()
	createSentRun(t, registry, "run-1", "hand-1")
	if requested, err := registry.RequestCancel("run-1", "timeout"); err != nil || !requested {
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
	if requested, err := registry.RequestCancel("run-1", "user"); err != nil || !requested {
		t.Fatal("RequestCancel returned false")
	}
	if err := registry.MarkCancelUnconfirmed("run-1"); err != nil {
		t.Fatal(err)
	}
	run, _ := registry.Snapshot("run-1")
	if run.Status != protocol.RunLost {
		t.Fatalf("status = %s, want lost", run.Status)
	}
}

func TestRegistryDisconnectMarksOnlyMatchingRunsLost(t *testing.T) {
	registry := NewRegistry()
	createSentRun(t, registry, "run-1", "hand-1")
	createSentRun(t, registry, "run-2", "hand-2")
	if err := registry.MarkLostByHand("hand-1"); err != nil {
		t.Fatal(err)
	}
	first, _ := registry.Snapshot("run-1")
	second, _ := registry.Snapshot("run-2")
	if first.Status != protocol.RunLost {
		t.Fatalf("hand-1 status = %s, want lost", first.Status)
	}
	if second.Status != protocol.RunSent {
		t.Fatalf("hand-2 status = %s, want sent", second.Status)
	}
}

func TestRegistryDisconnectMatchesConnectionGeneration(t *testing.T) {
	registry := NewRegistry()
	if err := registry.CreateForPeer("run-1", "session-1", "hand-1", "new-connection", "exec_command", AuditMetadata{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("run-1", protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("run-1", protocol.RunSent); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkLostByPeer("hand-1", "old-connection"); err != nil {
		t.Fatal(err)
	}
	run, _ := registry.Snapshot("run-1")
	if run.Status != protocol.RunSent {
		t.Fatalf("old connection changed run status to %s", run.Status)
	}
	if err := registry.MarkLostByPeer("hand-1", "new-connection"); err != nil {
		t.Fatal(err)
	}
	run, _ = registry.Snapshot("run-1")
	if run.Status != protocol.RunLost {
		t.Fatalf("new connection status = %s, want lost", run.Status)
	}
}

func TestRegistryAuditFailureDoesNotMutateErrorDetails(t *testing.T) {
	auditor := &recordingAuditor{}
	registry := NewRegistry(auditor)
	createSentRun(t, registry, "run-1", "hand-1")
	if _, err := registry.RequestCancel("run-1", "user"); err != nil {
		t.Fatal(err)
	}
	auditor.mu.Lock()
	auditor.failTransitions = true
	auditor.mu.Unlock()
	if err := registry.ApplyCancelResult("hand-1", protocol.RPCCancelResult{RunID: "run-1", Status: protocol.CancelUnknownRun}); err == nil {
		t.Fatal("audit failure should fail cancel result")
	}
	run, _ := registry.Snapshot("run-1")
	if run.Status != protocol.RunCancelRequested || run.Error != "" {
		t.Fatalf("memory mutated after audit failure: %+v", run)
	}
}

func TestRegistryResultCancelRaceHasOneTerminalState(t *testing.T) {
	for i := 0; i < 100; i++ {
		registry := NewRegistry()
		createSentRun(t, registry, "run-1", "hand-1")
		if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{RunID: "run-1", StartedAt: time.Now().UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		if requested, err := registry.RequestCancel("run-1", "user"); err != nil || !requested {
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
