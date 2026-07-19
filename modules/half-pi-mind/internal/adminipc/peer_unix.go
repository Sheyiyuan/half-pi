//go:build !windows && !linux

package adminipc

import "net"

func verifyPeer(conn net.Conn) error {
	return nil
}
