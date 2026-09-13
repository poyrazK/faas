//go:build linux

package main

import (
	"fmt"
	"io"
	"time"

	"golang.org/x/sys/unix"
)

// sendGuestEventFrame opens one Firecracker guest-initiated STREAM per event.
// Firecracker forwards it to the instance-specific host listener at
// <vsock.sock>_1027. A fresh stream gives the host an EOF frame boundary and
// avoids sharing a failed connection across independent lifecycle events.
func sendGuestEventFrame(frame []byte, timeout time.Duration) error {
	if len(frame) == 0 || len(frame) > frameworkReadyMaxStreamFrame {
		return fmt.Errorf("guest event frame length %d outside 1..%d", len(frame), frameworkReadyMaxStreamFrame)
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("guest event stream socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	tv := unix.NsecToTimeval(timeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &tv); err != nil {
		return fmt.Errorf("guest event stream send timeout: %w", err)
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: VsockFrameworkReadyPort}); err != nil {
		return fmt.Errorf("guest event stream connect: %w", err)
	}
	for len(frame) > 0 {
		n, err := unix.Write(fd, frame)
		if err != nil {
			return fmt.Errorf("guest event stream write: %w", err)
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

const frameworkReadyMaxStreamFrame = 1024
