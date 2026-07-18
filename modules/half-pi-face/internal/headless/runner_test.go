package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	ready, err := protocol.NewEnvelope("registered-1", protocol.TypeRegistered, protocol.Registered{
		ProtocolVersion: protocol.ProtocolVersion, ClientID: "face-1", ServerID: "mind",
		SessionID: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ready.SessionID, ready.From, ready.To, ready.Seq = "session-1", "mind", "face-1", 1
	return &fakeConnection{
		ready: *ready, reads: make(chan incoming, 4), sent: make(chan protocol.Envelope, 4),
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

type lineWriter struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	lines  chan []byte
}

func newLineWriter() *lineWriter { return &lineWriter{lines: make(chan []byte, 8)} }

func (w *lineWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written, _ := w.buffer.Write(data)
	for {
		line, err := w.buffer.ReadBytes('\n')
		if err != nil {
			if len(line) > 0 {
				_, _ = w.buffer.Write(line)
			}
			break
		}
		w.lines <- append([]byte(nil), line...)
	}
	return written, nil
}

func nextOutput(t *testing.T, output *lineWriter) protocol.Envelope {
	t.Helper()
	select {
	case line := <-output.lines:
		env, err := protocol.StrictDecode[protocol.Envelope](line)
		if err != nil {
			t.Fatal(err)
		}
		return env
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Headless output")
		return protocol.Envelope{}
	}
}

func TestRunEmitsRegisteredAndForwardsValidatedMessages(t *testing.T) {
	conn := newFakeConnection(t)
	inputReader, inputWriter := io.Pipe()
	defer inputReader.Close()
	output := newLineWriter()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, conn, inputReader, output) }()
	if ready := nextOutput(t, output); ready.Type != protocol.TypeRegistered || ready.Seq != 1 {
		t.Fatalf("ready = %+v", ready)
	}

	command, err := protocol.NewEnvelope("msg-chat", protocol.TypeFaceChat, protocol.FaceChat{
		RequestID: "request-chat", ConversationID: "conversation-1", Content: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(command)
	if _, err := inputWriter.Write(append(encoded, '\n')); err != nil {
		t.Fatal(err)
	}
	select {
	case sent := <-conn.sent:
		if sent.Type != protocol.TypeFaceChat || sent.SessionID != "" || sent.Seq != 0 {
			t.Fatalf("sent = %+v", sent)
		}
	case <-time.After(time.Second):
		t.Fatal("command was not forwarded")
	}

	accepted, _ := protocol.NewEnvelope("accepted-1", protocol.TypeFaceAccepted, protocol.FaceAccepted{
		RequestID: "request-chat", ConversationID: "conversation-1", Operation: protocol.FaceOperationChat,
	})
	accepted.SessionID, accepted.From, accepted.To, accepted.Seq = "session-1", "mind", "face-1", 2
	conn.reads <- incoming{env: *accepted}
	if got := nextOutput(t, output); got.Type != protocol.TypeFaceAccepted || got.Seq != 2 {
		t.Fatalf("accepted output = %+v", got)
	}
	cancel()
	_ = inputWriter.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Headless runner did not stop")
	}
}

func TestRunRejectsInvalidInputWithoutEchoingIt(t *testing.T) {
	conn := newFakeConnection(t)
	secret := "do-not-echo-this-secret"
	input := bytes.NewBufferString(`{"type":"face.result","payload":{"request_id":"` + secret + `","status":"succeeded"}}` + "\n")
	output := newLineWriter()
	err := Run(context.Background(), conn, input, output)
	if err == nil {
		t.Fatalf("Run error = %v", err)
	}
	if bytes.Contains([]byte(err.Error()), []byte(secret)) {
		t.Fatalf("error leaked input payload: %v", err)
	}
	if ready := nextOutput(t, output); ready.Type != protocol.TypeRegistered {
		t.Fatalf("ready = %+v", ready)
	}
	select {
	case env := <-conn.sent:
		t.Fatalf("invalid input was sent: %+v", env)
	default:
	}
}

func TestDecodeCommandRejectsConnectionFields(t *testing.T) {
	_, err := decodeCommand([]byte(`{
		"msg_id":"msg-1","type":"face.conversation.list","session_id":"forged",
		"payload":{"request_id":"request-1"}
	}`))
	if err == nil {
		t.Fatal("decodeCommand accepted forged connection fields")
	}
}
