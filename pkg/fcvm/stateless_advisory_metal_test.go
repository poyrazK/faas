//go:build metal

// adr: 047

// stateless_advisory_metal_test.go — ADR-047 (amended 2026-11-12) metal test.
//
// Boots a real microVM whose app writes to data/ and db/ under its working
// directory, and asserts guest-init's advisory reaches the host port-1025
// listener: real inotify in the guest kernel, a real guest-initiated vsock
// stream through Firecracker, and the listener's origin naming the woken app.
// vmmd's receiver (cmd/vmmd/stateless_advisory_recv.go) decodes the same
// frame with DecodeStatelessAdvisoryFrame; its budget and audit-row shape are
// unit tested there.
//
// Fixture (FAAS_TEST_ADVISORY_WRITES=1): the base carries a guest-init built
// from this checkout plus busybox; the layer has /app and /app/data (mode
// 0777, since the app runs as the default unprivileged user) and an
// /etc/faas/app.json of
//
//	{"entrypoint":["/bin/sh","-c","(sleep 2; mkdir -p /app/db; i=0;
//	  while true; do echo $i > /app/data/app.db; echo $i > /app/db/late.db;
//	  i=$((i+1)); sleep 1; done) & exec /bin/busybox httpd -f -p 8080 -h /"],
//	 "port":8080,"working_dir":"/app"}
//
// /app/db does not exist at boot, so the test also covers a root armed by
// its parent watch.

package fcvm

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

type metalAdvisoryFrame struct {
	instance, appID string
	events          []AdvisoryEvent
}

func TestMetal_StatelessAdvisory_EndToEnd(t *testing.T) {
	kernel, base, layer := metalImages(t)
	if os.Getenv("FAAS_TEST_ADVISORY_WRITES") != "1" {
		t.Skip("set FAAS_TEST_ADVISORY_WRITES=1 with the advisory fixture described in this file")
	}
	vmm := newMetalVMM(t, 30*time.Second)
	frames := make(chan metalAdvisoryFrame, 32)
	if err := vmm.RegisterGuestVsockStreamHandler(VsockStatelessAdvisoryHostPort, func(instance string, conn net.Conn) (string, error) {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		events, err := DecodeStatelessAdvisoryFrame(conn)
		if err != nil {
			t.Errorf("advisory frame: %v", err)
			return "protocol", err
		}
		origin, _ := GuestVsockOrigin(conn)
		frames <- metalAdvisoryFrame{instance: instance, appID: origin.AppID, events: events}
		return "", nil
	}); err != nil {
		t.Fatalf("register advisory receiver: %v", err)
	}
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	const instance, appID = "metal-advisory-1", "metal-advisory-app"
	if _, err := m.Wake(ctx, WakeRequest{
		Instance: instance, AppID: appID, Plan: "hobby",
		BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 128,
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dcancel()
		_ = m.Destroy(dctx, instance)
	})

	want := map[string]bool{"/app/data/app.db": false, "/app/db/late.db": false}
	seen := map[string]int{}
	deadline := time.After(30 * time.Second)
	for !want["/app/data/app.db"] || !want["/app/db/late.db"] {
		select {
		case f := <-frames:
			if f.instance != instance || f.appID != appID {
				t.Fatalf("frame from instance=%q app=%q, want %s / %s", f.instance, f.appID, instance, appID)
			}
			for _, e := range f.events {
				seen[e.Path]++
				if _, ok := want[e.Path]; ok {
					want[e.Path] = true
				}
				t.Logf("advisory: %s %s", e.Path, strings.Join(e.Masks, ","))
			}
		case <-deadline:
			t.Fatalf("advisories within 30s of boot: %v; want /app/data/app.db and /app/db/late.db", seen)
		}
	}
	// The app rewrites both files every second; each path is reported once.
	settle := time.After(4 * time.Second)
	for {
		select {
		case f := <-frames:
			for _, e := range f.events {
				seen[e.Path]++
			}
		case <-settle:
			for p, n := range seen {
				if n != 1 {
					t.Errorf("%s reported %d times, want once per instance", p, n)
				}
			}
			return
		}
	}
}
