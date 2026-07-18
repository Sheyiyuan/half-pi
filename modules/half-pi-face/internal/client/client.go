// Package client 提供 Headless 和人类 Face 共用的加密协议连接。
package client

import (
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

// Client 包装 gateway-core 的加密 SessionConn。
type Client struct {
	session *wss.SessionConn
}

// Dial 连接 Mind 并完成 Face 四步挑战握手。
func Dial(cfg *config.Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	session, err := wss.NewClient(cfg.Server.URL).ConnectAndRegister(wss.Credentials{
		Label: cfg.Face.ID, Type: protocol.PeerFace, Token: cfg.Server.Token,
		ApplicationKey: cfg.Server.ApplicationKey,
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
