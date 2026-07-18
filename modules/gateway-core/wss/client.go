// 客户端连接，用于 Face 或 Hand 连接到 Mind。
package wss

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

const clientWriteTimeout = 10 * time.Second

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Credentials 是四步注册所需的不可变身份凭据。
type Credentials struct {
	Label          string
	Type           protocol.PeerType
	Token          string
	ApplicationKey string
	Info           *protocol.HandInfo
}

// HandshakeError 是服务端返回的稳定握手错误。
type HandshakeError struct {
	Code string
}

func (e *HandshakeError) Error() string { return "handshake failed: " + e.Code }

// Permanent 报告客户端是否必须停止自动重试。
func (e *HandshakeError) Permanent() bool {
	return e.Code == "unsupported_protocol" || e.Code == "authentication_failed"
}

// Retryable 报告客户端是否可使用 capped backoff 重试。
func (e *HandshakeError) Retryable() bool { return e.Code == "duplicate_peer" }

// Client 是 WS 客户端，供 Face 和 Hand 使用以连接 Mind。
type Client struct{ url string }

// NewClient 创建 WS 客户端。
func NewClient(serverURL string) *Client { return &Client{url: serverURL} }

// Connect 与服务器建立 WebSocket 连接。
func (c *Client) Connect() (*websocket.Conn, error) {
	conn, _, err := websocket.DefaultDialer.Dial(c.url, nil)
	return conn, err
}

// SessionConn 是完成注册后的加密客户端连接。
type SessionConn struct {
	Conn      *websocket.Conn
	Session   *protocol.Session
	inCipher  *AES128GCM
	outCipher *AES128GCM
	writeMu   sync.Mutex
}

// ConnectAndRegister 连接服务端并完成 version 1 四步挑战握手。
func (c *Client) ConnectAndRegister(credentials Credentials) (*SessionConn, error) {
	if err := validateCredentials(credentials); err != nil {
		return nil, err
	}
	conn, err := c.Connect()
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*SessionConn, error) {
		_ = conn.Close()
		return nil, err
	}
	conn.SetReadLimit(MaxFrameSize)
	deadline := time.Now().Add(10 * time.Second)
	_ = conn.SetReadDeadline(deadline)
	_ = conn.SetWriteDeadline(deadline)
	reg, err := protocol.NewEnvelope("", protocol.TypeRegister, protocol.Register{
		ProtocolVersion: protocol.ProtocolVersion,
		ClientID:        credentials.Label,
		Token:           credentials.Token,
		Type:            credentials.Type,
		Info:            credentials.Info,
	})
	if err != nil {
		return fail(err)
	}
	if err := conn.WriteJSON(reg); err != nil {
		return fail(fmt.Errorf("send register: %w", err))
	}

	challengeEnv, err := readHandshakeEnvelope(conn)
	if err != nil {
		return fail(err)
	}
	if challengeEnv.Type == protocol.TypeError {
		return fail(decodeHandshakeError(challengeEnv))
	}
	if challengeEnv.Type != protocol.TypeRegisterChallenge {
		return fail(fmt.Errorf("expected register_challenge, got %q", challengeEnv.Type))
	}
	challenge, err := protocol.DecodePayload[protocol.RegisterChallenge](&challengeEnv)
	if err != nil || challenge.ProtocolVersion != protocol.ProtocolVersion || challenge.Algorithm != protocol.HandshakeAlgorithm || challenge.ServerID == "" || challenge.SessionID == "" || challenge.HandshakeID == "" || challenge.ExpiresAt <= time.Now().UnixMilli() || challenge.ExpiresAt > deadline.Add(time.Second).UnixMilli() {
		return fail(fmt.Errorf("invalid register challenge"))
	}
	transcript := protocol.HandshakeTranscript{
		ProtocolVersion: protocol.ProtocolVersion,
		PeerType:        credentials.Type,
		Label:           credentials.Label,
		HandshakeID:     challenge.HandshakeID,
		ServerID:        challenge.ServerID,
		SessionID:       challenge.SessionID,
		Challenge:       challenge.Challenge,
	}
	keys, err := DeriveSessionKeys(credentials.ApplicationKey, transcript)
	if err != nil {
		return fail(err)
	}
	proof, err := NewRegisterProof(keys, transcript)
	if err != nil {
		return fail(err)
	}
	proofEnv, err := protocol.NewEnvelope("", protocol.TypeRegisterProof, proof)
	if err != nil {
		return fail(err)
	}
	if err := conn.WriteJSON(proofEnv); err != nil {
		return fail(fmt.Errorf("send register proof: %w", err))
	}

	session, err := protocol.NewSession(credentials.Label, challenge.ServerID, challenge.SessionID)
	if err != nil {
		return fail(err)
	}
	inCipher, err := NewAES128GCM(keys.ServerToClient[:])
	if err != nil {
		return fail(err)
	}
	outCipher, err := NewAES128GCM(keys.ClientToServer[:])
	if err != nil {
		return fail(err)
	}
	registeredEnv, err := readHandshakeEnvelope(conn)
	if err != nil {
		return fail(err)
	}
	if registeredEnv.Type == protocol.TypeError {
		return fail(decodeHandshakeError(registeredEnv))
	}
	if registeredEnv.Type != protocol.TypeRegistered {
		return fail(fmt.Errorf("expected registered, got %q", registeredEnv.Type))
	}
	if err := session.Accept(registeredEnv); err != nil {
		return fail(fmt.Errorf("accept registered: %w", err))
	}
	registered, err := DecryptEnvelopePayload[protocol.Registered](inCipher, &registeredEnv)
	if err != nil {
		return fail(fmt.Errorf("decrypt registered: %w", err))
	}
	if registered.ProtocolVersion != protocol.ProtocolVersion || registered.ClientID != credentials.Label || registered.ServerID != challenge.ServerID || registered.SessionID != challenge.SessionID {
		return fail(fmt.Errorf("registered payload mismatch"))
	}
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
	return &SessionConn{Conn: conn, Session: session, inCipher: inCipher, outCipher: outCipher}, nil
}

