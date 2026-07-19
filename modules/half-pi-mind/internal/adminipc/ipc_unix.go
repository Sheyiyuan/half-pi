//go:build !windows

package adminipc

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
)

func listen(endpoint string) (net.Listener, error) {
	_ = os.Remove(endpoint)
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(endpoint, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", endpoint)
	if err != nil {
		fallback := errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
		return nil, &UnavailableError{Err: err, Fallback: fallback}
	}
	return conn, nil
}

func cleanup(endpoint string) {
	_ = os.Remove(endpoint)
}
