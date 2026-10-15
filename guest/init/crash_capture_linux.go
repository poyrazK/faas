//go:build linux

package main

import (
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/crashcapturewire"
)

func init() { crashCaptureDial = dialCrashCaptureHost }

// dialCrashCaptureHost uses the runtime config dialer: net.FileConn does not
// support AF_VSOCK, so a plain socket + FileConn never connects.
func dialCrashCaptureHost() (net.Conn, error) {
	return dialRuntimeConfigVsock(crashcapturewire.Port, 4*time.Second)
}
