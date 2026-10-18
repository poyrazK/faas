package main

import (
	"errors"
	"net"
	"os"
	"syscall"
	"time"
)

const workerStartupMarker = "/tmp/gregale-fixture-worker-started"

// Task guests must cold boot the pristine image without the worker entrypoint
// or its listener. The ordinary worker writes the marker before serving; a
// captured worker snapshot therefore cannot masquerade as a fresh task guest.
func assertWorkerTaskIsolation(marker, address string) error {
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		return errors.New("worker entrypoint marker present or unreadable")
	}
	conn, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return errors.New("worker listener is running in task guest")
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return errors.New("worker listener absence could not be established")
	}
	return nil
}
