//go:build windows

package adminipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listen(endpoint string) (net.Listener, error) {
	sddl, err := pipeSecurityDescriptor()
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		MessageMode:        false,
		InputBufferSize:    int32(MaxMessageSize),
		OutputBufferSize:   int32(MaxMessageSize),
	})
	if err != nil {
		return nil, err
	}
	return &pipeListener{Listener: listener}, nil
}

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	conn, err := winio.DialPipeContext(ctx, endpoint)
	if err != nil {
		return nil, &UnavailableError{Err: err, Fallback: isMissingPipeError(err)}
	}
	return &pipeConn{Conn: conn}, nil
}

type pipeListener struct {
	net.Listener
}

func (l *pipeListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &pipeConn{Conn: conn}, nil
}

type pipeConn struct {
	net.Conn
}

func (c *pipeConn) Read(payload []byte) (int, error) {
	count, err := c.Conn.Read(payload)
	return count, normalizePipeError(err)
}

func (c *pipeConn) Write(payload []byte) (int, error) {
	count, err := c.Conn.Write(payload)
	return count, normalizePipeError(err)
}

func normalizePipeError(err error) error {
	if errors.Is(err, winio.ErrTimeout) {
		return os.ErrDeadlineExceeded
	}
	return err
}

func cleanup(string) {}

func isMissingPipeError(err error) bool {
	return errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}

func verifyPeer(net.Conn) error {
	return nil
}

func pipeSecurityDescriptor() (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", err
	}
	if current.Uid == "" {
		return "", fmt.Errorf("current Windows user has no SID")
	}
	return fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;%s)", current.Uid), nil
}
