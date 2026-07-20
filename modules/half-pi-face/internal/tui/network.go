package tui

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-face/internal/client"
)

type authenticateMsg struct{ generation uint64 }

type connectedMsg struct {
	generation uint64
	conn       client.Connection
}

type connectionFailedMsg struct {
	generation uint64
	err        error
	permanent  bool
}

type envelopeMsg struct {
	generation uint64
	env        protocol.Envelope
}

type connectionLostMsg struct {
	generation uint64
	err        error
}

type sendDoneMsg struct {
	generation uint64
	requestID  string
	err        error
}

type retryMsg struct{ generation uint64 }

func connectCmd(ctx context.Context, connector client.Connector, generation uint64) tea.Cmd {
	return func() tea.Msg {
		if connector == nil {
			return connectionFailedMsg{generation: generation, err: fmt.Errorf("Face Connector is required"), permanent: true}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		conn, err := connector.Connect(ctx)
		if err != nil {
			return connectionFailedMsg{generation: generation, err: err, permanent: permanentConnectionError(err)}
		}
		return connectedMsg{generation: generation, conn: conn}
	}
}

func permanentConnectionError(err error) bool {
	var handshakeError *wss.HandshakeError
	return errors.As(err, &handshakeError) && handshakeError.Permanent()
}

func readCmd(conn client.Connection, generation uint64) tea.Cmd {
	return func() tea.Msg {
		env, err := conn.Read()
		if err != nil {
			return connectionLostMsg{generation: generation, err: err}
		}
		return envelopeMsg{generation: generation, env: env}
	}
}

func sendEnvelopeCmd(conn client.Connection, generation uint64, requestID, messageType string, payload any) tea.Cmd {
	return func() tea.Msg {
		env, err := protocol.NewEnvelope(requestID, messageType, payload)
		if err == nil {
			err = conn.Send(*env)
		}
		return sendDoneMsg{generation: generation, requestID: requestID, err: err}
	}
}

func retryDelay(attempt int, jitter float64) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	base := time.Second << min(attempt, 5)
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	factor := 0.8 + jitter*0.4
	return time.Duration(float64(base) * factor)
}

func retryCmd(generation uint64, attempt int) (tea.Cmd, time.Time) {
	delay := retryDelay(attempt, rand.Float64())
	return tea.Tick(delay, func(time.Time) tea.Msg { return retryMsg{generation: generation} }), time.Now().Add(delay)
}
