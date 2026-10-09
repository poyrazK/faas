//go:build linux

package main

import (
	"net"
	"os"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/crashcapturewire"
)

func init() { crashCaptureDial = dialCrashCaptureHost }

func dialCrashCaptureHost() (net.Conn, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: crashcapturewire.Port}); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "crash-capture-vsock")
	conn, err := net.FileConn(file)
	_ = file.Close()
	if err != nil {
		return nil, err
	}
	return conn, nil
}
