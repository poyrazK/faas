//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func init() {
	debugPprofDial = debugPprofDialVerified
}

// debugPprofDialVerified dials the daemon's abstract socket and checks the
// listener's SO_PEERCRED uid before any request is sent.
func debugPprofDialVerified(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", "@"+addr)
	if err != nil {
		return nil, err
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("@%s: not a unix connection", addr)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("@%s: raw conn: %w", addr, err)
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || credErr != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("@%s: peer credentials: %w", addr, errors.Join(err, credErr))
	}
	if !debugPprofServerAllowed(cred.Uid, addr) {
		_ = conn.Close()
		return nil, debugPprofRefusedServer(addr, cred.Uid)
	}
	return conn, nil
}
