package remoteexec

import (
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestAuthorityRoutesLifecycleWithoutCore(t *testing.T) {
	registry := NewRegistry()
	wsHub := hub.New()
	authority := NewAuthority(wsHub, registry, nil, func(string) (string, error) { return "test", nil })
	createSentRun(t, registry, "run-1", "hand-1")
	done, _ := registry.Done("run-1")
	peer := &hub.Peer{ID: "hand-1", Type: hub.PeerHand}
	accepted, _ := protocol.NewEnvelope("", protocol.TypeRPCAccepted, protocol.RPCAccepted{RunID: "run-1", StartedAt: time.Now().UnixMilli()})
	authority.handleMessage(peer, *accepted)
	result, _ := protocol.NewEnvelope("", protocol.TypeRPCResult, protocol.RPCResult{RunID: "run-1", Success: true})
	authority.handleMessage(peer, *result)
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

func TestAuthorityDisconnectMarksRunsLost(t *testing.T) {
	registry := NewRegistry()
	wsHub := hub.New()
	authority := NewAuthority(wsHub, registry, nil, func(string) (string, error) { return "test", nil })
	createSentRun(t, registry, "run-1", "hand-1")
	authority.handleDisconnect(&hub.Peer{ID: "hand-1", Type: hub.PeerHand})
	run, _ := registry.Snapshot("run-1")
	if run.Status != protocol.RunLost {
		t.Fatalf("status = %s, want lost", run.Status)
	}
}

func TestAuthorityRoutesPendingHandInfo(t *testing.T) {
	authority := NewAuthority(hub.New(), NewRegistry(), nil, func(string) (string, error) { return "test", nil })
	ch, cancel := authority.PendingCall("info-1", 0, "hand-1")
	defer cancel()
	peer := &hub.Peer{ID: "hand-1", Type: hub.PeerHand}
	response, _ := protocol.NewEnvelope("", protocol.TypeHandInfoResp, protocol.HandInfoResp{ID: "info-1"})
	authority.handleMessage(peer, *response)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("pending Hand info was not delivered")
	}
}
