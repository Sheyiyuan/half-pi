// Package protocol 定义 Face、Mind、Hand 之间 WebSocket 消息类型。
package protocol

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
)

const (
	TypeRegister   = "register"
	TypeRegistered = "registered"
	TypeRPC        = "rpc"
	TypeRPCResult  = "rpc_result"
	TypePing       = "ping"
	TypePong       = "pong"
	TypeError      = "error"

	TypeHandInfoReq  = "hand_info_req"  // Mind → Hand：查询可用工具列表
	TypeHandInfoResp = "hand_info_resp" // Hand → Mind：返回工具列表
	TypeHandEvent    = "hand_event"     // Hand → Mind：主动监控事件上报
)

// Envelope 是所有 WS 消息的包装，携带路由和加密上下文。
type Envelope struct {
	MsgID     string          `json:"msg_id"`
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"` // 会话 ID，用于重放防护
	From      string          `json:"from,omitempty"`       // 发送方 client_id
	To        string          `json:"to,omitempty"`         // 接收方 client_id
	Seq       int64           `json:"seq,omitempty"`        // 单调递增序号，重放防护
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// AAD 构建加密附加认证数据，绑定消息上下文防止挪用。
func (e *Envelope) AAD() []byte {
	data, _ := json.Marshal(struct {
		Version   int    `json:"v"`
		MsgID     string `json:"msg_id"`
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		From      string `json:"from"`
		To        string `json:"to"`
		Seq       int64  `json:"seq"`
	}{
		Version:   1,
		MsgID:     e.MsgID,
		Type:      e.Type,
		SessionID: e.SessionID,
		From:      e.From,
		To:        e.To,
		Seq:       e.Seq,
	})
	return data
}

// Register 是 Face 或 Hand 首次连接时发送的注册消息。
type Register struct {
	ClientID string   `json:"client_id"`
	Token    string   `json:"token"`
	Type     string   `json:"type"`           // "face" | "hand"
	Info     HandInfo `json:"info,omitempty"` // Hand 静态设备信息
}

// HandInfo Hand 注册时上报的静态设备信息。
type HandInfo struct {
	OS       string `json:"os"`       // 操作系统：linux / darwin / windows
	Arch     string `json:"arch"`     // CPU 架构：amd64 / arm64
	Hostname string `json:"hostname"` // 主机名
	WorkDir  string `json:"work_dir"` // 工作目录
}

// Registered 是服务端接受注册后返回给客户端的会话信息。
type Registered struct {
	ClientID  string `json:"client_id"`
	ServerID  string `json:"server_id"`
	SessionID string `json:"session_id"`
}

// EncryptedPayload 是 payload 加密后的 JSON 表示。
// Data 是 nonce || ciphertext 的 base64 编码，AAD 来自 Envelope.AAD()。
type EncryptedPayload struct {
	Alg  string `json:"alg"`
	Data string `json:"data"`
}

// RPC 由 Mind 发送，请求 Hand 执行工具。
type RPC struct {
	ID         string         `json:"id"`
	Tool       string         `json:"tool"`
	Args       map[string]any `json:"args"`
	SkipChecks bool           `json:"skip_checks"` // Mind 已安全检查，Hand 直接执行
	TimeoutMs  int            `json:"timeout_ms,omitempty"`
}

// RPCResult 是 Hand 执行工具后的返回。
type RPCResult struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// HandInfoReq Mind → Hand：请求 Hand 动态信息（工具列表等）。
type HandInfoReq struct {
	ID string `json:"id"` // 请求 ID，用于匹配响应
}

// HandInfoResp Hand → Mind：返回 Hand 动态信息。
type HandInfoResp struct {
	ID      string   `json:"id"`       // 匹配 HandInfoReq.ID
	Tools   []string `json:"tools"`    // 可用工具名称列表
	OS      string   `json:"os"`       // 操作系统
	Host    string   `json:"host"`     // 主机名
	WorkDir string   `json:"work_dir"` // 工作目录
}

// HandEvent Hand → Mind：主动监控事件上报。
type HandEvent struct {
	Name      string `json:"name"`    // 监控项名称
	HandID    string `json:"hand_id"` // 发送 Hand 的 ID
	Status    string `json:"status"`  // "triggered" | "ok"
	Output    string `json:"output"`  // 检测命令的 stdout
	Timestamp int64  `json:"ts"`      // unix 时间戳
}

// Ping 用于心跳检测。
type Ping struct {
	Timestamp int64 `json:"ts"`
}

// Pong 是心跳应答。
type Pong struct {
	Timestamp int64 `json:"ts"`
}

// ErrorMsg 携带错误信息。
type ErrorMsg struct {
	MsgID   string `json:"msg_id,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewEnvelope 创建一个携带已编码 payload 的 Envelope。
func NewEnvelope(msgID, typ string, payload any) (*Envelope, error) {
	if msgID == "" {
		var err error
		msgID, err = NewMsgID()
		if err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Envelope{MsgID: msgID, Type: typ, Payload: data}, nil
}

// DecodePayload 将 Envelope 的 Payload 反序列化为目标类型。
func DecodePayload[T any](env *Envelope) (T, error) {
	var v T
	if err := json.Unmarshal(env.Payload, &v); err != nil {
		return v, err
	}
	return v, nil
}

// NewEncryptedPayload 将密文字节编码为标准 payload。
func NewEncryptedPayload(alg string, data []byte) (json.RawMessage, error) {
	if alg == "" {
		return nil, fmt.Errorf("alg is required")
	}
	return json.Marshal(EncryptedPayload{
		Alg:  alg,
		Data: base64.StdEncoding.EncodeToString(data),
	})
}

// DecodeEncryptedPayload 解码加密 payload。
func DecodeEncryptedPayload(env *Envelope) (EncryptedPayload, []byte, error) {
	payload, err := DecodePayload[EncryptedPayload](env)
	if err != nil {
		return EncryptedPayload{}, nil, err
	}
	if payload.Alg == "" {
		return EncryptedPayload{}, nil, fmt.Errorf("encrypted payload alg is required")
	}
	data, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil {
		return EncryptedPayload{}, nil, fmt.Errorf("decode encrypted payload: %w", err)
	}
	return payload, data, nil
}

// NewMsgID 生成随机 msg_id（8 字节 hex）。
func NewMsgID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate msg_id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// MustNewMsgID 生成随机 msg_id，失败时 panic。适合测试和初始化路径。
func MustNewMsgID() string {
	id, err := NewMsgID()
	if err != nil {
		panic(err)
	}
	return id
}

// Session 维护一条连接上单方向入站/出站的 replay 状态。
type Session struct {
	LocalID   string
	RemoteID  string
	SessionID string

	mu     sync.Mutex
	inSeq  int64
	outSeq int64
}

// NewSession 创建客户端或服务端可复用的连接级会话状态。
func NewSession(localID, remoteID, sessionID string) (*Session, error) {
	if localID == "" {
		return nil, fmt.Errorf("local id is required")
	}
	if remoteID == "" {
		return nil, fmt.Errorf("remote id is required")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	return &Session{LocalID: localID, RemoteID: remoteID, SessionID: sessionID}, nil
}

// Stamp 填充出站 envelope 的 replay 字段，并在缺少 msg_id 时生成。
func (s *Session) Stamp(env Envelope) (Envelope, error) {
	if env.Type == "" {
		return Envelope{}, fmt.Errorf("message type is required")
	}
	if env.MsgID == "" {
		id, err := NewMsgID()
		if err != nil {
			return Envelope{}, err
		}
		env.MsgID = id
	}

	s.mu.Lock()
	s.outSeq++
	seq := s.outSeq
	s.mu.Unlock()

	env.SessionID = s.SessionID
	env.From = s.LocalID
	env.To = s.RemoteID
	env.Seq = seq
	return env, nil
}

// Accept 校验入站 envelope 的上下文和严格单调序号。
func (s *Session) Accept(env Envelope) error {
	if env.Type == "" {
		return fmt.Errorf("message type is required")
	}
	if env.SessionID != s.SessionID {
		return fmt.Errorf("session_id mismatch")
	}
	if env.From != s.RemoteID {
		return fmt.Errorf("from mismatch: got %q, want %q", env.From, s.RemoteID)
	}
	if env.To != s.LocalID {
		return fmt.Errorf("to mismatch: got %q, want %q", env.To, s.LocalID)
	}
	if env.Seq <= 0 {
		return fmt.Errorf("seq must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	want := s.inSeq + 1
	if env.Seq != want {
		return fmt.Errorf("replay or out-of-order message: got seq %d, want %d", env.Seq, want)
	}
	s.inSeq = env.Seq
	return nil
}
