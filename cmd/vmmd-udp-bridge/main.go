// Command vmmd-udp-bridge owns one connected IPv4 UDP socket inside the
// instance network namespace. Its stdin/stdout use udpwire datagram frames.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"

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
