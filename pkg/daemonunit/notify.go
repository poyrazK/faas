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
	"strconv"
	"strings"
	"sync"
	"time"
)

// WatchdogFromEnv starts the sd_watchdog heartbeat when systemd asked
// for one (WATCHDOG_USEC set, and WATCHDOG_PID absent or equal to this
// process). It sends WATCHDOG=1 every half interval while healthy
// reports true and stays silent otherwise, which is the whole
// mechanism: a daemon whose main loop is stalled stops pinging and
// systemd restarts it after WatchdogSec (ADR-190). Without the env
// (no unit, tests, local runs) it is a no-op. The returned stop func
// halts the heartbeat; cancelling ctx does the same.
func WatchdogFromEnv(ctx context.Context, healthy func() bool) func() {
	interval, ok := watchdogInterval(os.Getenv)
	if !ok || healthy == nil {
		return func() {}
	}
	return runWatchdog(ctx, interval/2, healthy, Notify)
}

// watchdogInterval parses the systemd watchdog contract from the
// environment. Returns ok=false when systemd did not request a
// watchdog or when WATCHDOG_PID names a different process (the env
// was inherited by a child that must not answer for its parent).
func watchdogInterval(getenv func(string) string) (time.Duration, bool) {
	raw := getenv("WATCHDOG_USEC")
	if raw == "" {
		return 0, false
	}
	usec, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || usec <= 0 {
		return 0, false
	}
	if pid := getenv("WATCHDOG_PID"); pid != "" && pid != strconv.Itoa(os.Getpid()) {
		return 0, false
	}
	return time.Duration(usec) * time.Microsecond, true
}

// runWatchdog is the testable core of WatchdogFromEnv.
func runWatchdog(ctx context.Context, every time.Duration, healthy func() bool, notify func(string) error) func() {
	if every <= 0 {
		every = time.Second
	}
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if healthy() {
					_ = notify("WATCHDOG=1")
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}

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
	defer func() { _ = conn.Close() }()
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
