//go:build metal

// adr: 906 — on a host with memory-only swap (the zswap role) a running VM
// never swaps, a frozen VM's memory compresses into zswap on request, and
// the thawed VM serves again with swap closed.
package fcvm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func readScopeFile(t *testing.T, scope, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(scope, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return strings.TrimSpace(string(raw))
}

// TestMetalFrozenCompress cold-boots a guest, whose memory is all anonymous
// on the host, freezes it, compresses it and thaws it.
func TestMetalFrozenCompress(t *testing.T) {
	kernel, base, layer := metalImages(t)
	ctx := context.Background()
	fcVersion, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 30*time.Second), Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	if parent := ParentCgroupFor("scale"); !frozenSwapReady(parent) {
		t.Skipf("no memory-only swap for %s: zswap on, a swap device and memory.zswap.writeback=0 on an ancestor are required (the zswap Ansible role)", parent)
	}

	const id = "compress"
	inst, err := m.ColdBoot(ctx, ColdBootRequest{Instance: id, Plan: "scale", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 256})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
	url := "http://" + inst.Lease.HostIP.String() + ":8080/"
	if _, err := firstByte(url, 10*time.Second); err != nil {
		t.Fatalf("guest does not serve: %v", err)
	}
	scope := vmCgroupScope(inst.Lease)
	if got := readScopeFile(t, scope, "memory.swap.max"); got != swapClosed {
		t.Fatalf("running VM memory.swap.max = %q, want %q", got, swapClosed)
	}

	if _, err := m.Freeze(ctx, id, FreezeOptions{}); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if got := readScopeFile(t, scope, "memory.swap.max"); got != swapOpen {
		t.Fatalf("frozen VM memory.swap.max = %q, want %q", got, swapOpen)
	}
	rep, err := m.Freeze(ctx, id, FreezeOptions{Reclaim: true})
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	zswapped := readScopeFile(t, scope, "memory.zswap.current")
	t.Logf("compress: %d MiB -> %d MiB resident, %d MiB swapped, zswap pool %s bytes, %s",
		rep.BeforeBytes>>20, rep.AfterBytes>>20, rep.SwappedBytes>>20, zswapped, rep.Reclaim)
	if rep.SwappedBytes <= 0 || rep.AfterBytes*2 > rep.BeforeBytes || zswapped == "0" {
		t.Fatalf("compression did not move the frozen guest into zswap: %+v, zswap %s", rep, zswapped)
	}

	started := time.Now()
	if err := m.Thaw(ctx, id); err != nil {
		t.Fatalf("thaw: %v", err)
	}
	if got := readScopeFile(t, scope, "memory.swap.max"); got != swapClosed {
		t.Fatalf("thawed VM memory.swap.max = %q, want %q", got, swapClosed)
	}
	if _, err := firstByte(url, 10*time.Second); err != nil {
		t.Fatalf("thawed guest does not serve: %v", err)
	}
	t.Logf("compressed thaw to first byte: %s", time.Since(started))
}
