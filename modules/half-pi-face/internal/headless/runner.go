// Package headless 实现 stdin/stdout JSONL Agent Face。
package headless

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-face/internal/client"
)

type inputLine struct {
	data []byte
	err  error
}

type incoming struct {
	env protocol.Envelope
	err error
}

// Run 将 JSONL command 转发到 Mind，并把正式入站 envelope 输出为 JSONL。
func Run(ctx context.Context, conn client.Connection, input io.Reader, output io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if conn == nil || input == nil || output == nil {
		return fmt.Errorf("Headless Face streams and connection are required")
	}
	defer conn.Close()
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(conn.RegisteredEnvelope()); err != nil {
		return fmt.Errorf("write registered message: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := scanLines(runCtx, input)
	messages := readMessages(runCtx, conn)
	lineNumber := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			lineNumber++
			if line.err != nil {
				return fmt.Errorf("read command line %d: %w", lineNumber, line.err)
			}
			if len(bytes.TrimSpace(line.data)) == 0 {
				continue
			}
			env, err := decodeCommand(line.data)
			if err != nil {
				return fmt.Errorf("command line %d: %w", lineNumber, err)
			}
			if err := conn.Send(env); err != nil {
				return fmt.Errorf("send command line %d: %w", lineNumber, err)
			}
		case message := <-messages:
			if message.err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("read Mind message: %w", message.err)
			}
			if !protocol.IsFaceServerMessageType(message.env.Type) {
				return fmt.Errorf("Mind sent invalid Face message type %q", message.env.Type)
			}
			if err := protocol.ValidateFacePayload(message.env.Type, message.env.Payload); err != nil {
				return fmt.Errorf("Mind sent invalid Face payload: %w", err)
			}
			if err := encoder.Encode(message.env); err != nil {
				return fmt.Errorf("write Mind message: %w", err)
			}
		}
	}
}

func decodeCommand(line []byte) (protocol.Envelope, error) {
	env, err := protocol.StrictDecode[protocol.Envelope](line)
	if err != nil {
		return protocol.Envelope{}, fmt.Errorf("decode Face command: %w", err)
	}
	if env.SessionID != "" || env.From != "" || env.To != "" || env.Seq != 0 {
		return protocol.Envelope{}, fmt.Errorf("connection fields are assigned by the Face client")
	}
	if !protocol.IsFaceCommandType(env.Type) {
		return protocol.Envelope{}, fmt.Errorf("invalid Face command type %q", env.Type)
	}
	if err := protocol.ValidateFacePayload(env.Type, env.Payload); err != nil {
		return protocol.Envelope{}, err
	}
	return env, nil
}

func scanLines(ctx context.Context, input io.Reader) <-chan inputLine {
	result := make(chan inputLine, 1)
	go func() {
		defer close(result)
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 64*1024), wss.MaxFrameSize)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case result <- inputLine{data: line}:
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

func readMessages(ctx context.Context, conn client.Connection) <-chan incoming {
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
