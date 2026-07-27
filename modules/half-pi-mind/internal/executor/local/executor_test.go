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
	exec := New(nil)
	exec.SetSkills(store)
	ctx := exec.PrepareToolContext(context.Background())
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
		result := runtime.Execute(ctx, coreexec.Invocation{
			Meta: meta, Tool: "view_skill", Args: json.RawMessage(`{"name":"private"}`),
		})
		if (result.ExecutionOutcome == coreexec.ExecutionSucceeded) != test.allowed {
			t.Fatalf("group %q outcome = %s, output=%q", test.group, result.ExecutionOutcome, result.Output)
		}
	}
}

func TestSkillToolsRequireStoreInContext(t *testing.T) {
	runtime := coreexec.NewToolRuntime(allowToolAuthorizer{}, corelifecycle.NewRegistry())
	for _, test := range []struct {
		tool string
		args string
	}{
		{tool: "view_skill", args: `{"name":"anything"}`},
		{tool: "list_skills", args: `{}`},
	} {
		result := runtime.Execute(context.Background(), coreexec.Invocation{
			Meta: corelifecycle.NewMeta(corelifecycle.SourceMind), Tool: test.tool, Args: json.RawMessage(test.args),
		})
		if result.ExecutionOutcome == coreexec.ExecutionSucceeded {
			t.Fatalf("%s succeeded without a skill store in context", test.tool)
		}
		if !strings.Contains(result.Output, "not initialized") {
			t.Fatalf("%s error = %q, want an explicit uninitialized error", test.tool, result.Output)
		}
	}
}

func TestListSkillsEnforcesLifecycleGroup(t *testing.T) {
	dir := t.TempDir()
	writeToolSkillFile(t, dir, "global.skill.md", "---\nname: global\ndescription: Shared\ntags: [shared]\n---\nglobal body")
	writeToolSkillFile(t, dir, "private.skill.md", "---\nname: private\ndescription: Group A only\ngroups: [group-a]\n---\nsecret body")
	writeToolSkillFile(t, dir, "conventions.skill.md", "---\nname: conventions\ndescription: Project conventions\nalways: true\n---\nconventions body")

	store, err := skill.LoadFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	exec := New(nil)
	exec.SetSkills(store)
	ctx := exec.PrepareToolContext(context.Background())
	runtime := coreexec.NewToolRuntime(allowToolAuthorizer{}, corelifecycle.NewRegistry())

	for _, test := range []struct {
		group  string
		leaked bool
	}{
		{group: "group-a", leaked: true},
		{group: "group-b", leaked: false},
		{group: "", leaked: false},
	} {
		meta := corelifecycle.NewMeta(corelifecycle.SourceMind).WithGroup(test.group)
		result := runtime.Execute(ctx, coreexec.Invocation{
			Meta: meta, Tool: "list_skills", Args: json.RawMessage(`{}`),
		})
		if result.ExecutionOutcome != coreexec.ExecutionSucceeded {
			t.Fatalf("group %q outcome = %s", test.group, result.ExecutionOutcome)
		}
		if strings.Contains(result.Output, "private") != test.leaked {
			t.Fatalf("group %q output = %q", test.group, result.Output)
		}
		if strings.Contains(result.Output, "secret body") || strings.Contains(result.Output, "global body") {
			t.Fatalf("list_skills leaked skill content: %q", result.Output)
		}
		// always 技能优先排列，供模型优先识别项目级约定。
		if !strings.HasPrefix(result.Output, "conventions") {
			t.Fatalf("group %q output did not lead with the always skill: %q", test.group, result.Output)
		}
	}
}

func writeToolSkillFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

type allowToolAuthorizer struct{}

func (allowToolAuthorizer) Authorize(context.Context, coreexec.FrozenInvocation) coreexec.Authorization {
	return coreexec.Authorization{Allowed: true, Decision: "allow", ReasonCode: "test_allow"}
}
