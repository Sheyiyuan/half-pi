package tui

import (
	"encoding/json"
	"io"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

type fakeConnection struct {
	ready protocol.Envelope
	sent  []protocol.Envelope
}

func newFakeConnection(t *testing.T) *fakeConnection {
	t.Helper()
	ready, err := protocol.NewEnvelope("registered", protocol.TypeRegistered, protocol.Registered{
		ProtocolVersion: protocol.ProtocolVersion, ClientID: "face-1", ServerID: "mind", SessionID: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fakeConnection{ready: *ready}
}

func (c *fakeConnection) RegisteredEnvelope() protocol.Envelope { return c.ready }
func (c *fakeConnection) Send(env protocol.Envelope) error {
	c.sent = append(c.sent, env)
	return nil
}
func (c *fakeConnection) Read() (protocol.Envelope, error) { return protocol.Envelope{}, io.EOF }
func (c *fakeConnection) Close() error                     { return nil }

func readyModel(t *testing.T) (*Model, *fakeConnection) {
	t.Helper()
	connection := newFakeConnection(t)
	model := NewModel(nil)
	model.conn = connection
	model.state = stateReady
	model.capabilitiesKnown = true
	for _, scope := range []protocol.FaceScope{
		protocol.FaceScopeChat, protocol.FaceScopeSessionsRead, protocol.FaceScopeSessionsWrite,
		protocol.FaceScopeApprove, protocol.FaceScopeRunsRead, protocol.FaceScopeRunsCancel,
		protocol.FaceScopeRunsOutput, protocol.FaceScopeHandsRead, protocol.FaceScopeTasksRead,
		protocol.FaceScopeTasksCancel,
	} {
		model.scopes[scope] = struct{}{}
	}
	for _, feature := range []protocol.FaceFeature{
		protocol.FaceFeatureChatStream, protocol.FaceFeatureChatStreamResume,
		protocol.FaceFeatureRunProgress, protocol.FaceFeatureMessagePaging,
	} {
		model.features[feature] = struct{}{}
	}
	model.resize(120, 30)
	return model, connection
}

func sequenceIDSource(t *testing.T, ids ...string) func() (string, error) {
	t.Helper()
	index := 0
	return func() (string, error) {
		if index >= len(ids) {
			t.Fatalf("request ID sequence exhausted")
		}
		value := ids[index]
		index++
		return value, nil
	}
}

func runCommand(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	runMessage(t, cmd())
}

func runMessage(t *testing.T, message tea.Msg) {
	t.Helper()
	switch value := message.(type) {
	case tea.BatchMsg:
		for _, cmd := range value {
			if cmd != nil {
				runMessage(t, cmd())
			}
		}
	}
}

func resultData(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validSummary(id string) protocol.ConversationSummary {
	return protocol.ConversationSummary{
		ConversationID: id, Name: "Untitled", Mode: "normal", UpdatedAt: time.Now().UTC(),
	}
}

func validSnapshot(id string) protocol.ConversationSnapshot {
	return protocol.ConversationSnapshot{
		ConversationID: id, Name: "Untitled", Mode: "normal",
		Messages: []protocol.FaceMessage{}, PendingChats: []protocol.ChatSummary{},
		PendingApprovals: []protocol.ApprovalSummary{}, ActiveRuns: []protocol.RemoteRunSummary{},
		Tasks: []protocol.TaskSummary{}, TaskHistoryLimit: protocol.DefaultFaceTaskHistoryLimit,
		SnapshotVersion: 1,
	}
}
