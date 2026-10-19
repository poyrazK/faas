//go:build unix

package wire

import (
	"fmt"
	"net"
	"strconv"
	"syscall"
)

// inspectInheritedFD identifies one inherited socket by its bound address
// and marks it close-on-exec, as sd_listen_fds(3) does, so a helper process
// cannot keep the listener alive past this daemon. Stream sockets are
// listeners; datagram sockets are UDP packet conns.
func inspectInheritedFD(fd int) (network, addr string, err error) {
	sockType, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE)
	if err != nil {
		return "", "", err
	}
	if sockType != syscall.SOCK_STREAM && sockType != syscall.SOCK_DGRAM {
		return "", "", fmt.Errorf("socket type %d is neither a stream listener nor a datagram socket", sockType)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		return "", "", err
	}
	syscall.CloseOnExec(fd)
	switch a := sa.(type) {
	case *syscall.SockaddrUnix:
		if sockType != syscall.SOCK_STREAM {
			return "", "", fmt.Errorf("unix datagram socket %q is not supported", a.Name)
		}
		if a.Name == "" || a.Name[0] == '@' {
			return "", "", fmt.Errorf("unnamed or abstract unix socket %q", a.Name)
		}
		return "unix", a.Name, nil
	case *syscall.SockaddrInet4:
		return inetNetwork(sockType), net.JoinHostPort(net.IP(a.Addr[:]).String(), strconv.Itoa(a.Port)), nil
	case *syscall.SockaddrInet6:
		return inetNetwork(sockType), net.JoinHostPort(net.IP(a.Addr[:]).String(), strconv.Itoa(a.Port)), nil
	}
	return "", "", fmt.Errorf("unsupported socket address %T", sa)
}

func inetNetwork(sockType int) string {
	if sockType == syscall.SOCK_DGRAM {
		return "udp"
	}
	return "tcp"
}
