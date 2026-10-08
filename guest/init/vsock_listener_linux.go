//go:build linux

package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// net.FileListener cannot decode AF_VSOCK addresses. Use a nonblocking file
// descriptor with Go's poller, as the runtime-config vsock connection does.
type guestVsockListener struct {
	socket *os.File
	addr   runtimeConfigVsockAddr
	closed atomic.Bool
}

func listenGuestVsock(port uint32) (net.Listener, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("vsock bind port %d: %w", port, err)
	}
	if err := unix.Listen(fd, 1); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("vsock listen: %w", err)
	}
	addr := runtimeConfigVsockAddr{unix.VMADDR_CID_ANY, port}
	if local, err := unix.Getsockname(fd); err == nil {
		if local, ok := local.(*unix.SockaddrVM); ok {
			addr = runtimeConfigVsockAddr{local.CID, local.Port}
		}
	}
	return &guestVsockListener{socket: os.NewFile(uintptr(fd), "guest-vsock-listener"), addr: addr}, nil
}

func (l *guestVsockListener) Accept() (net.Conn, error) {
	if l.closed.Load() {
		return nil, net.ErrClosed
	}
	raw, err := l.socket.SyscallConn()
	if err != nil {
		return nil, l.acceptError(err)
	}
	accepted := -1
	var peer unix.Sockaddr
	var acceptErr error
	err = raw.Read(func(fd uintptr) bool {
		for {
			accepted, peer, acceptErr = unix.Accept4(int(fd), unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK)
			if errors.Is(acceptErr, unix.EINTR) || errors.Is(acceptErr, unix.ECONNABORTED) {
				continue
			}
			return !errors.Is(acceptErr, unix.EAGAIN)
		}
	})
	if err != nil || acceptErr != nil {
		if accepted >= 0 {
			_ = unix.Close(accepted)
		}
		return nil, l.acceptError(errors.Join(err, acceptErr))
	}
	remote := runtimeConfigVsockAddr{unix.VMADDR_CID_ANY, 0}
	if peer, ok := peer.(*unix.SockaddrVM); ok {
		remote = runtimeConfigVsockAddr{peer.CID, peer.Port}
	}
	return &runtimeConfigVsockConn{
		File: os.NewFile(uintptr(accepted), "guest-vsock-connection"), local: l.addr, remote: remote,
	}, nil
}

func (l *guestVsockListener) acceptError(err error) error {
	if l.closed.Load() || errors.Is(err, os.ErrClosed) {
		return net.ErrClosed
	}
	return fmt.Errorf("vsock accept: %w", err)
}

func (l *guestVsockListener) Close() error {
	if l.closed.Swap(true) {
		return net.ErrClosed
	}
	return l.socket.Close()
}

func (l *guestVsockListener) Addr() net.Addr { return l.addr }
