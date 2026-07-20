// Package tui 实现复用正式 Face 协议的人类终端客户端。
package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-face/internal/client"
)

var errQuit = errors.New("quit terminal Face")

type inputLine struct {
	text string
	err  error
}

type incoming struct {
	env protocol.Envelope
	err error
}

type terminal struct {
	conn     client.Connection
	output   io.Writer
	active   string
	lastChat map[string]string
	pending  map[string]pendingRequest
	features map[protocol.FaceFeature]struct{}
	scopes   map[protocol.FaceScope]struct{}
	streams  map[string]*chatStreamView
	prompted bool
}

type pendingRequest struct {
	operation             protocol.FaceOperation
	conversationID        string
	targetRequestID       string
	recoverConversationID string
	recoverChatIDs        []string
}

type chatStreamView struct {
	responses  map[int]string
	lastSeq    int64
	recovering bool
	buffered   []protocol.FaceChatDelta
	terminal   bool
}

// Run 启动人类终端 Face。
func Run(ctx context.Context, conn client.Connection, input io.Reader, output io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if conn == nil || input == nil || output == nil {
		return fmt.Errorf("terminal Face streams and connection are required")
	}
	defer conn.Close()
	ready := conn.RegisteredEnvelope()
	registered, err := protocol.DecodePayload[protocol.Registered](&ready)
	if err != nil || ready.Type != protocol.TypeRegistered {
		return fmt.Errorf("invalid registered ready message")
	}
	term := &terminal{
		conn: conn, output: output,
		lastChat: make(map[string]string), pending: make(map[string]pendingRequest),
		features: make(map[protocol.FaceFeature]struct{}), scopes: make(map[protocol.FaceScope]struct{}),
		streams: make(map[string]*chatStreamView),
	}
	term.line("Half-Pi Face connected as %s", safeText(registered.ClientID))
	if err := term.listConversations(); err != nil {
		return err
	}
	if err := term.getCapabilities(); err != nil {
		return err
	}
	term.prompt()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := scanInput(runCtx, input)
	messages := readInput(runCtx, conn)
	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			if line.err != nil {
				return fmt.Errorf("read terminal input: %w", line.err)
			}
			if err := term.handleInput(line.text); err != nil {
				if errors.Is(err, errQuit) {
					return nil
				}
				term.line("error: %s", safeText(err.Error()))
			}
			term.prompt()
		case message := <-messages:
			if message.err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("read Mind message: %w", message.err)
			}
			if err := term.handleEnvelope(message.env); err != nil {
				return err
			}
			term.prompt()
		}
	}
}

func (t *terminal) send(requestID, typ string, payload any, operation protocol.FaceOperation) error {
	env, err := protocol.NewEnvelope("", typ, payload)
	if err != nil {
		return err
	}
	if !protocol.IsFaceCommandType(typ) {
		return fmt.Errorf("invalid Face command type %q", typ)
	}
	if err := protocol.ValidateFacePayload(typ, env.Payload); err != nil {
		return err
	}
	var meta protocol.FaceCommandMeta
	if err := json.Unmarshal(env.Payload, &meta); err != nil {
		return err
	}
	if meta.RequestID != requestID {
		return fmt.Errorf("Face command request ID mismatch")
	}
	if err := t.conn.Send(*env); err != nil {
		return err
	}
	t.pending[requestID] = pendingRequest{operation: operation, conversationID: meta.ConversationID}
	return nil
}

func (t *terminal) nextRequestID() (string, error) {
	return protocol.NewMsgID()
}

func (t *terminal) prompt() {
	conversation := "no conversation"
	if t.active != "" {
		conversation = t.active
	}
	fmt.Fprintf(t.output, "[%s]> ", safeText(conversation))
	t.prompted = true
}

func (t *terminal) line(format string, args ...any) {
	if t.prompted {
		fmt.Fprintln(t.output)
		t.prompted = false
	}
	fmt.Fprintf(t.output, format+"\n", args...)
}

func scanInput(ctx context.Context, input io.Reader) <-chan inputLine {
	result := make(chan inputLine, 1)
	go func() {
		defer close(result)
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 64*1024), wss.MaxFrameSize)
		for scanner.Scan() {
			select {
			case result <- inputLine{text: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case result <- inputLine{err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return result
}

func readInput(ctx context.Context, conn client.Connection) <-chan incoming {
	result := make(chan incoming, 1)
	go func() {
		for {
			env, err := conn.Read()
			select {
			case result <- incoming{env: env, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return result
}

func safeText(value string) string {
	var result bytes.Buffer
	for _, char := range value {
		switch char {
		case '\n':
			result.WriteString(`\n`)
		case '\r':
			result.WriteString(`\r`)
		case '\t':
			result.WriteString(`\t`)
		default:
			if char < 0x20 || char == 0x7f || (char >= 0x80 && char <= 0x9f) {
				fmt.Fprintf(&result, `\u%04x`, char)
			} else {
				result.WriteRune(char)
			}
		}
	}
	return result.String()
}

func decodeData[T any](raw json.RawMessage) (T, error) {
	return protocol.StrictDecode[T](raw)
}
