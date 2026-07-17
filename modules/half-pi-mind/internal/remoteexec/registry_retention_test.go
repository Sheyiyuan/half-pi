package remoteexec

import (
	"fmt"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

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
