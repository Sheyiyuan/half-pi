package remoteexec

import (
	"errors"
	"strings"
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
	progresses      []AuditProgress
	failProgress    bool
}

func (a *recordingAuditor) AppendRemoteRunProgress(progress AuditProgress) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failProgress {
		return errors.New("progress audit unavailable")
	}
	a.progresses = append(a.progresses, progress)
	return nil
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

func TestRegistryProgressMonotonicGapAndBestEffortAudit(t *testing.T) {
	auditor := &recordingAuditor{}
	registry := NewRegistry(auditor)
	createSentRun(t, registry, "progress-run", "hand-1")
	preAccepted, err := registry.ApplyProgressFrom("hand-1", "", protocol.RPCProgress{
		RunID: "progress-run", Seq: 1, Kind: protocol.ProgressStdout, Data: "early",
	})
	if err == nil || preAccepted.Accepted {
		t.Fatalf("pre-accepted progress = %+v, %v", preAccepted, err)
	}
	if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{
		RunID: "progress-run", StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	first, err := registry.ApplyProgressFrom("hand-1", "", protocol.RPCProgress{
		RunID: "progress-run", Seq: 1, Kind: protocol.ProgressStdout, Data: "one",
	})
	if err != nil || !first.Accepted || first.Gap {
		t.Fatalf("first = %+v, %v", first, err)
	}
	duplicate, err := registry.ApplyProgressFrom("hand-1", "", protocol.RPCProgress{
		RunID: "progress-run", Seq: 1, Kind: protocol.ProgressStdout, Data: "duplicate",
	})
	if err != nil || duplicate.Accepted {
		t.Fatalf("duplicate = %+v, %v", duplicate, err)
	}
	auditor.mu.Lock()
	auditor.failProgress = true
	auditor.mu.Unlock()
	gapped, err := registry.ApplyProgressFrom("hand-1", "", protocol.RPCProgress{
		RunID: "progress-run", Seq: 3, Kind: protocol.ProgressStderr, Data: "three",
	})
	if err != nil || !gapped.Accepted || !gapped.Gap {
		t.Fatalf("gapped = %+v, %v", gapped, err)
	}
	run, _ := registry.Snapshot("progress-run")
	if run.Status != protocol.RunRunning || run.ProgressSeq != 3 || run.ProgressEvents != 2 || run.ProgressBytes != 8 {
		t.Fatalf("run = %+v", run)
	}
	if _, err := registry.ApplyProgressFrom("hand-2", "", protocol.RPCProgress{
		RunID: "progress-run", Seq: 4, Kind: protocol.ProgressStdout, Data: "wrong",
	}); err == nil {
		t.Fatal("wrong source progress accepted")
	}
}

func TestRegistryAllowsMonotonicProgressAfterAcceptedCancelRequest(t *testing.T) {
	registry := NewRegistry()
	createSentRun(t, registry, "cancel-progress-run", "hand-1")
	if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{
		RunID: "cancel-progress-run", StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	if requested, err := registry.RequestCancel("cancel-progress-run", "user"); err != nil || !requested {
		t.Fatalf("request cancel = %v, %v", requested, err)
	}
	admission, err := registry.ApplyProgressFrom("hand-1", "", protocol.RPCProgress{
		RunID: "cancel-progress-run", Seq: 1, Kind: protocol.ProgressStdout, Data: "in-flight",
	})
	if err != nil || !admission.Accepted || admission.Gap {
		t.Fatalf("cancel-requested progress = %+v, %v", admission, err)
	}
}

func TestRegistryProgressResultCancelRaceHasOneTerminal(t *testing.T) {
	registry := NewRegistry()
	createSentRun(t, registry, "race-run", "hand-1")
	if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{
		RunID: "race-run", StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 1; i <= 100; i++ {
		wg.Add(1)
		go func(seq int64) {
			defer wg.Done()
			_, _ = registry.ApplyProgressFrom("hand-1", "", protocol.RPCProgress{
				RunID: "race-run", Seq: seq, Kind: protocol.ProgressStdout, Data: "x",
			})
		}(int64(i))
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = registry.ApplyResult("hand-1", protocol.RPCResult{RunID: "race-run", Success: true})
	}()
	go func() {
		defer wg.Done()
		_, _ = registry.RequestCancel("race-run", "user")
		_ = registry.ApplyCancelResult("hand-1", protocol.RPCCancelResult{RunID: "race-run", Status: protocol.CancelCancelled})
	}()
	wg.Wait()
	run, _ := registry.Snapshot("race-run")
	if !protocol.IsTerminalRunStatus(run.Status) {
		t.Fatalf("race did not reach terminal state: %+v", run)
	}
	admission, err := registry.ApplyProgressFrom("hand-1", "", protocol.RPCProgress{
		RunID: "race-run", Seq: 1000, Kind: protocol.ProgressStdout, Data: "late",
	})
	if err != nil || admission.Accepted {
		t.Fatalf("late progress = %+v, %v", admission, err)
	}
}

func TestRegistryProgressEnforcesIndependentCaps(t *testing.T) {
	registry := NewRegistry()
	createSentRun(t, registry, "cap-run", "hand-1")
	if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{
		RunID: "cap-run", StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	chunk := strings.Repeat("x", protocol.MaxRPCProgressChunkBytes)
	for seq := int64(1); seq <= protocol.MaxRPCProgressEvents; seq++ {
		admission, err := registry.ApplyProgressFrom("hand-1", "", protocol.RPCProgress{
			RunID: "cap-run", Seq: seq, Kind: protocol.ProgressStdout, Data: chunk,
		})
		if err != nil || !admission.Accepted {
			t.Fatalf("progress %d = %+v, %v", seq, admission, err)
		}
	}
	over, err := registry.ApplyProgressFrom("hand-1", "", protocol.RPCProgress{
		RunID: "cap-run", Seq: protocol.MaxRPCProgressEvents + 1, Kind: protocol.ProgressStdout, Data: "x",
	})
	if err != nil || over.Accepted {
		t.Fatalf("over-cap progress = %+v, %v", over, err)
	}
	run, _ := registry.Snapshot("cap-run")
	if run.ProgressEvents != protocol.MaxRPCProgressEvents || run.ProgressBytes != protocol.MaxRPCProgressBytes {
		t.Fatalf("progress counters = events %d, bytes %d", run.ProgressEvents, run.ProgressBytes)
	}
}
