//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// A test-only relay uses Firecracker's guest-initiated vsock port. Production
// API routes still validate the real task capability and lease; this fixture
// does not claim to qualify public network ingress or DNS/TLS routing.
const customerFixturePort = 19041

type customerVSockConn struct{ *os.File }
type customerVSockAddr string

func (a customerVSockAddr) Network() string       { return "vsock" }
func (a customerVSockAddr) String() string        { return string(a) }
func (c *customerVSockConn) LocalAddr() net.Addr  { return customerVSockAddr("guest") }
func (c *customerVSockConn) RemoteAddr() net.Addr { return customerVSockAddr("host") }

func dialCustomerFixtureVSock(ctx context.Context, _, _ string) (net.Conn, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = unix.Close(fd)
		}
	}()
	err = unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: customerFixturePort})
	if errors.Is(err, unix.EINPROGRESS) || errors.Is(err, unix.EINTR) {
		deadline := time.Now().Add(3 * time.Second)
		if bound, exists := ctx.Deadline(); exists && bound.Before(deadline) {
			deadline = bound
		}
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !time.Now().Before(deadline) {
				return nil, os.ErrDeadlineExceeded
			}
			fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
			n, pollErr := unix.Poll(fds, 100)
			if errors.Is(pollErr, unix.EINTR) {
				continue
			}
			if pollErr != nil {
				return nil, pollErr
			}
			if n == 0 {
				continue
			}
			code, sockErr := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
			if sockErr != nil {
				return nil, sockErr
			}
			if code != 0 {
				return nil, unix.Errno(code)
			}
			if fds[0].Revents&unix.POLLOUT == 0 {
				return nil, fmt.Errorf("vsock connection unavailable")
			}
			err = nil
			break
		}
	}
	if err != nil {
		return nil, err
	}
	ok = true
	return &customerVSockConn{File: os.NewFile(uintptr(fd), "customer-operation-fixture")}, nil
}
