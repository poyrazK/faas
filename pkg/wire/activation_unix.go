//go:build unix

package wire

import (
	"fmt"
	"net"
	"strconv"
	"syscall"
)

// inspectInheritedFD identifies one inherited stream socket by its bound
// address and marks it close-on-exec, as sd_listen_fds(3) does, so a helper
// process cannot keep the listener alive past this daemon.
func inspectInheritedFD(fd int) (network, addr string, err error) {
	sockType, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE)
	if err != nil {
		return "", "", err
	}
	if sockType != syscall.SOCK_STREAM {
		return "", "", fmt.Errorf("socket type %d is not a stream listener", sockType)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		return "", "", err
	}
	syscall.CloseOnExec(fd)
	switch a := sa.(type) {
	case *syscall.SockaddrUnix:
		if a.Name == "" || a.Name[0] == '@' {
			return "", "", fmt.Errorf("unnamed or abstract unix socket %q", a.Name)
		}
		return "unix", a.Name, nil
	case *syscall.SockaddrInet4:
		return "tcp", net.JoinHostPort(net.IP(a.Addr[:]).String(), strconv.Itoa(a.Port)), nil
	case *syscall.SockaddrInet6:
		return "tcp", net.JoinHostPort(net.IP(a.Addr[:]).String(), strconv.Itoa(a.Port)), nil
	}
	return "", "", fmt.Errorf("unsupported socket address %T", sa)
}
