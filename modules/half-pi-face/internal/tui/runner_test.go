package tui

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

type fakeConnection struct {
	ready     protocol.Envelope
	reads     chan incoming
	sent      chan protocol.Envelope
	closed    chan struct{}
	closeOnce sync.Once
}

func newFakeConnection(t *testing.T) *fakeConnection {
	t.Helper()
	ready := testEnvelope(t, protocol.TypeRegistered, protocol.Registered{
		ProtocolVersion: protocol.ProtocolVersion, ClientID: "face-1", ServerID: "mind", SessionID: "session-1",
	})
	ready.SessionID, ready.From, ready.To, ready.Seq = "session-1", "mind", "face-1", 1
	return &fakeConnection{
		ready: ready, reads: make(chan incoming, 8), sent: make(chan protocol.Envelope, 32),
		closed: make(chan struct{}),
	}
}

func (c *fakeConnection) RegisteredEnvelope() protocol.Envelope { return c.ready }

func (c *fakeConnection) Send(env protocol.Envelope) error {
	c.sent <- env
	return nil
}

func (c *fakeConnection) Read() (protocol.Envelope, error) {
	select {
	case result := <-c.reads:
		return result.env, result.err
	case <-c.closed:
		return protocol.Envelope{}, io.EOF
	}
}

func (c *fakeConnection) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func newTestTerminal(t *testing.T) (*terminal, *fakeConnection, *bytes.Buffer) {
	t.Helper()
	conn := newFakeConnection(t)
	var output bytes.Buffer
	return &terminal{
		conn: conn, output: &output,
		lastChat: make(map[string]string), pending: make(map[string]pendingRequest),
	}, conn, &output
}

func testEnvelope(t *testing.T, typ string, payload any) protocol.Envelope {
	t.Helper()
	env, err := protocol.NewEnvelope("", typ, payload)
	if err != nil {
		t.Fatal(err)
	}
	return *env
}

func nextSent(t *testing.T, conn *fakeConnection) protocol.Envelope {
	t.Helper()
	select {
	case env := <-conn.sent:
		return env
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Face command")
		return protocol.Envelope{}
	}
}

func waitRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("terminal Face did not stop")
		return nil
	}
}

func TestRunListsConversationsAndStopsAtEOF(t *testing.T) {
	conn := newFakeConnection(t)
	var output bytes.Buffer
	if err := Run(context.Background(), conn, bytes.NewReader(nil), &output); err != nil {
		t.Fatal(err)
	}
	command := nextSent(t, conn)
	if command.Type != protocol.TypeFaceConversationList || protocol.ValidateFacePayload(command.Type, command.Payload) != nil {
		t.Fatalf("initial command = %+v", command)
	}
	if !strings.Contains(output.String(), "Half-Pi Face connected as face-1") {
		t.Fatalf("output = %q", output.String())
	}
	select {
	case <-conn.closed:
	default:
		t.Fatal("connection was not closed")
	}
}

func TestRunStopsOnCancellation(t *testing.T) {
	conn := newFakeConnection(t)
	inputReader, inputWriter := io.Pipe()
	defer inputWriter.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, conn, inputReader, io.Discard) }()
	_ = nextSent(t, conn)
	cancel()
	if err := waitRun(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsInvalidMindMessage(t *testing.T) {
	conn := newFakeConnection(t)
	inputReader, inputWriter := io.Pipe()
	defer inputWriter.Close()
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), conn, inputReader, io.Discard) }()
	_ = nextSent(t, conn)
	conn.reads <- incoming{env: testEnvelope(t, protocol.TypeRPC, map[string]string{"id": "bad"})}
	err := waitRun(t, done)
	if err == nil || !strings.Contains(err.Error(), "invalid Face message type") {
		t.Fatalf("Run error = %v", err)
	}
}

func TestRunRejectsInvalidRegisteredMessage(t *testing.T) {
	conn := newFakeConnection(t)
	conn.ready = testEnvelope(t, protocol.TypeRPC, map[string]string{"id": "bad"})
	if err := Run(context.Background(), conn, bytes.NewReader(nil), io.Discard); err == nil {
		t.Fatal("Run accepted an invalid ready message")
	}
}
