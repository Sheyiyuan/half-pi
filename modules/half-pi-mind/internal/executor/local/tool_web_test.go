package local

import (
	"encoding/json"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

func TestWebToolsAreRegistered(t *testing.T) {
	for _, name := range []string{"web_search", "web_fetch"} {
		tool, ok := executor.FindTool(name)
		if !ok {
			t.Fatalf("tool %q is not registered", name)
		}
		if tool.Execute == nil || tool.Parameters == nil || tool.DefaultConfirm {
			t.Fatalf("tool %q has invalid definition: %+v", name, tool)
		}
	}
}

func TestWebFetchCheckRejectsPrivateTarget(t *testing.T) {
	decision, reason := webFetchCheck(json.RawMessage(`{"url":"http://169.254.169.254/latest/meta-data"}`))
	if decision != executor.DecisionDeny || reason == "" {
		t.Fatalf("decision = %v, reason = %q", decision, reason)
	}
}
