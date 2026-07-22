package local

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	coreexec "github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
)

func TestLocalExecutorsIsolateRemoteBridges(t *testing.T) {
	first := New(&RemoteBridge{ActiveHand: func() string { return "hand-a" }})
	second := New(&RemoteBridge{ActiveHand: func() string { return "hand-b" }})
	args := json.RawMessage(`{}`)
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for _, exec := range []*LocalExecutor{first, second} {
		wg.Add(1)
		go func(exec *LocalExecutor) {
			defer wg.Done()
			runtime := coreexec.NewToolRuntime(allowToolAuthorizer{}, corelifecycle.NewRegistry())
			ctx := exec.PrepareToolContext(context.Background())
			result := runtime.Execute(ctx, coreexec.Invocation{Tool: "select_hand", Args: args})
			results <- result.Output
		}(exec)
	}
	wg.Wait()
	close(results)
	seen := map[string]bool{}
	for result := range results {
		if strings.Contains(result, "hand-a") {
			seen["hand-a"] = true
		}
		if strings.Contains(result, "hand-b") {
			seen["hand-b"] = true
		}
	}
	if !seen["hand-a"] || !seen["hand-b"] {
		t.Fatalf("bridges were not isolated: %+v", seen)
	}
}

func TestViewSkillEnforcesLifecycleGroup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "private.skill.md")
	if err := os.WriteFile(path, []byte("---\nname: private\ndescription: private\ngroups: [group-a]\n---\nsecret instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := skill.LoadFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	SetSkillStore(store)
	t.Cleanup(func() { SetSkillStore(nil) })
	runtime := coreexec.NewToolRuntime(allowToolAuthorizer{}, corelifecycle.NewRegistry())
	for _, test := range []struct {
		group   string
		allowed bool
	}{
		{group: "group-a", allowed: true},
		{group: "group-b", allowed: false},
		{group: "", allowed: false},
	} {
		meta := corelifecycle.NewMeta(corelifecycle.SourceMind).WithGroup(test.group)
		result := runtime.Execute(context.Background(), coreexec.Invocation{
			Meta: meta, Tool: "view_skill", Args: json.RawMessage(`{"name":"private"}`),
		})
		if (result.ExecutionOutcome == coreexec.ExecutionSucceeded) != test.allowed {
			t.Fatalf("group %q outcome = %s, output=%q", test.group, result.ExecutionOutcome, result.Output)
		}
	}
}

type allowToolAuthorizer struct{}

func (allowToolAuthorizer) Authorize(context.Context, coreexec.FrozenInvocation) coreexec.Authorization {
	return coreexec.Authorization{Allowed: true, Decision: "allow", ReasonCode: "test_allow"}
}
