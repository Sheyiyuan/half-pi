// Package adminipc implements Mind's local management IPC.
package adminipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/management"
)

const (
	Version        = 1
	MaxMessageSize = 1 << 20
	ioTimeout      = 5 * time.Second
)

// Request 是管理 IPC v1 请求。
type Request struct {
	Version   int             `json:"version"`
	RequestID string          `json:"request_id"`
	Operation string          `json:"operation"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// Response 是管理 IPC v1 响应。
type Response struct {
	Version   int            `json:"version"`
	RequestID string         `json:"request_id"`
	OK        bool           `json:"ok"`
	Result    any            `json:"result,omitempty"`
	Error     *ErrorResponse `json:"error,omitempty"`
}

// ErrorResponse 是稳定机器可读错误。
type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// UnavailableError 表示管理 IPC 端点不可达。
type UnavailableError struct {
	Err      error
	Fallback bool
}

func (e *UnavailableError) Error() string { return e.Err.Error() }
func (e *UnavailableError) Unwrap() error { return e.Err }

// Server 是本地管理 IPC server。
type Server struct {
	listener  net.Listener
	service   *management.Service
	done      chan struct{}
	sem       chan struct{}
	requests  sync.WaitGroup
	closeOnce sync.Once
}

// Start 启动本地管理 IPC listener。
func Start(endpoint string, service *management.Service) (*Server, error) {
	listener, err := listen(endpoint)
	if err != nil {
		return nil, err
	}
	server := &Server{listener: listener, service: service, done: make(chan struct{}), sem: make(chan struct{}, 32)}
	go server.serve()
	return server, nil
}

// Close 停止 IPC listener。
func (s *Server) Close() error {
	if s == nil || s.listener == nil {
		return nil
	}
	var err error
	s.closeOnce.Do(func() {
		err = s.listener.Close()
		<-s.done
		s.requests.Wait()
		cleanup(s.listener.Addr().String())
	})
	return err
}

// Call 发起一次本地管理 IPC 请求。
func Call(ctx context.Context, endpoint string, req Request) (Response, error) {
	if err := validateRequest(req); err != nil {
		return Response{}, fmt.Errorf("validate IPC request: %w", err)
	}
	conn, err := dial(ctx, endpoint)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	deadline := time.Now().Add(ioTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = conn.SetDeadline(deadline)
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, fmt.Errorf("write IPC request: %w", err)
	}
	raw, err := readJSONLine(conn)
	if err != nil {
		return Response{}, fmt.Errorf("read IPC response: %w", err)
	}
	var wire struct {
		Version   int             `json:"version"`
		RequestID string          `json:"request_id"`
		OK        bool            `json:"ok"`
		Result    json.RawMessage `json:"result,omitempty"`
		Error     *ErrorResponse  `json:"error,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return Response{}, fmt.Errorf("decode IPC response: %w", err)
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return Response{}, fmt.Errorf("IPC response has trailing data")
	}
	if wire.Version != Version {
		return Response{}, fmt.Errorf("IPC response version mismatch")
	}
	if wire.RequestID != req.RequestID {
		return Response{}, fmt.Errorf("IPC response request_id mismatch")
	}
	if wire.OK {
		if wire.Error != nil || len(wire.Result) == 0 {
			return Response{}, fmt.Errorf("invalid successful IPC response")
		}
	} else if wire.Error == nil || wire.Error.Code == "" || wire.Error.Message == "" || len(wire.Result) != 0 {
		return Response{}, fmt.Errorf("invalid failed IPC response")
	}
	resp := Response{Version: wire.Version, RequestID: wire.RequestID, OK: wire.OK, Error: wire.Error}
	if len(wire.Result) > 0 {
		if err := json.Unmarshal(wire.Result, &resp.Result); err != nil {
			return Response{}, fmt.Errorf("decode IPC result: %w", err)
		}
	}
	return resp, nil
}

func (s *Server) serve() {
	defer close(s.done)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		select {
		case s.sem <- struct{}{}:
			s.requests.Add(1)
			go func() {
				defer s.requests.Done()
				defer func() { <-s.sem }()
				s.handle(conn)
			}()
		default:
			_ = conn.Close()
		}
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(ioTimeout))
	if err := verifyPeer(conn); err != nil {
		writeResponse(conn, Response{Version: Version, OK: false, Error: errorResponse("unauthorized", "unauthorized")})
		return
	}
	raw, err := readCapped(conn)
	if err != nil {
		writeResponse(conn, Response{Version: Version, OK: false, Error: errorResponse("invalid_request", err.Error())})
		return
	}
	req, err := decodeRequest(raw)
	if err != nil {
		writeResponse(conn, Response{Version: Version, RequestID: req.RequestID, OK: false, Error: errorResponse("invalid_request", err.Error())})
		return
	}
	result, err := s.dispatch(req)
	if err != nil {
		var managed *management.Error
		if errors.As(err, &managed) {
			writeResponse(conn, Response{Version: Version, RequestID: req.RequestID, OK: false, Error: errorResponse(managed.Code, managed.Error())})
		} else {
			writeResponse(conn, Response{Version: Version, RequestID: req.RequestID, OK: false, Error: errorResponse("internal", err.Error())})
		}
		return
	}
	writeResponse(conn, Response{Version: Version, RequestID: req.RequestID, OK: true, Result: result})
}

func readCapped(conn net.Conn) ([]byte, error) {
	raw, err := readJSONLine(conn)
	if err != nil {
		return nil, fmt.Errorf("read IPC request: %w", err)
	}
	return raw, nil
}

func readJSONLine(r io.Reader) ([]byte, error) {
	limited := io.LimitReader(r, MaxMessageSize+1)
	reader := bufio.NewReader(limited)
	raw, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxMessageSize {
		return nil, fmt.Errorf("IPC message too large")
	}
	if reader.Buffered() > 0 {
		return nil, fmt.Errorf("IPC connection contains trailing data")
	}
	return raw, nil
}

func decodeRequest(raw []byte) (Request, error) {
	var req Request
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return req, fmt.Errorf("IPC request has trailing data")
	}
	return req, validateRequest(req)
}

func writeResponse(conn net.Conn, resp Response) {
	payload, err := json.Marshal(resp)
	if err != nil || len(payload)+1 > MaxMessageSize {
		message := "encode IPC response"
		if err == nil {
			message = "IPC response too large"
		}
		payload, _ = json.Marshal(Response{
			Version: Version, RequestID: resp.RequestID, OK: false, Error: errorResponse("internal", message),
		})
	}
	payload = append(payload, '\n')
	_, _ = conn.Write(payload)
}

func decodeParams[T any](raw json.RawMessage) (T, error) {
	var params T
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	if raw[0] != '{' {
		return params, managementError("invalid_argument", "params must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&params); err != nil {
		return params, managementError("invalid_argument", fmt.Sprintf("invalid params: %v", err))
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return params, managementError("invalid_argument", "params has trailing data")
	}
	return params, nil
}

func validateRequest(req Request) error {
	if req.Version != Version {
		return fmt.Errorf("unsupported IPC version")
	}
	if _, err := uuid.Parse(req.RequestID); err != nil {
		return fmt.Errorf("invalid request_id")
	}
	if req.Operation == "" {
		return fmt.Errorf("operation is required")
	}
	return nil
}

func errorResponse(code, message string) *ErrorResponse {
	return &ErrorResponse{Code: code, Message: message, Retryable: false}
}
