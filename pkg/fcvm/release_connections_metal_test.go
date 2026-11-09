//go:build metal

package fcvm

import (
	"context"
	"os"
	"testing"
	"time"
)

// adr: 20261109130000 — before destroying an app guest vmmd asks guest-init to
// reset the guest's off-guest TCP connections. The probe connects to its own
// non-loopback address (10.0.0.2), so both ends of that connection count as
// off-guest, and keeps a loopback connection that must survive. A real guest
// kernel must accept SOCK_DESTROY (CONFIG_INET_DIAG_DESTROY), and the probe
// must observe the reset.
//
//	FAAS_TEST_KERNEL=<release vmlinux> FAAS_TEST_RNG_GO_ROOTFS=/var/tmp/alpine.tar \
//	make test-metal PKGS=./pkg/fcvm RUN_REGEX=TestMetalReleaseGuestConnections
func TestMetalReleaseGuestConnections(t *testing.T) {
	tarball := compatGoRootfs(t)
	probe := compatGoProbe(t, compatConnProbe)
	for _, phase := range []string{"cold-boot", "restored"} {
		t.Run(phase, func(t *testing.T) {
			p, inst := startCompatProbe(t, "conn-"+phase, tarball, "srv/probe", probe, []string{"/srv/probe"}, map[string]string{}, nil)
			if phase == "restored" {
				p.park()
				inst = p.restore()
				// A restored guest's captured sockets are stale; open fresh ones
				// the way a woken app would.
				p.getJSON(inst, "/reconnect")
			}
			before := p.getJSON(inst, "/state")
			if before["off_open"] != true || before["lo_open"] != true {
				t.Fatalf("probe connections not open before release: %v", before)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			n, err := p.vmm.ReleaseGuestConnections(ctx, inst.Lease)
			if err != nil {
				t.Fatalf("ReleaseGuestConnections: %v", err)
			}
			// Both ends of the 10.0.0.2 connection have a non-loopback peer, but
			// resetting one closes the other in the same kernel before the
			// release reaches it, so one reset is enough for this pair. Against
			// a remote peer each socket is reset separately.
			if n == 0 && os.Getenv("FAAS_TEST_KERNEL") == "" {
				t.Skip("reset nothing: the default legacy test kernel has no TCP sock_diag; set FAAS_TEST_KERNEL to the release vmlinux")
			}
			if n < 1 {
				t.Fatalf("reset %d sockets, want the probe's off-guest connection", n)
			}
			after := p.getJSON(inst, "/state")
			t.Logf("reset %d sockets; probe before %v after %v", n, before, after)
			if after["off_open"] != false {
				t.Errorf("off-guest connection still open after release: %v", after)
			}
			if after["lo_open"] != true {
				t.Errorf("loopback connection was reset: %v", after)
			}
			// The Manager's teardown runs the same release before destroying.
			if err := p.m.Destroy(context.Background(), p.instance); err != nil {
				t.Fatalf("destroy: %v", err)
			}
		})
	}
}

// compatConnProbe holds one connection to its own 10.0.0.2 listener and one
// loopback connection, and reports on /state whether each still works: a read
// with a short deadline that times out means open, any other result closed.
const compatConnProbe = `package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	mu        sync.Mutex
	off, lo   net.Conn
	listeners []net.Listener
)

func drain(c net.Conn) {
	buf := make([]byte, 1)
	for {
		if _, err := c.Read(buf); err != nil {
			return
		}
	}
}

func pair(host string) net.Conn {
	ln, err := net.Listen("tcp4", host+":0")
	if err != nil {
		os.Exit(2)
	}
	listeners = append(listeners, ln)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go drain(c)
		}
	}()
	c, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		os.Exit(3)
	}
	return c
}

func connect() {
	mu.Lock()
	defer mu.Unlock()
	for _, c := range []net.Conn{off, lo} {
		if c != nil {
			_ = c.Close()
		}
	}
	for _, ln := range listeners {
		_ = ln.Close()
	}
	listeners = nil
	off, lo = pair("10.0.0.2"), pair("127.0.0.1")
}

func open(c net.Conn) bool {
	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, err := c.Read(make([]byte, 1))
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func main() {
	connect()
	http.HandleFunc("/reconnect", func(w http.ResponseWriter, _ *http.Request) {
		connect()
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	http.HandleFunc("/state", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"off_open": open(off), "lo_open": open(lo)})
	})
	_ = http.ListenAndServe("0.0.0.0:8080", nil)
}
`
