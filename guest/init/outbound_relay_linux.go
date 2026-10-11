//go:build linux

package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// outboundRelayHostPort mirrors fcvm.VsockOutboundRelayHostPort (ADR-981).
const outboundRelayHostPort = 1032

// startOutboundRelay exposes GREGALE_OUTBOUND_URL on loopback. It fails soft
// like the other platform proxies; vmmd closes the stream when the relay is
// not configured, so apps get a prompt connection error.
func startOutboundRelay(log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	ln, err := net.Listen("tcp4", outboundRelayListenAddr)
	if err != nil {
		return fmt.Errorf("outbound relay listen: %w", err)
	}
	go serveOutboundRelay(ln, dialOutboundRelayHost, log)
	log.Info("outbound relay started", "endpoint", OutboundRelayEndpoint)
	return nil
}

// vsockStream is a connected AF_VSOCK stream. net.FileConn cannot wrap
// AF_VSOCK, so a non-blocking *os.File gives poller-backed I/O whose Close
// also interrupts a blocked Read.
type vsockStream struct{ *os.File }

func (s vsockStream) CloseWrite() error {
	raw, err := s.SyscallConn()
	if err != nil {
		return err
	}
	var shutdownErr error
	if err := raw.Control(func(fd uintptr) { shutdownErr = unix.Shutdown(int(fd), unix.SHUT_WR) }); err != nil {
		return err
	}
	return shutdownErr
}

func dialOutboundRelayHost() (relayHostConn, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("outbound relay socket: %w", err)
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: outboundRelayHostPort}); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("outbound relay connect: %w", err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("outbound relay nonblock: %w", err)
	}
	return vsockStream{File: os.NewFile(uintptr(fd), "outbound-relay-vsock")}, nil
}
