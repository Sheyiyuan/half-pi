//go:build linux

package adminipc

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
)

func verifyPeer(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("not a Unix socket")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return err
	}
	var (
		cred    *syscall.Ucred
		callErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, callErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if callErr != nil {
		return callErr
	}
	current := uint32(os.Getuid())
	if parsed, err := strconv.ParseUint(os.Getenv("SUDO_UID"), 10, 32); err == nil {
		current = uint32(parsed)
	}
	if cred == nil || cred.Uid != current {
		return fmt.Errorf("peer uid mismatch")
	}
	return nil
}
