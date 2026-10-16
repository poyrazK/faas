// adr: 1151 — a running VM never swaps; a frozen VM may use memory-only
// swap (ADR-906 decision 6), and only on a host where tenant memory cannot
// reach a disk.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// swapHost fakes the host's zswap switch, its swap device and, when
// writebackAt is set, memory.zswap.writeback=0 on that cgroup.
func swapHost(t *testing.T, enabled string, swapTotalKB int, writebackAt string) {
	t.Helper()
	dir := t.TempDir()
	zswap := filepath.Join(dir, "enabled")
	meminfo := filepath.Join(dir, "meminfo")
	if err := os.WriteFile(zswap, []byte(enabled+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meminfo, fmt.Appendf(nil, "SwapTotal: %d kB\nSwapFree: %d kB\n", swapTotalKB, swapTotalKB), 0o644); err != nil {
		t.Fatal(err)
	}
	savedZswap, savedMeminfo := zswapEnabledPath, procMeminfoPath
	zswapEnabledPath, procMeminfoPath = zswap, meminfo
	t.Cleanup(func() { zswapEnabledPath, procMeminfoPath = savedZswap, savedMeminfo })
	if writebackAt != "" {
		at := filepath.Join(cgroupRoot, writebackAt)
		if err := os.MkdirAll(at, 0o755); err != nil {
			t.Fatal(err)
		}
		writeCgroupFile(t, at, "memory.zswap.writeback", "0")
	}
}

// swapFencedVM boots the fence of an app VM whose scope has swap accounting.
func swapFencedVM(t *testing.T, l Lease) (*JailerVMM, string) {
	t.Helper()
	scope := filepath.Join(cgroupRoot, vmCgroupParent(l), PerInstanceScope(l.Instance))
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCgroupFile(t, scope, "cpu.weight", itoa(vmCPUWeight(l.Plan, l.IsBuilder)))
	writeCgroupFile(t, scope, "memory.swap.max", "max")
	v := &JailerVMM{}
	if err := v.applyPreBootCgroupFence(l, nil); err != nil {
		t.Fatalf("applyPreBootCgroupFence: %v", err)
	}
	return v, scope
}

func TestAppVMStartsWithSwapClosed(t *testing.T) {
	withFakeCgroupRoot(t)
	_, scope := swapFencedVM(t, Lease{Instance: "a", Plan: api.PlanPro, MemoryMaxMiB: 256, CPUMillicores: 1000})
	if got := readFenceFile(t, scope, "memory.swap.max"); got != swapClosed {
		t.Fatalf("memory.swap.max = %q, want %q: a running VM must not swap", got, swapClosed)
	}
}

func TestSetFrozenSwapOpensOnlyOnMemoryOnlySwap(t *testing.T) {
	for _, tc := range []struct {
		name        string
		enabled     string
		swapTotalKB int
		writeback   string
		wantFrozen  string
	}{
		{name: "memory-only swap", enabled: "Y", swapTotalKB: 1 << 20, writeback: CgroupMountRoot, wantFrozen: swapOpen},
		{name: "writeback disabled higher up", enabled: "Y", swapTotalKB: 1 << 20, writeback: "faas.slice", wantFrozen: swapOpen},
		{name: "zswap off", enabled: "N", swapTotalKB: 1 << 20, writeback: CgroupMountRoot, wantFrozen: swapClosed},
		{name: "no swap device", enabled: "Y", swapTotalKB: 0, writeback: CgroupMountRoot, wantFrozen: swapClosed},
		{name: "writeback to disk allowed", enabled: "Y", swapTotalKB: 1 << 20, wantFrozen: swapClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withFakeCgroupRoot(t)
			swapHost(t, tc.enabled, tc.swapTotalKB, tc.writeback)
			l := Lease{Instance: "a", Plan: api.PlanPro, MemoryMaxMiB: 256, CPUMillicores: 1000}
			v, scope := swapFencedVM(t, l)
			if err := v.SetFrozenSwap(l, true); err != nil {
				t.Fatal(err)
			}
			if got := readFenceFile(t, scope, "memory.swap.max"); got != tc.wantFrozen {
				t.Fatalf("frozen memory.swap.max = %q, want %q", got, tc.wantFrozen)
			}
			// A fence repair keeps the frozen value instead of closing swap.
			writeCgroupFile(t, scope, "memory.swap.max", "123")
			v.RepairCgroupFences(t.Context())
			if got := readFenceFile(t, scope, "memory.swap.max"); got != tc.wantFrozen {
				t.Fatalf("repaired memory.swap.max = %q, want %q", got, tc.wantFrozen)
			}
			if err := v.SetFrozenSwap(l, false); err != nil {
				t.Fatal(err)
			}
			if got := readFenceFile(t, scope, "memory.swap.max"); got != swapClosed {
				t.Fatalf("thawed memory.swap.max = %q, want %q", got, swapClosed)
			}
		})
	}
}

