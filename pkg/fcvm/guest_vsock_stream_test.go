// adr: 051
package fcvm

import (
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

func TestGuestVsockPlatformReceiversUsePerInstanceUnixSockets(t *testing.T) {
	// macOS has a 104-byte Unix-socket path limit and t.TempDir is long.
	base, err := os.MkdirTemp("/tmp", "gv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	v := NewJailerVMM(base, time.Second)
	lease := Lease{Instance: "receiver-contract", UID: os.Getuid(), GID: os.Getgid()}
	if _, err := v.mkChroot(lease.Instance); err != nil {
		t.Fatal(err)
	}

	type receipt struct {
		port     uint32
		instance string
		body     string
	}
	receipts := make(chan receipt, 3)
	var observerMu sync.Mutex
	available := map[uint32]bool{}
	v.WithGuestVsockTransportObserver(func(port uint32, kind string, err error) {
		observerMu.Lock()
		available[port] = err == nil && kind == ""
		observerMu.Unlock()
	})
	for _, port := range []uint32{VsockGuestEventHostPort, VsockWorkloadIdentityHostPort, VsockRuntimeConfigHostPort} {
		port := port
		if err := v.RegisterGuestVsockStreamHandler(port, func(instance string, conn net.Conn) (string, error) {
			body, err := io.ReadAll(conn)
			if err != nil {
				return "read", err
			}
			receipts <- receipt{port: port, instance: instance, body: string(body)}
			return "", nil
		}); err != nil {
			t.Fatalf("register port %d: %v", port, err)
		}
	}
	if err := v.prepareRegisteredGuestVsockListeners(lease); err != nil {
		t.Fatalf("prepare receivers: %v", err)
	}
	defer v.closeGuestVsockListeners(lease.Instance)

	for _, port := range []uint32{VsockGuestEventHostPort, VsockWorkloadIdentityHostPort, VsockRuntimeConfigHostPort} {
		path := v.guestVsockUDSSock(lease.Instance, port)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat port %d UDS: %v", port, err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			t.Fatalf("port %d endpoint mode = %v, want Unix socket", port, info.Mode())
		}
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatalf("dial port %d UDS: %v", port, err)
		}
		if _, err := io.WriteString(conn, "frame"); err != nil {
			t.Fatalf("write port %d UDS: %v", port, err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[uint32]bool{}
	for range 3 {
		select {
		case got := <-receipts:
			if got.instance != lease.Instance || got.body != "frame" {
				t.Fatalf("receipt = %+v", got)
			}
			seen[got.port] = true
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for guest receiver")
		}
	}
	for _, port := range []uint32{VsockGuestEventHostPort, VsockWorkloadIdentityHostPort, VsockRuntimeConfigHostPort} {
		if !seen[port] {
			t.Errorf("port %d did not receive a stream", port)
		}
		observerMu.Lock()
		up := available[port]
		observerMu.Unlock()
		if !up {
			t.Errorf("port %d was not reported available", port)
		}
	}
}

func TestGuestVsockReceiverRegistrationRejectsDuplicatePort(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	handler := func(string, net.Conn) (string, error) { return "", nil }
	if err := v.RegisterGuestVsockStreamHandler(VsockGuestEventHostPort, handler); err != nil {
		t.Fatal(err)
	}
	if err := v.RegisterGuestVsockStreamHandler(VsockGuestEventHostPort, handler); err == nil {
		t.Fatal("duplicate registration succeeded")
	}
}
