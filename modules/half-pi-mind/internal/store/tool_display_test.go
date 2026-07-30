package store

import (
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestToolDisplayProjectionPersistsAdmissionAndReliableTerminal(t *testing.T) {
	s := newTestStore(t)
	group, err := s.UpsertGroup(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(group.ID, "conversation"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	args := &protocol.ToolArgsView{
		ProjectionVersion: "tool-display.v1", Fields: map[string]protocol.ToolFieldView{
			"command": {State: protocol.ToolDisplayShow, Value: "pwd"},
		}, Warnings: []string{"possible_secret:command"},
	}
	if err := s.CreateToolDisplayProjection(ToolDisplayProjection{
		ConversationID: "conversation", RequestID: "chat", Ordinal: 1, Tool: "exec_command",
		DetailMode: protocol.FaceDetailModeTransparent, ArgsDigest: "sha256:args", Args: args,
		ProjectionVersion: args.ProjectionVersion, ScanWarnings: append([]string(nil), args.Warnings...), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	output := &protocol.ToolOutputView{Stdout: "ok", StdoutBytes: 2, OutputBytes: 2, Digest: "sha256:result", Warnings: []string{}}
	completedAt := now.Add(time.Second)
	if err := s.CompleteToolDisplayProjection("conversation", "chat", 1, output, true, "tool-display.v1", []string{},
		2, "sha256:result", false, "", completedAt); err != nil {
		t.Fatal(err)
	}
	records, err := s.ListToolDisplayProjections("conversation")
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %+v, %v", records, err)
	}
	got := records[0]
	if !got.Complete || !got.Success || got.Args == nil || got.Result == nil || got.Result.Stdout != "ok" ||
		got.DetailMode != protocol.FaceDetailModeTransparent || !got.CreatedAt.Equal(now) || !got.CompletedAt.Equal(completedAt) ||
		len(got.ScanWarnings) != 1 || got.ScanWarnings[0] != "possible_secret:command" {
		t.Fatalf("projection = %+v", got)
	}
}

func TestToolDisplayProjectionRejectsRawSummaryViews(t *testing.T) {
	s := newTestStore(t)
	group, err := s.UpsertGroup(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(group.ID, "conversation"); err != nil {
		t.Fatal(err)
	}
	args := &protocol.ToolArgsView{ProjectionVersion: protocol.FaceToolDisplayProjectionVersion,
		Fields: map[string]protocol.ToolFieldView{}, Warnings: []string{}}
	err = s.CreateToolDisplayProjection(ToolDisplayProjection{
		ConversationID: "conversation", RequestID: "chat", Ordinal: 1, Tool: "read_file",
		DetailMode: protocol.FaceDetailModeSummary, ArgsDigest: "sha256:args", Args: args,
		ProjectionVersion: args.ProjectionVersion, CreatedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("summary projection accepted raw arguments")
	}
}

func TestSummaryToolDisplayProjectionNeverStoresViews(t *testing.T) {
	s := newTestStore(t)
	group, err := s.UpsertGroup(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(group.ID, "conversation"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.CreateToolDisplayProjection(ToolDisplayProjection{
		ConversationID: "conversation", RequestID: "chat", Ordinal: 1, Tool: "read_file",
		DetailMode: protocol.FaceDetailModeSummary, ArgsDigest: "sha256:args", ScanWarnings: []string{}, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteToolDisplayProjection("conversation", "chat", 1, nil, true, "", []string{},
		0, "sha256:empty", false, "", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	records, err := s.ListToolDisplayProjections("conversation")
	if err != nil || len(records) != 1 || records[0].Args != nil || records[0].Result != nil {
		t.Fatalf("summary records = %+v, %v", records, err)
	}
}
