package remoteexec

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

type recordingAuditor struct {
	mu              sync.Mutex
	creates         []AuditRun
	transitions     []AuditTransition
	failTransitions bool
}

func (a *recordingAuditor) CreateRemoteRun(run AuditRun) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.creates = append(a.creates, run)
	return nil
}

func (a *recordingAuditor) TransitionRemoteRun(change AuditTransition) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failTransitions {
		return errors.New("audit unavailable")
	}
	a.transitions = append(a.transitions, change)
	return nil
}

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

func TestRegistryAuditFailureDoesNotAdvanceMemoryState(t *testing.T) {
	auditor := &recordingAuditor{failTransitions: true}
	registry := NewRegistry(auditor)
	if err := registry.Create("run-1", "session-1", "hand-1", "exec_command"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("run-1", protocol.RunApproved); err == nil {
		t.Fatal("transition should fail when audit persistence fails")
	}
	run, _ := registry.Snapshot("run-1")
	if run.Status != protocol.RunCreated {
		t.Fatalf("status = %s, want created", run.Status)
	}
}

func TestRegistryAuditsAcceptedAndRunningSeparately(t *testing.T) {
	auditor := &recordingAuditor{}
	registry := NewRegistry(auditor)
	createSentRun(t, registry, "run-1", "hand-1")
	if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{RunID: "run-1", StartedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	count := len(auditor.transitions)
	if count < 2 || auditor.transitions[count-2].ToStatus != protocol.RunAccepted || auditor.transitions[count-1].ToStatus != protocol.RunRunning {
		t.Fatalf("accepted transitions were not audited separately: %+v", auditor.transitions)
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
