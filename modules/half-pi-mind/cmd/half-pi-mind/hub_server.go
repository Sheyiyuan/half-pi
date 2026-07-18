package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
)

type runningHubServer struct {
	server   *http.Server
	listener net.Listener
	errors   chan error
	wsURL    string
}

type mindReady struct {
	Type    string `json:"type"`
	PID     int    `json:"pid"`
	Address string `json:"address"`
	WSURL   string `json:"ws_url"`
}

func startHubServer(cfg config.ServerConfig, wsHub *hub.Hub) (*runningHubServer, error) {
	if wsHub == nil {
		return nil, fmt.Errorf("WebSocket Hub is required")
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("server.host is required")
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("server.port must be between 0 and 65535")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return nil, fmt.Errorf("listen Mind Hub: %w", err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("resolve Mind Hub address: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = wsHub.ServeWS(conn)
	})
	server := &runningHubServer{
		server:   &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second},
		listener: listener, errors: make(chan error, 1),
		wsURL: (&url.URL{Scheme: "ws", Host: net.JoinHostPort(cfg.Host, port), Path: "/ws"}).String(),
	}
	go func() {
		err := server.server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			server.errors <- fmt.Errorf("serve Mind Hub: %w", err)
		}
		close(server.errors)
	}()
	return server, nil
}

func (s *runningHubServer) ready(pid int) mindReady {
	return mindReady{Type: "mind.ready", PID: pid, Address: s.listener.Addr().String(), WSURL: s.wsURL}
}

func (s *runningHubServer) shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func writeMindReady(output io.Writer, ready mindReady) error {
	if output == nil {
		return fmt.Errorf("ready output is required")
	}
	if err := json.NewEncoder(output).Encode(ready); err != nil {
		return fmt.Errorf("write Mind ready message: %w", err)
	}
	return nil
}