// swappingVMM records the swap limit the Manager sets around a freeze.
type swappingVMM struct {
	*freezingVMM
	ready bool
	mu    sync.Mutex
	swaps []bool
}

func (v *swappingVMM) FrozenSwapReady(Lease) bool { return v.ready }

func (v *swappingVMM) SetFrozenSwap(_ Lease, frozen bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.swaps = append(v.swaps, frozen)
	return nil
}

func (v *swappingVMM) swapList() []bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.swaps)
}

func newSwappingManager(t *testing.T, ready bool) (*Manager, *swappingVMM) {
	t.Helper()
	v := &swappingVMM{freezingVMM: &freezingVMM{fakeVMM: &fakeVMM{}}, ready: ready}
	m := newTestManager(&fakeRunner{}, v)
	if _, err := m.Wake(t.Context(), admissionWake("f")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "f") })
	return m, v
}

func TestFreezeOpensSwapAndThawClosesIt(t *testing.T) {
	m, v := newSwappingManager(t, true)
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := m.Thaw(t.Context(), "f"); err != nil {
		t.Fatal(err)
	}
	if got := v.swapList(); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("swap limit changes = %v, want open at freeze, closed before thaw", got)
	}
	// A freeze that fails after the pause closes swap before the guest runs.
	v.reclaimErr = errors.New("reclaim failed")
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{Reclaim: true}); err == nil {
		t.Fatal("freeze with a failing reclaim succeeded")
	}
	if got := v.swapList(); !slices.Equal(got, []bool{true, false, true, false}) {
		t.Fatalf("swap limit changes after a failed freeze = %v, want it closed again", got)
	}
}

// Freezing a frozen instance with reclaim compresses it on a host with
// memory-only swap, and refuses on a host without.
func TestCompressFrozenInstance(t *testing.T) {
	m, v := newSwappingManager(t, true)
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	rep, err := m.Freeze(t.Context(), "f", FreezeOptions{Reclaim: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.BeforeBytes != 200<<20 || rep.AfterBytes != 60<<20 || rep.SwappedBytes != 120<<20 {
		t.Fatalf("compress report = %+v", rep)
	}
	if calls := v.callList(); !slices.Contains(calls, "reclaim:f") || slices.Index(calls, "reclaim:f") < slices.Index(calls, "pause:f") {
		t.Fatalf("calls = %v, want a reclaim after the freeze's pause", calls)
	}
	if frozen, _ := m.FrozenState("f"); !frozen {
		t.Fatal("compression thawed the instance")
	}

	m2, _ := newSwappingManager(t, false)
	if _, err := m2.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Freeze(t.Context(), "f", FreezeOptions{Reclaim: true}); !errors.Is(err, ErrCompressUnavailable) {
		t.Fatalf("compress without memory-only swap = %v, want ErrCompressUnavailable", err)
	}
}
