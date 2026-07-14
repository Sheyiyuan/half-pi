// 服务端连接管理，用于 Mind 接受 Face 和 Hand 的 WebSocket 连接。
package wss

import (
	"net/http"

	"github.com/gorilla/websocket"
)

// Server 接受来自 Face 和 Hand 的 WebSocket 连接。
type Server struct {
	upgrader websocket.Upgrader
}

// NewServer 创建 WS 服务端。
func NewServer() *Server {
	return &Server{
		upgrader: websocket.Upgrader{},
	}
}

// Upgrade 将 HTTP 请求升级为 WebSocket 连接。
func (s *Server) Upgrade(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return s.upgrader.Upgrade(w, r, nil)
}
