package daemonunit

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotifyNoSocketIsNoop(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if err := Notify("READY=1"); err != nil {
		t.Fatalf("Notify without systemd socket: %v", err)
	}
}

func TestNotifyReadyWhenSendsReadyAndStopping(t *testing.T) {
	path := fmt.Sprintf("/tmp/daemonunit-notify-%d.sock", time.Now().UnixNano())
	defer os.Remove(path)
	addr := &net.UnixAddr{Name: path, Net: "unixgram"}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	t.Setenv("NOTIFY_SOCKET", path)

	ctx, cancel := context.WithCancel(context.Background())
	stop := NotifyReadyWhen(ctx, func() bool { return true })
	defer stop()

	buf := make([]byte, 64)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := conn.ReadFromUnix(buf)
	if err != nil {
		t.Fatalf("read READY=1: %v", err)
	}
	if got := string(buf[:n]); got != "READY=1" {
		t.Fatalf("first notification = %q, want READY=1", got)
	}
	cancel()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err = conn.ReadFromUnix(buf)
	if err != nil {
		t.Fatalf("read STOPPING=1: %v", err)
	}
	if got := string(buf[:n]); got != "STOPPING=1" {
		t.Fatalf("second notification = %q, want STOPPING=1", got)
	}
}

func TestWatchdogIntervalParsesSystemdContract(t *testing.T) {
	self := strconv.Itoa(os.Getpid())
	cases := []struct {
		name string
		env  map[string]string
		want time.Duration
		ok   bool
	}{
		{name: "unset", env: map[string]string{}, ok: false},
		{name: "usec only", env: map[string]string{"WATCHDOG_USEC": "60000000"}, want: 60 * time.Second, ok: true},
		{name: "pid matches", env: map[string]string{"WATCHDOG_USEC": "1000000", "WATCHDOG_PID": self}, want: time.Second, ok: true},
		{name: "pid is another process", env: map[string]string{"WATCHDOG_USEC": "1000000", "WATCHDOG_PID": "1"}, ok: false},
		{name: "garbage", env: map[string]string{"WATCHDOG_USEC": "soon"}, ok: false},
		{name: "zero", env: map[string]string{"WATCHDOG_USEC": "0"}, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := watchdogInterval(func(k string) string { return tc.env[k] })
			if ok != tc.ok || got != tc.want {
				t.Fatalf("got (%s,%v) want (%s,%v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestWatchdogPingsOnlyWhenHealthy(t *testing.T) {
	var mu sync.Mutex
	var pings int
	notify := func(state string) error {
		if state != "WATCHDOG=1" {
			t.Errorf("unexpected state %q", state)
		}
		mu.Lock()
		pings++
		mu.Unlock()
		return nil
	}
	var healthy atomic.Bool
	healthy.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := runWatchdog(ctx, 5*time.Millisecond, healthy.Load, notify)
	defer stop()

	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	healthyPings := pings
	mu.Unlock()
	if healthyPings == 0 {
		t.Fatal("no WATCHDOG=1 while healthy")
	}

	healthy.Store(false)
	time.Sleep(20 * time.Millisecond) // drain any in-flight tick
	mu.Lock()
	base := pings
	mu.Unlock()
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	after := pings
	mu.Unlock()
	if after != base {
		t.Fatalf("pings continued while unhealthy: %d -> %d", base, after)
	}

	stop()
	stop() // idempotent
}

func TestWatchdogFromEnvNoopWithoutSystemd(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "")
	t.Setenv("WATCHDOG_PID", "")
	stop := WatchdogFromEnv(context.Background(), func() bool { return true })
	stop()
}

func TestNotifyRejectsUnreachableSocket(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	if err := Notify("READY=1"); err == nil {
		t.Fatal("Notify() returned nil for an unreachable configured socket")
	}
}
