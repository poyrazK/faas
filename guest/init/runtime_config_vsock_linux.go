//go:build linux

// adr:438
package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// net.FileConn supports IP and Unix sockets, not AF_VSOCK. A nonblocking
// os.File uses Go's poller directly and preserves deadlines and close wakeups.
type runtimeConfigVsockConn struct {
	*os.File
	local, remote runtimeConfigVsockAddr
}

type runtimeConfigVsockAddr struct{ cid, port uint32 }

func (a runtimeConfigVsockAddr) Network() string       { return "vsock" }
func (a runtimeConfigVsockAddr) String() string        { return fmt.Sprintf("%d:%d", a.cid, a.port) }
func (c *runtimeConfigVsockConn) LocalAddr() net.Addr  { return c.local }
func (c *runtimeConfigVsockConn) RemoteAddr() net.Addr { return c.remote }

func dialRuntimeConfigVsock(port uint32, timeout time.Duration) (net.Conn, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("runtime config vsock socket: %w", err)
	}
	remote := runtimeConfigVsockAddr{unix.VMADDR_CID_HOST, port}
	err = unix.Connect(fd, &unix.SockaddrVM{CID: remote.cid, Port: remote.port})
	if errors.Is(err, unix.EINPROGRESS) || errors.Is(err, unix.EINTR) {
		err = waitRuntimeConfigVsockConnect(fd, time.Now().Add(timeout))
	}
	if err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("runtime config vsock connect: %w", err)
	}
	local := runtimeConfigVsockAddr{unix.VMADDR_CID_ANY, 0}
	if address, err := unix.Getsockname(fd); err == nil {
		if address, ok := address.(*unix.SockaddrVM); ok {
			local = runtimeConfigVsockAddr{address.CID, address.Port}
		}
	}
	return &runtimeConfigVsockConn{File: os.NewFile(uintptr(fd), "runtime-config-vsock"), local: local, remote: remote}, nil
}

func waitRuntimeConfigVsockConnect(fd int, deadline time.Time) error {
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return os.ErrDeadlineExceeded
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
		n, err := unix.Poll(poll, int((remaining+time.Millisecond-1)/time.Millisecond))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return os.ErrDeadlineExceeded
		}
		code, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
		if err != nil {
			return err
		}
		if code != 0 {
			return unix.Errno(code)
		}
		if poll[0].Revents&unix.POLLOUT == 0 {
			return unix.ECONNABORTED
		}
		return nil
	}
}
