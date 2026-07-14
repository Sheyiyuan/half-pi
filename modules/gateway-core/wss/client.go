// 客户端连接，用于 Face 或 Hand 连接到 Mind。
package wss

import (
	"fmt"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// Client 是 WS 客户端，供 Face 和 Hand 使用以连接 Mind。
type Client struct {
	url string
}

// NewClient 创建 WS 客户端。
func NewClient(serverURL string) *Client {
	return &Client{url: serverURL}
}

// Connect 与服务器建立 WebSocket 连接。
func (c *Client) Connect() (*websocket.Conn, error) {
	conn, _, err := websocket.DefaultDialer.Dial(c.url, nil)
	return conn, err
}

// SessionConn 是完成注册后的客户端连接。
type SessionConn struct {
	Conn    *websocket.Conn
	Session *protocol.Session
}

// ConnectAndRegister 连接服务端并完成 register/registered 握手。
func (c *Client) ConnectAndRegister(clientID, peerType, token string) (*SessionConn, error) {
	conn, err := c.Connect()
	if err != nil {
		return nil, err
	}

	reg, err := protocol.NewEnvelope("", protocol.TypeRegister, protocol.Register{
		ClientID: clientID,
		Token:    token,
		Type:     peerType,
	})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteJSON(reg); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send register: %w", err)
	}

	var env protocol.Envelope
	if err := conn.ReadJSON(&env); err != nil {
		conn.Close()
		return nil, fmt.Errorf("read registered: %w", err)
	}
	if env.Type != protocol.TypeRegistered {
		conn.Close()
		return nil, fmt.Errorf("expected registered, got %q", env.Type)
	}
	registered, err := protocol.DecodePayload[protocol.Registered](&env)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("decode registered: %w", err)
	}
	if registered.ClientID != clientID {
		conn.Close()
		return nil, fmt.Errorf("registered client_id mismatch: got %q, want %q", registered.ClientID, clientID)
	}

	session, err := protocol.NewSession(clientID, registered.ServerID, registered.SessionID)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := session.Accept(env); err != nil {
		conn.Close()
		return nil, fmt.Errorf("accept registered: %w", err)
	}

	return &SessionConn{Conn: conn, Session: session}, nil
}

// Send 发送业务消息，并自动填充 session/from/to/seq。
func (c *SessionConn) Send(env protocol.Envelope) error {
	stamped, err := c.Session.Stamp(env)
	if err != nil {
		return err
	}
	return c.Conn.WriteJSON(stamped)
}

// Read 读取并校验一条业务消息。
func (c *SessionConn) Read() (protocol.Envelope, error) {
	var env protocol.Envelope
	if err := c.Conn.ReadJSON(&env); err != nil {
		return protocol.Envelope{}, err
	}
	if err := c.Session.Accept(env); err != nil {
		return protocol.Envelope{}, err
	}
	return env, nil
}
