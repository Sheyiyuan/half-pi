// Package protocol 定义 Face、Mind、Hand 之间 WebSocket 消息类型。
package protocol

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

const (
	TypeRegister          = "register"
	TypeRegisterChallenge = "register_challenge"
	TypeRegisterProof     = "register_proof"
	TypeRegistered        = "registered"
	TypeRPC               = "rpc"
	TypeRPCAccepted       = "rpc_accepted"
	TypeRPCRejected       = "rpc_rejected"
	TypeRPCProgress       = "rpc_progress"
	TypeRPCResult         = "rpc_result"
	TypeRPCCancel         = "rpc_cancel"
	TypeRPCCancelResult   = "rpc_cancel_result"
	TypeTaskStatusReq     = "task_status_req"
	TypeTaskStatusResp    = "task_status_resp"
	TypeTaskLogReq        = "task_log_req"
	TypeTaskLogResp       = "task_log_resp"
	TypeTaskCancel        = "task_cancel"
	TypeTaskCancelResult  = "task_cancel_result"
	TypePing              = "ping"
	TypePong              = "pong"
	TypeError             = "error"

	TypeHandInfoReq  = "hand_info_req"  // Mind → Hand：查询可用工具列表
	TypeHandInfoResp = "hand_info_resp" // Hand → Mind：返回工具列表
	TypeHandEvent    = "hand_event"     // Hand → Mind：主动监控事件上报
)

const (
	// ProtocolVersion 是当前唯一支持的 wire protocol 版本。
	ProtocolVersion = 2
	// HandshakeAlgorithm 是注册证明和会话 payload 使用的算法。
	HandshakeAlgorithm = "AES-128-GCM"
)

// PeerType 标识注册节点的角色。
type PeerType string

const (
	PeerFace PeerType = "face"
	PeerHand PeerType = "hand"
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

// Register 是 Face 或 Hand 首次连接时发送的公开路由信息，不包含长期秘密。
type Register struct {
	ProtocolVersion int      `json:"protocol_version"`
	ClientID        string   `json:"client_id"`
	Type            PeerType `json:"type"`
}

// HandInfo 是 Hand 在加密注册证明中上报的静态设备信息。
type HandInfo struct {
	OS       string `json:"os"`       // 操作系统：linux / darwin / windows
	Arch     string `json:"arch"`     // CPU 架构：amd64 / arm64
	Hostname string `json:"hostname"` // 主机名
	WorkDir  string `json:"work_dir"` // 工作目录
}

// Registered 是服务端接受注册后加密返回给客户端的会话信息。
type Registered struct {
	ProtocolVersion int    `json:"protocol_version"`
	ClientID        string `json:"client_id"`
	ServerID        string `json:"server_id"`
	SessionID       string `json:"session_id"`
}

// RegisterChallenge 是服务端绑定当前连接发出的单次挑战。
type RegisterChallenge struct {
	ProtocolVersion int    `json:"protocol_version"`
	HandshakeID     string `json:"handshake_id"`
	ServerID        string `json:"server_id"`
	SessionID       string `json:"session_id"`
	Challenge       string `json:"challenge"`
	ExpiresAt       int64  `json:"expires_at"`
	Algorithm       string `json:"algorithm"`
}

// RegisterProof 是客户端对注册挑战的持钥证明。
type RegisterProof struct {
	ProtocolVersion int    `json:"protocol_version"`
	HandshakeID     string `json:"handshake_id"`
	Algorithm       string `json:"algorithm"`
	Proof           string `json:"proof"`
}

// RegisterProofClaims 是 register proof 内部加密的身份声明。
type RegisterProofClaims struct {
	Challenge string    `json:"challenge"`
	HandInfo  *HandInfo `json:"hand_info,omitempty"`
}

// HandshakeTranscript 是会话密钥派生使用的规范 transcript。
type HandshakeTranscript struct {
	ProtocolVersion int      `json:"protocol_version"`
	PeerType        PeerType `json:"peer_type"`
	Label           string   `json:"label"`
	HandshakeID     string   `json:"handshake_id"`
	ServerID        string   `json:"server_id"`
	SessionID       string   `json:"session_id"`
	Challenge       string   `json:"challenge"`
}

// RegisterProofAAD 是 register proof 的固定附加认证数据。
type RegisterProofAAD struct {
	ProtocolVersion int      `json:"protocol_version"`
	Type            string   `json:"type"`
	PeerType        PeerType `json:"peer_type"`
	Label           string   `json:"label"`
	HandshakeID     string   `json:"handshake_id"`
	ServerID        string   `json:"server_id"`
	SessionID       string   `json:"session_id"`
}

// EncryptedPayload 是 payload 加密后的 JSON 表示。
// Data 是 nonce || ciphertext 的 base64 编码，AAD 来自 Envelope.AAD()。
type EncryptedPayload struct {
	Alg  string `json:"alg"`
	Data string `json:"data"`
}

// HandInfoReq Mind → Hand：请求 Hand 动态信息（工具列表等）。
type HandInfoReq struct {
	ID string `json:"id"` // 请求 ID，用于匹配响应
}

// HandInfoResp Hand → Mind：返回 Hand 动态信息。
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type HandInfoResp struct {
	ID      string     `json:"id"`       // 匹配 HandInfoReq.ID
	Tools   []ToolInfo `json:"tools"`    // 可用工具及参数 schema
	OS      string     `json:"os"`       // 操作系统
	Host    string     `json:"host"`     // 主机名
	WorkDir string     `json:"work_dir"` // 工作目录
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
	return StrictDecode[T](env.Payload)
}

// StrictDecode 严格解码单个 JSON 值，拒绝未知字段和 trailing data。
func StrictDecode[T any](data []byte) (T, error) {
	var v T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&v); err != nil {
		return v, err
	}
	if decoder.More() {
		return v, fmt.Errorf("trailing JSON data")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return v, fmt.Errorf("trailing JSON data")
	} else if err != io.EOF {
		return v, fmt.Errorf("trailing JSON data: %w", err)
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
