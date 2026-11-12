//go:build linux

package main

import (
	"errors"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// peerUID returns the uid of the process that connected, as the kernel
// recorded it at connect time.
func peerUID(conn net.Conn) (int, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return -1, errors.New("neond: not a unix socket")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var (
		cred    *unix.Ucred
		credErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if credErr != nil {
		return -1, credErr
	}
	return int(cred.Uid), nil
}
