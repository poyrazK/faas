//go:build metal

// adr: 908 — at density, app VMs on per-account pmem bases cost the host
// less than the same VMs reading the base over virtio-blk.
package fcvm

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalPmemDensity boots FAAS_TEST_PMEM_DENSITY_VMS app VMs (default 6)
// twice: once reading the base over virtio-blk, once on per-account pmem
// copies split between two accounts. The base is a real runtime (set
// FAAS_TEST_BASE_ROOTFS to it, with FAAS_TEST_LAYER_ROOTFS starting an app
// on :8080 that answers /heavy). It reports the host memory each VM costs.
func TestMetalPmemDensity(t *testing.T) {
	kernel, base, layer := metalImages(t)
	dir := os.Getenv("FAAS_TEST_PMEM_BASE_DIR")
	if dir == "" || os.Getenv("FAAS_TEST_PMEM_DENSITY") != "1" {
		t.Skip("set FAAS_TEST_PMEM_DENSITY=1 and FAAS_TEST_PMEM_BASE_DIR (reflink filesystem beside the base)")
	}
	n := 6
	if raw := os.Getenv("FAAS_TEST_PMEM_DENSITY_VMS"); raw != "" {
		var err error
		if n, err = strconv.Atoi(raw); err != nil || n < 2 {
			t.Fatalf("FAAS_TEST_PMEM_DENSITY_VMS=%q", raw)
		}
	}
	ctx := context.Background()
	fcVersion, err := DetectFirecrackerVersion(ctx)
	if err != nil || !PmemBaseSupported(fcVersion) {
		t.Skipf("Firecracker %q without virtio-pmem (%v)", fcVersion, err)
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	for _, mode := range []string{"blk", "pmem"} {
		v := newMetalVMM(t, 180*time.Second)
		if mode == "pmem" {
			v = v.WithPmemBase(dir)
		}
		m := NewManager(wire.ExecRunner{}, v, Paths{Kernel: kernel}, fcVersion, nil, nil)
		dropPageCache(t)
		before := memAvailableMB(t)
		ids := make([]string, n)
		hosts := make([]string, n)
		for i := range ids {
			ids[i] = fmt.Sprintf("dens-%s-%d", mode, i)
			account := "acct-a"
			if i >= n/2 {
				account = "acct-b"
			}
			inst, err := m.Wake(ctx, WakeRequest{Instance: ids[i], AccountID: account, AppID: "app-" + account, Plan: "scale",
				BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 1024})
			if err != nil {
				t.Fatalf("%s wake %d: %v", mode, i, err)
			}
			hosts[i] = "http://" + inst.Lease.HostIP.String() + ":8080"
			t.Cleanup(func() { _ = m.Destroy(context.Background(), inst.Lease.Instance) })
		}
		for _, h := range hosts {
			if _, err := firstResponse(h+"/", 120*time.Second); err != nil {
				t.Fatalf("%s: %s does not serve: %v", mode, h, err)
			}
			resp, err := (&http.Client{Timeout: 60 * time.Second}).Get(h + "/heavy")
			if err != nil {
				t.Fatalf("%s: %s/heavy: %v", mode, h, err)
			}
			_ = resp.Body.Close()
		}
		time.Sleep(3 * time.Second)
		var vmMB int64
		for _, id := range ids {
			cur, err := readCgroupInt(cgroupRoot+"/"+ParentCgroupFor("scale")+"/"+PerInstanceScope(id), "memory.current")
			if err == nil {
				vmMB += cur >> 20
			}
		}
		after := memAvailableMB(t)
		t.Logf("density %s: %d VMs, host MemAvailable %d -> %d MB (%.1f MB per VM), VM cgroups %d MB (%.1f per VM)",
			mode, n, before, after, float64(before-after)/float64(n), vmMB, float64(vmMB)/float64(n))
		for _, id := range ids {
			if err := m.Destroy(ctx, id); err != nil {
				t.Errorf("%s destroy %s: %v", mode, id, err)
			}
		}
	}
}

func memAvailableMB(t *testing.T) int64 {
	t.Helper()
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemAvailable:" {
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			return kb >> 10
		}
	}
	t.Fatal("MemAvailable missing")
	return 0
}
