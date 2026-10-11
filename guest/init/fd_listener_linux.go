//go:build linux

package main

import (
	"errors"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// fdListener is a net.Listener over a raw listening socket. net.FileListener
// cannot wrap AF_VSOCK sockets: Go's syscall package does not decode vsock
// addresses, so its getsockname fails with EAFNOSUPPORT. Accepted sockets are
// made non-blocking so os.File registers them with the runtime poller and
// read/write deadlines work.
type fdListener struct {
	fd        int
	addr      net.Addr
	closeOnce sync.Once
	closeErr  error
}

func newFDListener(fd int, addr net.Addr) *fdListener {
	return &fdListener{fd: fd, addr: addr}
}

func (l *fdListener) Accept() (net.Conn, error) {
	for {
		raw, _, err := unix.Accept4(l.fd, unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.ECONNABORTED) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &fdConn{File: os.NewFile(uintptr(raw), "accepted-socket"), local: l.addr}, nil
	}
}

func (l *fdListener) Close() error {
	l.closeOnce.Do(func() { l.closeErr = unix.Close(l.fd) })
	return l.closeErr
}

func (l *fdListener) Addr() net.Addr { return l.addr }

// fdConn adapts an accepted socket file to net.Conn.
type fdConn struct {
	*os.File
	local net.Addr
}

func (c *fdConn) LocalAddr() net.Addr  { return c.local }
func (c *fdConn) RemoteAddr() net.Addr { return c.local }

func (c *fdConn) SetDeadline(t time.Time) error      { return c.File.SetDeadline(t) }
func (c *fdConn) SetReadDeadline(t time.Time) error  { return c.File.SetReadDeadline(t) }
func (c *fdConn) SetWriteDeadline(t time.Time) error { return c.File.SetWriteDeadline(t) }

// vsockAddr names a guest vsock port for logs.
type vsockAddr struct{ port uint32 }

func (a vsockAddr) Network() string { return "vsock" }
func (a vsockAddr) String() string  { return "vsock:" + strconv.FormatUint(uint64(a.port), 10) }
