// Command vmmd-tcp-bridge owns one protocol-neutral TCP socket inside a
// Firecracker instance's network namespace. vmmd starts it with nsenter and
// connects its stdin/stdout to the ForwardTCPStream gRPC handler.
//
// Unlike vmmd-raw-bridge, this helper never parses or manufactures HTTP
// headers. stdin/stdout are application bytes and EOF is propagated as a TCP
// half-close so PostgreSQL, SSH, MQTT, and other bidirectional protocols keep
// their wire format intact.
package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

const (
	dialTimeout = 30 * time.Second
	readyFD     = 3
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: vmmd-tcp-bridge <guest-ip> <port>")
		os.Exit(2)
	}
	port, err := strconv.ParseUint(os.Args[2], 10, 16)
	if err != nil || port == 0 {
		fmt.Fprintf(os.Stderr, "invalid port %q\n", os.Args[2])
		os.Exit(2)
	}

	conn, err := (&net.Dialer{Timeout: dialTimeout}).Dial(
		"tcp", net.JoinHostPort(os.Args[1], strconv.FormatUint(port, 10)))
	if err != nil {
		writeReady("ERR " + err.Error() + "\n")
		fmt.Fprintf(os.Stderr, "dial guest %s:%d: %v\n", os.Args[1], port, err)
		os.Exit(3)
	}
	defer conn.Close()
	if err := writeReady("OK\n"); err != nil {
		fmt.Fprintf(os.Stderr, "write readiness: %v\n", err)
		os.Exit(4)
	}

	if err := bridge(conn, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "bridge: %v\n", err)
		os.Exit(4)
	}
}

func writeReady(message string) error {
	f := os.NewFile(uintptr(readyFD), "ready")
	if f == nil {
		return errors.New("readiness fd is unavailable")
	}
	defer f.Close()
	_, err := io.WriteString(f, message)
	return err
}

// bridge copies both directions concurrently. A clean EOF from stdin is
// translated to CloseWrite instead of closing the whole socket; the guest may
// still have response bytes to send. The opposite direction follows the same
// rule through the process' stdout EOF.
func bridge(conn net.Conn, input io.Reader, output io.Writer) error {
	readDone := make(chan error, 1)
	writeDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(output, conn)
		readDone <- err
	}()
	go func() {
		_, err := io.Copy(conn, input)
		if err == nil {
			if tcp, ok := conn.(*net.TCPConn); ok {
				err = tcp.CloseWrite()
			}
		}
		writeDone <- err
	}()

	firstRead, firstWrite := false, false
	var readErr, writeErr error
	for !(firstRead && firstWrite) {
		select {
		case readErr = <-readDone:
			firstRead = true
		case writeErr = <-writeDone:
			firstWrite = true
		}
		if firstRead && !firstWrite {
			_ = conn.Close()
		}
		if firstWrite && !firstRead && writeErr != nil {
			_ = conn.Close()
		}
	}

	if !cleanClose(readErr) {
		return readErr
	}
	if !cleanClose(writeErr) {
		return writeErr
	}
	return nil
}

func cleanClose(err error) bool {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	return false
}
