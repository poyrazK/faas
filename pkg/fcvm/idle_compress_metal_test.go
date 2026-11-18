//go:build metal

// adr: 20261107122928 — amendment 1: where the multi-gen LRU's debugfs
// interface does not open (kernel lockdown, ADR-20261107220000), L3/L4
// compress a paused VM through its cgroup's memory.reclaim down to their
// share of its memory, and the resumed guest serves again with swap closed.
package fcvm

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalIdleCompressUnderLockdown cold-boots a guest, pauses it,
// compresses it to the L4 share without lru_gen and resumes it. On a host
// without lockdown it hides lru_gen, so the run takes the lockdown path
// either way.
func TestMetalIdleCompressUnderLockdown(t *testing.T) {
	kernel, base, layer := metalImages(t)
	ctx := context.Background()
	fcVersion, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	saved := lruGenPath
	lruGenPath = filepath.Join(t.TempDir(), "no-lru-gen")
	t.Cleanup(func() { lruGenPath = saved })
	vmm := newMetalVMM(t, 30*time.Second)
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	if parent := ParentCgroupFor("scale"); !frozenSwapReady(parent) {
		t.Skipf("no memory-only swap for %s: zswap on, a swap device and memory.zswap.writeback=0 on an ancestor are required (the zswap Ansible role)", parent)
	}
	if vmm.ColdPagesByGeneration() {
		t.Fatal("compresses by generation with lru_gen hidden")
	}

	const id = "idle-compress"
	inst, err := m.ColdBoot(ctx, ColdBootRequest{Instance: id, Plan: "scale", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 256})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
	url := "http://" + inst.Lease.HostIP.String() + ":8080/"
	if _, err := firstByte(url, 10*time.Second); err != nil {
		t.Fatalf("guest does not serve: %v", err)
	}
	if !vmm.ColdPagesReady(inst.Lease) {
		t.Fatal("cold pages not ready on a zswap host without lru_gen")
	}
	if err := vmm.PauseVM(ctx, inst.Lease); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := vmm.SetFrozenSwap(inst.Lease, true); err != nil {
		t.Fatalf("open swap: %v", err)
	}
	if err := vmm.AgeVMMemory(inst.Lease); err != nil {
		t.Fatalf("age without lru_gen: %v", err)
	}
	keep := ColdKeep{Generations: api.IdleLadderCold2KeepGenerations, Percent: api.IdleLadderCold2KeepPercent}
	r, err := vmm.EvictColdVMMemory(inst.Lease, keep, func() bool { return false })
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	scope := vmCgroupScope(inst.Lease)
	t.Logf("L4 under lockdown: %d MiB -> %d MiB resident, %d MiB swapped, zswap pool %s bytes",
		r.BeforeBytes>>20, r.AfterBytes>>20, r.SwappedBytes>>20, readScopeFile(t, scope, "memory.zswap.current"))
	if r.SwappedBytes <= 0 || r.AfterBytes*2 > r.BeforeBytes {
		t.Fatalf("compression did not move the paused guest into zswap: %+v", r)
	}

	if err := vmm.SetFrozenSwap(inst.Lease, false); err != nil {
		t.Fatalf("close swap: %v", err)
	}
	started := time.Now()
	if err := vmm.ResumeVM(ctx, inst.Lease); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := readScopeFile(t, scope, "memory.swap.max"); got != swapClosed {
		t.Fatalf("resumed VM memory.swap.max = %q, want %q", got, swapClosed)
	}
	if _, err := firstByte(url, 10*time.Second); err != nil {
		t.Fatalf("resumed guest does not serve: %v", err)
	}
	t.Logf("compressed resume to first byte: %s", time.Since(started))
}
