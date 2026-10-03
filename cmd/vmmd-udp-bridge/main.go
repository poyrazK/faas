// Command vmmd-udp-bridge owns one connected IPv4 UDP socket inside the
// instance network namespace. Its stdin/stdout use udpwire datagram frames.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/udpwire"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: vmmd-udp-bridge <guest-ip> <port>")
		os.Exit(2)
	}
	ip := net.ParseIP(os.Args[1])
	port, err := strconv.ParseUint(os.Args[2], 10, 16)
	if ip == nil || ip.To4() == nil || err != nil || port == 0 {
		fmt.Fprintln(os.Stderr, "invalid IPv4 guest address or port")
		os.Exit(2)
	}
	if !validReadyDescriptor() {
		fmt.Fprintln(os.Stderr, "readiness descriptor must be a writable pipe")
		os.Exit(4)
	}
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: ip.To4(), Port: int(port)})
	if err != nil {
		_ = writeReady("ERR " + err.Error() + "\n")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	if err := writeReady("OK\n"); err != nil {
		_ = conn.Close()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	if err := udpwire.Bridge(context.Background(), conn, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
}

func writeReady(message string) error {
	ready := os.NewFile(3, "ready")
	if ready == nil {
		return fmt.Errorf("missing readiness descriptor")
	}
	defer func() { _ = ready.Close() }()
	_, err := ready.WriteString(message)
	return err
}

// Check FD 3 before opening the UDP socket, so a missing readiness pipe
// cannot be mistaken for a newly allocated guest socket or another file.
func validReadyDescriptor() bool {
	var info unix.Stat_t
	if unix.Fstat(3, &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFIFO {
		return false
	}
	flags, err := unix.FcntlInt(3, unix.F_GETFL, 0)
	return err == nil && flags&unix.O_ACCMODE != unix.O_RDONLY
}
