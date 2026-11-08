// adr: 1027
package dbsched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var bigger = Shape{VcpuCount: 2, MemMiB: 1024, VolumeBytes: 2 << 30}

// A parked database takes its new shape without a boot: the volume grows
// on its node and the record is no longer pending.
func TestResizeAParkedDatabase(t *testing.T) {
	ctx := context.Background()
	s, _, vmm := newTestScheduler(t, 4096)
	vm := provision(t, s)
	starts := vmm.callCount("start")
	got, err := s.Resize(ctx, vm.DatabaseID, bigger)
	if err != nil || got.State != StateParked || got.ResizePending || got.shape() != bigger {
		t.Fatalf("resize = %+v, %v", got, err)
	}
	if vmm.callCount("start") != starts || !strings.Contains(vmm.nodeCalls(), "grow:2147483648@db-a") {
		t.Fatalf("calls = %s, starts %d -> %d", vmm.nodeCalls(), starts, vmm.callCount("start"))
	}
	// The same shape again does nothing.
	vmm.onNode = nil
	if _, err := s.Resize(ctx, vm.DatabaseID, bigger); err != nil || vmm.nodeCalls() != "" {
		t.Fatalf("repeat = %v, calls %q", err, vmm.nodeCalls())
	}
}

// A running database restarts on the new shape, and its storage ledger
// moves to the new size.
func TestResizeRestartsARunningDatabase(t *testing.T) {
	ctx := context.Background()
	s, store, vmm := newTestScheduler(t, 4096)
	hour := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	clock := hour
	now := func() time.Time { return clock }
	store.now, s.now = now, now
	vm := provision(t, s)
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	clock = hour.Add(30 * time.Minute)
	stops := vmm.callCount("stop")
	got, err := s.Resize(ctx, vm.DatabaseID, bigger)
	if err != nil || got.State != StateRunning || got.ResizePending || got.VcpuCount != 2 {
		t.Fatalf("resize of a running database = %+v, %v", got, err)
	}
	if vmm.callCount("stop") != stops+1 || !vmm.running[vm.DatabaseID] {
		t.Fatalf("not restarted: stops %d -> %d, running %v", stops, vmm.callCount("stop"), vmm.running[vm.DatabaseID])
	}
	clock = hour.Add(2 * time.Hour)
	u, err := s.Usage(ctx, vm.DatabaseID, hour, hour.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// 1 GiB for the first half hour, 2 GiB for the second; 1 vCPU, then 2.
	if u.StorageByteSeconds != (1<<30)*1800+(2<<30)*1800 || u.ComputeUnitSeconds != 1800+2*1800 {
		t.Fatalf("usage across the resize = %+v", u)
	}
}

func TestResizeRefusals(t *testing.T) {
	ctx := context.Background()
	s, _, vmm := newTestScheduler(t, 1024)
	vm := provision(t, s)
	for name, shape := range map[string]Shape{
		"shrink":    {VcpuCount: 1, MemMiB: 384, VolumeBytes: 512 << 20},
		"no vcpu":   {VcpuCount: 0, MemMiB: 384, VolumeBytes: 1 << 30},
		"huge disk": {VcpuCount: 1, MemMiB: 384, VolumeBytes: 200 << 30},
		"huge RAM":  {VcpuCount: 1, MemMiB: 100000, VolumeBytes: 1 << 30},
	} {
		if _, err := s.Resize(ctx, vm.DatabaseID, shape); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// The node's 1024 MB ceiling cannot hold a running 2 GiB VM.
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resize(ctx, vm.DatabaseID, Shape{VcpuCount: 1, MemMiB: 2048, VolumeBytes: 1 << 30}); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("over the RAM ceiling = %v", err)
	}
	// A failed grow leaves the resize pending; the repeat finishes it.
	if err := s.Park(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	vmm.growErr = errors.New("resize2fs failed")
	if _, err := s.Resize(ctx, vm.DatabaseID, Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: 2 << 30}); err == nil {
		t.Fatal("a failed grow reported success")
	}
	if got, _ := s.Get(ctx, vm.DatabaseID); !got.ResizePending {
		t.Fatal("a failed grow cleared the pending mark")
	}
	vmm.growErr = nil
	if got, err := s.Resize(ctx, vm.DatabaseID, Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: 2 << 30}); err != nil || got.ResizePending {
		t.Fatalf("resumed resize = %+v, %v", got, err)
	}
}
