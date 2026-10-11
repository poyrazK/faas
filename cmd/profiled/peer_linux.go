//go:build linux

package main

import (
	"net"

	"golang.org/x/sys/unix"
)

// Filesystem permissions alone cannot distinguish other daemons sharing the
// service UID. Only root vmmd may submit host-owned profiling principals.
type rootListener struct{ net.Listener }

func (l rootListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if profilePeerUID(conn, 0) {
			return conn, nil
		}
		_ = conn.Close()
	}
}

func profilePeerUID(conn net.Conn, uid uint32) bool {
	u, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return false
	}
	allowed := false
	if err := raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		allowed = err == nil && cred.Uid == uid
	}); err != nil {
		return false
	}
	return allowed
}
