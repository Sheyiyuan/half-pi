package local

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func TestGetHandTaskEnforcesCurrentSession(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	authority := remoteexec.NewAuthority(hub.New(), remoteexec.NewRegistry(), nil)
	service := remoteexec.NewTaskService(authority, db)
	if err := service.CreateStartSnapshot(remoteexec.Task{
		TaskID: "0123456789abcdef", SessionID: "session-a", HandID: "hand-a", Tool: "tool", ArgsDigest: "digest",
	}); err != nil {
		t.Fatal(err)
	}
	bridge := &RemoteBridge{Tasks: service, SessionID: func() string { return "session-b" }}
	tool, ok := executor.FindTool("get_hand_task")
	if !ok {
		t.Fatal("get_hand_task not registered")
	}
	args, _ := json.Marshal(map[string]any{"task_id": "0123456789abcdef"})
	result := tool.Execute(WithRemoteBridge(context.Background(), bridge), args)
	if result.Success || result.Error != remoteexec.ErrTaskNotOwned.Error() {
		t.Fatalf("result = %+v", result)
	}
}

func TestTaskToolResultRedactsArgsDigest(t *testing.T) {
	result := taskToolResult(remoteexec.Task{TaskID: "task-1", ArgsDigest: "secret-digest"})
	if strings.Contains(result.Output, "secret-digest") {
		t.Fatalf("output leaked digest: %s", result.Output)
	}
	data, ok := result.Data.(remoteexec.Task)
	if !ok || data.ArgsDigest != "" {
		t.Fatalf("tool data leaked digest: %+v", result.Data)
	}
}

func TestBackgroundUseHandForcesConfirmation(t *testing.T) {
	if !remoteNeedsConfirmation(false, true, true) {
		t.Fatal("known local tool in background mode did not force confirmation")
	}
	if remoteNeedsConfirmation(false, false, true) {
		t.Fatal("known foreground tool unexpectedly forced confirmation")
	}
}
