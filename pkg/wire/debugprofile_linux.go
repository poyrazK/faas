//go:build linux

package wire

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"syscall"
)

// listenDebugProfile listens on the abstract socket @faas-debug/<name>, or
// @faas-debug/<name>.<pid> while another process of the same daemon holds
// the name (the outgoing generation of a two-generation reload). Only root
// and this process's own uid may connect.
func listenDebugProfile(name string, log *slog.Logger) (net.Listener, string, error) {
	if !validDebugProfileName(name) {
		return nil, "", fmt.Errorf("invalid daemon name %q for a debug socket", name)
	}
	addr := DebugProfileSocketPrefix + name
	ln, err := net.Listen("unix", "@"+addr)
	if errors.Is(err, syscall.EADDRINUSE) {
		addr = fmt.Sprintf("%s%s.%d", DebugProfileSocketPrefix, name, os.Getpid())
		ln, err = net.Listen("unix", "@"+addr)
	}
	if err != nil {
		return nil, "", fmt.Errorf("listen @%s: %w", addr, err)
	}
	allowed := map[uint32]bool{0: true, uint32(os.Getuid()): true}
	return &peerCredListener{Listener: ln, allowed: allowed, log: log, daemon: name}, addr, nil
}

// peerCredListener closes every connection whose peer is not an allowed uid
// before any byte is read. Abstract sockets have no file permissions, so this
// check is the access control.
type peerCredListener struct {
	net.Listener
	allowed map[uint32]bool
	log     *slog.Logger
	daemon  string
}

func (l *peerCredListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(conn)
		if err == nil && l.allowed[uid] {
			return conn, nil
		}
		if err != nil {
			l.log.Warn("debug profiling: refused a connection without peer credentials", "daemon", l.daemon, "err", err)
		} else {
			l.log.Warn("debug profiling: refused a connection", "daemon", l.daemon, "peer_uid", uid)
		}
		_ = conn.Close()
	}
}

func peerUID(conn net.Conn) (uint32, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("debug profiling: raw conn: %w", err)
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("debug profiling: peer credentials: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("debug profiling: peer credentials: %w", credErr)
	}
	return cred.Uid, nil
}