func validateCredentials(credentials Credentials) error {
	if !labelPattern.MatchString(credentials.Label) {
		return fmt.Errorf("invalid label")
	}
	if credentials.Type != protocol.PeerHand && credentials.Type != protocol.PeerFace {
		return fmt.Errorf("valid peer type is required")
	}
	for name, value := range map[string]string{"token": credentials.Token, "application key": credentials.ApplicationKey} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != value {
			return fmt.Errorf("%s must be 32 lowercase hex characters", name)
		}
	}
	if credentials.Type == protocol.PeerFace && credentials.Info != nil {
		return fmt.Errorf("face credentials must omit info")
	}
	if credentials.Type == protocol.PeerHand && (credentials.Info == nil || credentials.Info.OS == "" || credentials.Info.Arch == "" || credentials.Info.Hostname == "") {
		return fmt.Errorf("hand info is required")
	}
	return nil
}

func readHandshakeEnvelope(conn *websocket.Conn) (protocol.Envelope, error) {
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return protocol.Envelope{}, err
	}
	return protocol.StrictDecode[protocol.Envelope](raw)
}

func decodeHandshakeError(env protocol.Envelope) error {
	msg, err := protocol.DecodePayload[protocol.ErrorMsg](&env)
	if err != nil {
		return err
	}
	return &HandshakeError{Code: msg.Code}
}

// Send 发送并加密业务 payload；调用方 Envelope.Payload 应由 NewEnvelope 创建。
func (c *SessionConn) Send(env protocol.Envelope) (err error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.Conn.SetWriteDeadline(time.Now().Add(clientWriteTimeout)); err != nil {
		return err
	}
	stamped, err := c.Session.Stamp(env)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = c.Conn.Close()
		}
	}()
	if err := c.outCipher.EncryptEnvelopePayload(&stamped, json.RawMessage(env.Payload)); err != nil {
		return err
	}
	return c.Conn.WriteJSON(stamped)
}

// SendPayload 创建、stamp 并加密一条 typed payload 消息。
func (c *SessionConn) SendPayload(msgID, messageType string, payload any) error {
	env, err := protocol.NewEnvelope(msgID, messageType, payload)
	if err != nil {
		return err
	}
	return c.Send(*env)
}

// Read 读取、校验并解密一条业务消息。
func (c *SessionConn) Read() (env protocol.Envelope, err error) {
	defer func() {
		if err != nil {
			_ = c.Conn.Close()
		}
	}()
	_, raw, err := c.Conn.ReadMessage()
	if err != nil {
		return protocol.Envelope{}, err
	}
	env, err = protocol.StrictDecode[protocol.Envelope](raw)
	if err != nil {
		return protocol.Envelope{}, err
	}
	if err := c.Session.Accept(env); err != nil {
		return protocol.Envelope{}, err
	}
	plain, err := DecryptEnvelopePayload[json.RawMessage](c.inCipher, &env)
	if err != nil {
		return protocol.Envelope{}, err
	}
	env.Payload = plain
	return env, nil
}

// ReadPayload 读取消息并严格解码其 typed payload。
func ReadPayload[T any](c *SessionConn) (protocol.Envelope, T, error) {
	var zero T
	env, err := c.Read()
	if err != nil {
		return protocol.Envelope{}, zero, err
	}
	payload, err := protocol.DecodePayload[T](&env)
	if err != nil {
		_ = c.Conn.Close()
	}
	return env, payload, err
}
