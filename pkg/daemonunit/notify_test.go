package daemonunit

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

func TestNotifyRejectsUnreachableSocket(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	if err := Notify("READY=1"); err == nil {
		t.Fatal("Notify() returned nil for an unreachable configured socket")
	}
}
