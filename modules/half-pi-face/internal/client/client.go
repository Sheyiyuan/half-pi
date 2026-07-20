// Package client 提供 Headless 和人类 Face 共用的加密协议连接。
package client

import (
	"context"
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/wss"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-face/internal/config"
)

// Connection 是 Face 入口共用的已注册协议连接。
type Connection interface {
	RegisteredEnvelope() protocol.Envelope
	Send(protocol.Envelope) error
	Read() (protocol.Envelope, error)
	Close() error
}

// Connector 建立一条完成注册的 Face 协议连接。
type Connector interface {
	Connect(context.Context) (Connection, error)
}

// Dialer 保存建立 Face 连接所需的不可变配置。
type Dialer struct {
	serverURL      string
	label          string
	token          string
	applicationKey string
}

// Client 包装 gateway-core 的加密 SessionConn。
type Client struct {
	session *wss.SessionConn
}

// Dial 连接 Mind 并完成 Face 四步挑战握手。
func Dial(cfg *config.Config) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("Face config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	connection, err := NewDialer(cfg).Connect(context.Background())
	if err != nil {
		return nil, err
	}
	result, ok := connection.(*Client)
	if !ok {
		return nil, fmt.Errorf("unexpected Face connection type")
	}
	return result, nil
}

// NewDialer 从配置创建可重复使用的 Face Connector。
func NewDialer(cfg *config.Config) *Dialer {
	if cfg == nil {
		return &Dialer{}
	}
	return &Dialer{
		serverURL: cfg.Server.URL, label: cfg.Face.ID, token: cfg.Server.Token,
		applicationKey: cfg.Server.ApplicationKey,
	}
}

// Connect 连接 Mind 并完成 Face 四步挑战握手。
func (d *Dialer) Connect(ctx context.Context) (Connection, error) {
	if d == nil {
		return nil, fmt.Errorf("Face Dialer is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if d.serverURL == "" || d.label == "" || d.token == "" || d.applicationKey == "" {
		return nil, fmt.Errorf("Face Dialer configuration is incomplete")
	}
	session, err := wss.NewClient(d.serverURL).ConnectAndRegisterContext(ctx, wss.Credentials{
		Label: d.label, Type: protocol.PeerFace, Token: d.token,
		ApplicationKey: d.applicationKey,
	})
	if err != nil {
		return nil, fmt.Errorf("connect Face to Mind: %w", err)
	}
	return &Client{session: session}, nil
}

// RegisteredEnvelope 返回握手成功的正式 ready 消息。
func (c *Client) RegisteredEnvelope() protocol.Envelope {
	return c.session.RegisteredEnvelope()
}

// Send 发送一条 Face command。
func (c *Client) Send(env protocol.Envelope) error {
	return c.session.Send(env)
}

// Read 读取一条 Mind Face 消息。
func (c *Client) Read() (protocol.Envelope, error) {
	return c.session.Read()
}

// Close 关闭协议连接。
func (c *Client) Close() error {
	return c.session.Conn.Close()
}
