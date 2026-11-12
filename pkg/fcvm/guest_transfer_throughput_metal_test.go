//go:build metal

// adr: 20261108170000 — measures the guest-to-host transfer cost of a VMM
// that offers no checksum/segmentation offload. Run it once per Firecracker
// binary (PATH decides which) and compare the logged numbers.

package fcvm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

// throughputGuestScript serves a 64 MiB file and a small file from tmpfs.
// /ready appears last so the host never times a partially written file.
const throughputGuestScript = `#!/bin/sh
set +e
busybox mount -t tmpfs -o mode=1777,size=96m tmpfs /tmp
mkdir -p /tmp/www
busybox dd if=/dev/zero of=/tmp/www/big bs=1M count=64 2>/dev/null
busybox echo small > /tmp/www/small
busybox echo ok > /tmp/www/ready
exec busybox httpd -f -p 8080 -h /tmp/www
`

// TestMetalGuestTransferThroughput boots an uncapped guest and logs
// guest-to-host bulk throughput (64 MiB GETs, the guest transmit path that
// loses TSO) and small-request latency (what functions and web apps mostly
// see). Opt-in: FAAS_TEST_NET_THROUGHPUT=1.
func TestMetalGuestTransferThroughput(t *testing.T) {
	if os.Getenv("FAAS_TEST_NET_THROUGHPUT") != "1" {
		t.Skip("opt-in: set FAAS_TEST_NET_THROUGHPUT=1")
	}
	kernel, _, _ := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	img := ensureEgressGuestExt4WithScript(t, t.TempDir(), throughputGuestScript)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	inst, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   "netbench",
		Plan:       "scale",
		BaseKey:    img,
		LayerKey:   img,
		VcpuCount:  2,
		MemSizeMiB: 256,
		EgressMbit: 0, // uncapped: measure the VMM path, not the plan shaper
	})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() {
		teardownCtx, teardownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer teardownCancel()
		if err := m.Destroy(teardownCtx, "netbench"); err != nil {
			t.Errorf("destroy cleanup: %v", err)
		}
		leakcheck.AssertZero(t)
	})
	base := fmt.Sprintf("http://%s:8080", inst.Lease.HostIP)
	client := &http.Client{Timeout: 60 * time.Second}

	get := func(path string) (int64, error) {
		resp, err := client.Get(base + path)
		if err != nil {
			return 0, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
		}
		return io.Copy(io.Discard, resp.Body)
	}

	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := get("/ready"); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("guest never served /ready: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	const bulkRuns = 5
	rates := make([]float64, 0, bulkRuns)
	for i := 0; i < bulkRuns; i++ {
		start := time.Now()
		n, err := get("/big")
		if err != nil {
			t.Fatalf("bulk %d: %v", i, err)
		}
		if n != 64<<20 {
			t.Fatalf("bulk %d: got %d bytes", i, n)
		}
		rates = append(rates, float64(n)/(1<<20)/time.Since(start).Seconds())
	}
	sort.Float64s(rates)

	const smallRuns = 300
	lat := make([]time.Duration, 0, smallRuns)
	for i := 0; i < smallRuns; i++ {
		start := time.Now()
		if _, err := get("/small"); err != nil {
			t.Fatalf("small %d: %v", i, err)
		}
		lat = append(lat, time.Since(start))
	}
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

	t.Logf("netbench bulk_mib_s median=%.1f min=%.1f max=%.1f small_ms p50=%.3f p90=%.3f p99=%.3f",
		rates[bulkRuns/2], rates[0], rates[bulkRuns-1],
		ms(lat[smallRuns/2]), ms(lat[smallRuns*9/10]), ms(lat[smallRuns*99/100]))
}
