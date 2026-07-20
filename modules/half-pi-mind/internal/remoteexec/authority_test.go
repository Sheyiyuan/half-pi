package remoteexec

import (
	"sync"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
)

type authorityEventWriter struct {
	mu     sync.Mutex
	events []events.Event
	block  chan struct{}
	start  chan struct{}
}

func (w *authorityEventWriter) WriteEvent(event events.Event) error {
	if event.Type == events.TypeToolProgress && w.block != nil {
		select {
		case w.start <- struct{}{}:
		default:
		}
		<-w.block
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, event)
	return nil
}

func (w *authorityEventWriter) Close() error { return nil }

func TestAuthorityRoutesLifecycleWithoutCore(t *testing.T) {
	registry := NewRegistry()
	wsHub := hub.New()
	authority := NewAuthority(wsHub, registry, nil)
	createSentRun(t, registry, "run-1", "hand-1")
	done, _ := registry.Done("run-1")
	peer := &hub.Peer{ID: "hand-1", Type: hub.PeerHand}
	accepted, _ := protocol.NewEnvelope("", protocol.TypeRPCAccepted, protocol.RPCAccepted{RunID: "run-1", StartedAt: time.Now().UnixMilli()})
	authority.HandleHandMessage(peer, *accepted)
	result, _ := protocol.NewEnvelope("", protocol.TypeRPCResult, protocol.RPCResult{RunID: "run-1", Success: true})
	authority.HandleHandMessage(peer, *result)
	select {
	case <-done:
	default:
		t.Fatal("Authority did not route terminal result")
	}
	run, _ := registry.Snapshot("run-1")
	if run.Status != protocol.RunSucceeded {
		t.Fatalf("status = %s", run.Status)
	}
}

func TestAuthorityFailClosesTerminalAuditFailure(t *testing.T) {
	auditor := &recordingAuditor{}
	registry := NewRegistry(auditor)
	authority := NewAuthority(hub.New(), registry, nil)
	createSentRun(t, registry, "run-audit-failure", "hand-1")
	if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{
		RunID: "run-audit-failure", StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	done, _ := registry.Done("run-audit-failure")
	auditor.mu.Lock()
	auditor.failTransitions = true
	auditor.mu.Unlock()
	result, _ := protocol.NewEnvelope("", protocol.TypeRPCResult, protocol.RPCResult{
		RunID: "run-audit-failure", Success: true,
	})
	authority.HandleHandMessage(&hub.Peer{ID: "hand-1", Type: hub.PeerHand}, *result)
	select {
	case <-done:
	default:
		t.Fatal("terminal audit failure left run open")
	}
	run, _ := registry.Snapshot("run-audit-failure")
	if run.Status != protocol.RunRejected {
		t.Fatalf("status = %s, want rejected", run.Status)
	}
}

func TestAuthorityDisconnectMarksRunsLost(t *testing.T) {
	registry := NewRegistry()
	wsHub := hub.New()
	authority := NewAuthority(wsHub, registry, nil)
	createSentRun(t, registry, "run-1", "hand-1")
	authority.HandleHandDisconnect(&hub.Peer{ID: "hand-1", Type: hub.PeerHand})
	run, _ := registry.Snapshot("run-1")
	if run.Status != protocol.RunLost {
		t.Fatalf("status = %s, want lost", run.Status)
	}
}

func TestAuthorityIgnoresFaceDisconnect(t *testing.T) {
	registry := NewRegistry()
	authority := NewAuthority(hub.New(), registry, nil)
	createSentRun(t, registry, "run-1", "same-label")
	authority.HandleHandDisconnect(&hub.Peer{ID: "same-label", Type: hub.PeerFace})
	run, _ := registry.Snapshot("run-1")
	if run.Status != protocol.RunSent {
		t.Fatalf("status = %s, want sent", run.Status)
	}
}

func TestAuthorityRoutesPendingHandInfo(t *testing.T) {
	authority := NewAuthority(hub.New(), NewRegistry(), nil)
	ch, cancel := authority.PendingCall("info-1", 0, "hand-1")
	defer cancel()
	peer := &hub.Peer{ID: "hand-1", Type: hub.PeerHand}
	response, _ := protocol.NewEnvelope("", protocol.TypeHandInfoResp, protocol.HandInfoResp{ID: "info-1"})
	authority.HandleHandMessage(peer, *response)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("pending Hand info was not delivered")
	}
}

