package daemonunit

// Small systemd watchdog protocol helpers. Keeping this in the unit package
// lets every daemon share the same readiness contract without importing a
// platform-specific systemd library. When NOTIFY_SOCKET is unset (local
// development, tests, containers), notifications are a no-op.

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Notify sends one sd_notify state datagram to systemd. Abstract namespace
// sockets use the systemd convention of a leading '@' in NOTIFY_SOCKET.
func Notify(state string) error {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return nil
	}
	name := socket
	if strings.HasPrefix(name, "@") {
		name = "\x00" + name[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("daemonunit: sd_notify dial: %w", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(state)); err != nil {
		return fmt.Errorf("daemonunit: sd_notify write: %w", err)
	}
	return nil
}

// NotifyReadyWhen starts a small readiness bridge for a daemon. It sends
// READY=1 exactly once after ready reports true, then sends STOPPING=1 when
// ctx is cancelled. The returned function stops the bridge for tests or
// callers that own a shorter lifecycle.
func NotifyReadyWhen(ctx context.Context, ready func() bool) func() {
	if ready == nil || os.Getenv("NOTIFY_SOCKET") == "" {
		return func() {}
	}
	stop := make(chan struct{})
	var closeOnce sync.Once
	var stopOnce sync.Once
	stopNotify := func() {
		stopOnce.Do(func() { _ = Notify("STOPPING=1") })
	}
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			if ready() {
				_ = Notify("READY=1")
				select {
				case <-stop:
				case <-ctx.Done():
				}
				stopNotify()
				return
			}
			select {
			case <-stop:
				stopNotify()
				return
			case <-ctx.Done():
				stopNotify()
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		closeOnce.Do(func() { close(stop) })
		stopNotify()
	}
}
