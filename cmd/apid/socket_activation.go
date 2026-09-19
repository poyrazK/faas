package main

import (
	"errors"
	"fmt"
	"net"
	"os"
)

// apidListener consumes the socket owned by faas-apid.socket when systemd
// starts the production service. Keeping the listening socket outside the
// process lets TCP handshakes queue while the old apid exits and its
// replacement starts. Local and test runs retain the ordinary net.Listen
// path when no activation environment is present.
func apidListener(network, addr string) (net.Listener, error) {
	pidValue, hasPID := os.LookupEnv("LISTEN_PID")
	fdsValue, hasFDs := os.LookupEnv("LISTEN_FDS")
	if !hasPID && !hasFDs {
		return net.Listen(network, addr)
	}
	if !hasPID || !hasFDs {
		return nil, errors.New("apid: incomplete systemd socket activation environment")
	}
	if pidValue != fmt.Sprint(os.Getpid()) {
		// A descriptor advertised for an ancestor does not belong to this
		// process. Ignore it exactly as the systemd activation contract asks.
		return net.Listen(network, addr)
	}
	if network != "tcp" {
		return nil, fmt.Errorf("apid: activated listener network %q, want tcp", network)
	}
	if fdsValue != "1" {
		return nil, fmt.Errorf("apid: LISTEN_FDS=%s, want exactly 1", fdsValue)
	}
	if names := os.Getenv("LISTEN_FDNAMES"); names != "" && names != "api" {
		return nil, fmt.Errorf("apid: LISTEN_FDNAMES=%q, want api", names)
	}

	file := os.NewFile(uintptr(3), "faas-apid.socket")
	if file == nil {
		return nil, errors.New("apid: systemd listener fd 3 is unavailable")
	}
	listener, err := activatedAPIDListener(file, addr)
	_ = file.Close()
	if err != nil {
		return nil, err
	}
	// The listener returned by FileListener is close-on-exec. Clear the
	// activation variables as well so helpers cannot mistake fd 3 for theirs.
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	_ = os.Unsetenv("LISTEN_FDNAMES")
	return listener, nil
}

func activatedAPIDListener(file *os.File, addr string) (net.Listener, error) {
	listener, err := net.FileListener(file)
	if err != nil {
		return nil, fmt.Errorf("apid: consume systemd listener: %w", err)
	}
	want, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("apid: resolve configured listener: %w", err)
	}
	got, ok := listener.Addr().(*net.TCPAddr)
	if !ok || got.Port != want.Port || !got.IP.Equal(want.IP) {
		_ = listener.Close()
		return nil, fmt.Errorf("apid: activated listener is %s, want %s", listener.Addr(), want)
	}
	return listener, nil
}