func TestAuthorityRoutesTaskErrorsOnlyFromExpectedHand(t *testing.T) {
	authority := NewAuthority(hub.New(), NewRegistry(), nil)
	ch, cancel := authority.PendingCall("request-1", 0, "hand-1")
	defer cancel()
	errEnv, _ := protocol.NewEnvelope("", protocol.TypeError, protocol.ErrorMsg{
		MsgID: "request-1", Code: "unknown_task", Message: "missing",
	})
	authority.HandleHandMessage(&hub.Peer{ID: "hand-2", Type: hub.PeerHand}, *errEnv)
	select {
	case <-ch:
		t.Fatal("wrong Hand response was delivered")
	default:
	}
	authority.HandleHandMessage(&hub.Peer{ID: "hand-1", Type: hub.PeerHand}, *errEnv)
	select {
	case got := <-ch:
		if got.Type != protocol.TypeError {
			t.Fatalf("type = %s", got.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Hand error was not delivered")
	}
}

func TestAuthorityPendingPeerCallRejectsWrongConnection(t *testing.T) {
	authority := NewAuthority(hub.New(), NewRegistry(), nil)
	expected := &hub.Peer{ID: "hand-1", Type: hub.PeerHand}
	ch, cancel := authority.PendingPeerCall("request-1", 0, expected)
	defer cancel()
	env, _ := protocol.NewEnvelope("", protocol.TypeTaskCancelResult, protocol.TaskCancelResult{
		ID: "request-1", TaskID: "task-1", Status: protocol.TaskCancelCancelled,
	})
	authority.HandleHandMessage(&hub.Peer{ID: "hand-1", Type: hub.PeerHand}, *env)
	select {
	case <-ch:
		t.Fatal("response from a different peer connection was delivered")
	default:
	}
}

func TestAuthorityDeliversMalformedTaskResponseByEnvelopeID(t *testing.T) {
	authority := NewAuthority(hub.New(), NewRegistry(), nil)
	peer := &hub.Peer{ID: "hand-1", Type: hub.PeerHand}
	ch, cancel := authority.PendingPeerCall("request-1", 0, peer)
	defer cancel()
	authority.HandleHandMessage(peer, protocol.Envelope{
		MsgID: "request-1", Type: protocol.TypeTaskStatusResp, Payload: []byte(`{"id":`),
	})
	select {
	case got := <-ch:
		if got.MsgID != "request-1" {
			t.Fatalf("message ID = %q", got.MsgID)
		}
	case <-time.After(time.Second):
		t.Fatal("malformed task response did not wake pending caller")
	}
}

func TestAuthorityPublishesOnlyAcceptedProgress(t *testing.T) {
	registry := NewRegistry()
	bus := events.NewEventBus()
	writer := &authorityEventWriter{}
	bus.Subscribe(writer)
	authority := NewAuthority(hub.New(), registry, bus)
	var observations []ProgressObservation
	authority.OnProgress(func(observation ProgressObservation) {
		observations = append(observations, observation)
	})
	createSentRun(t, registry, "progress-run", "hand-1")
	peer := &hub.Peer{ID: "hand-1", Type: hub.PeerHand}
	early, _ := protocol.NewEnvelope("", protocol.TypeRPCProgress, protocol.RPCProgress{
		RunID: "progress-run", Seq: 1, Kind: protocol.ProgressStdout, Data: "early",
	})
	authority.HandleHandMessage(peer, *early)
	accepted, _ := protocol.NewEnvelope("", protocol.TypeRPCAccepted, protocol.RPCAccepted{
		RunID: "progress-run", StartedAt: time.Now().UnixMilli(),
	})
	authority.HandleHandMessage(peer, *accepted)
	for _, seq := range []int64{1, 1, 3} {
		env, _ := protocol.NewEnvelope("", protocol.TypeRPCProgress, protocol.RPCProgress{
			RunID: "progress-run", Seq: seq, Kind: protocol.ProgressStdout, Data: "data",
		})
		authority.HandleHandMessage(peer, *env)
	}
	result, _ := protocol.NewEnvelope("", protocol.TypeRPCResult, protocol.RPCResult{RunID: "progress-run", Success: true})
	authority.HandleHandMessage(peer, *result)
	late, _ := protocol.NewEnvelope("", protocol.TypeRPCProgress, protocol.RPCProgress{
		RunID: "progress-run", Seq: 4, Kind: protocol.ProgressStdout, Data: "late",
	})
	authority.HandleHandMessage(peer, *late)
	bus.Close()
	writer.mu.Lock()
	defer writer.mu.Unlock()
	var progress []events.Event
	for _, event := range writer.events {
		if event.Type == events.TypeToolProgress {
			progress = append(progress, event)
		}
	}
	if len(progress) != 2 {
		t.Fatalf("published progress count = %d, events=%+v", len(progress), writer.events)
	}
	var gapped ToolProgressEventData
	for _, event := range progress {
		data, ok := event.Data.(ToolProgressEventData)
		if ok && data.Seq == 3 {
			gapped = data
		}
	}
	if gapped.Seq != 3 || !gapped.Gap || gapped.Data != "data" {
		t.Fatalf("gapped event data = %+v", gapped)
	}
	if len(observations) != 2 || observations[0].Progress.Seq != 1 || observations[0].Gap ||
		observations[1].Progress.Seq != 3 || !observations[1].Gap || observations[1].Run.ID != "progress-run" {
		t.Fatalf("progress observations = %+v", observations)
	}
}

func TestAuthorityPublishesProgressBeforeResultDeterministically(t *testing.T) {
	registry := NewRegistry()
	bus := events.NewEventBus()
	writer := &authorityEventWriter{block: make(chan struct{}), start: make(chan struct{}, 1)}
	bus.Subscribe(writer)
	authority := NewAuthority(hub.New(), registry, bus)
	createSentRun(t, registry, "ordered-run", "hand-1")
	if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{
		RunID: "ordered-run", StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	peer := &hub.Peer{ID: "hand-1", Type: hub.PeerHand}
	progress, _ := protocol.NewEnvelope("", protocol.TypeRPCProgress, protocol.RPCProgress{
		RunID: "ordered-run", Seq: 1, Kind: protocol.ProgressStdout, Data: "first",
	})
	progressDone := make(chan struct{})
	go func() {
		authority.HandleHandMessage(peer, *progress)
		close(progressDone)
	}()
	<-writer.start
	result, _ := protocol.NewEnvelope("", protocol.TypeRPCResult, protocol.RPCResult{RunID: "ordered-run", Success: true})
	resultDone := make(chan struct{})
	go func() {
		authority.HandleHandMessage(peer, *result)
		close(resultDone)
	}()
	select {
	case <-resultDone:
		t.Fatal("result publication passed blocked progress publication")
	case <-time.After(20 * time.Millisecond):
	}
	close(writer.block)
	<-progressDone
	<-resultDone
	bus.Close()
	writer.mu.Lock()
	defer writer.mu.Unlock()
	var observed []string
	for _, event := range writer.events {
		if event.Type == events.TypeToolProgress || event.Type == events.TypeToolResult {
			observed = append(observed, event.Type)
		}
	}
	if len(observed) != 2 || observed[0] != events.TypeToolProgress || observed[1] != events.TypeToolResult {
		t.Fatalf("publication order = %v", observed)
	}
}

func TestAuthoritySerializesResultAdmissionBehindProgressPublication(t *testing.T) {
	registry := NewRegistry()
	bus := events.NewEventBus()
	writer := &authorityEventWriter{block: make(chan struct{}), start: make(chan struct{}, 1)}
	bus.Subscribe(writer)
	authority := NewAuthority(hub.New(), registry, bus)
	createSentRun(t, registry, "serialized-run", "hand-1")
	if err := registry.ApplyAccepted("hand-1", protocol.RPCAccepted{
		RunID: "serialized-run", StartedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	peer := &hub.Peer{ID: "hand-1", Type: hub.PeerHand}
	progress, _ := protocol.NewEnvelope("", protocol.TypeRPCProgress, protocol.RPCProgress{
		RunID: "serialized-run", Seq: 1, Kind: protocol.ProgressStdout, Data: "first",
	})
	go authority.HandleHandMessage(peer, *progress)
	<-writer.start
	result, _ := protocol.NewEnvelope("", protocol.TypeRPCResult, protocol.RPCResult{RunID: "serialized-run", Success: true})
	resultDone := make(chan struct{})
	go func() {
		authority.HandleHandMessage(peer, *result)
		close(resultDone)
	}()
	time.Sleep(20 * time.Millisecond)
	run, _ := registry.Snapshot("serialized-run")
	if run.Status != protocol.RunRunning {
		t.Fatalf("result admitted before progress publication completed: %s", run.Status)
	}
	close(writer.block)
	<-resultDone
	bus.Close()
}
