// adr: 1027
package dbsched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func provisionAlwaysOn(t *testing.T, s *Scheduler) VM {
	t.Helper()
	vm, err := s.Provision(context.Background(), ProvisionRequest{DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, AlwaysOn: true})
	if err != nil || !vm.AlwaysOn {
		t.Fatalf("provision always-on = %+v, %v", vm, err)
	}
	return vm
}

func TestAlwaysOnIsNeverParkedForIdleness(t *testing.T) {
	s, store, _ := newTestScheduler(t, 4096)
	ctx := context.Background()
	always := provisionAlwaysOn(t, s)
	if _, err := s.Wake(ctx, always.DatabaseID); err != nil {
		t.Fatal(err)
	}
	_ = store.Touch(ctx, always.DatabaseID, time.Now().Add(-time.Hour))
	if n, err := s.ParkIdle(ctx); err != nil || n != 0 {
		t.Fatalf("ParkIdle = %d, %v; an always-on database must stay up", n, err)
	}
	if got, _ := store.Get(ctx, always.DatabaseID); got.State != StateRunning {
		t.Fatalf("always-on = %s", got.State)
	}
}

func TestKeepWarmWakesOnlyParkedAlwaysOn(t *testing.T) {
	s, store, _ := newTestScheduler(t, 4096)
	ctx := context.Background()
	always, scaleToZero := provisionAlwaysOn(t, s), provision(t, s)
	n, err := s.KeepWarm(ctx)
	if err != nil || n != 1 {
		t.Fatalf("KeepWarm = %d, %v", n, err)
	}
	if got, _ := store.Get(ctx, always.DatabaseID); got.State != StateRunning {
		t.Fatalf("always-on after keep-warm = %s", got.State)
	}
	if got, _ := store.Get(ctx, scaleToZero.DatabaseID); got.State != StateParked {
		t.Fatalf("scale-to-zero woken by keep-warm: %s", got.State)
	}
	if n, _ := s.KeepWarm(ctx); n != 0 {
		t.Fatalf("a healthy running always-on database was woken again (%d)", n)
	}
}

// A database node that rebooted leaves "running" records without VMs. The
// sweep must notice and boot them; an unreachable node must not trigger a
// restart of a database that may be healthy.
func TestKeepWarmRecoversLostVMsButNotUnreachableNodes(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	always := provisionAlwaysOn(t, s)
	if _, err := s.Wake(ctx, always.DatabaseID); err != nil {
		t.Fatal(err)
	}
	starts := vmm.callCount("start")

	vmm.controlErr = errors.New("connection refused")
	if n, err := s.KeepWarm(ctx); err != nil || n != 0 || vmm.callCount("start") != starts {
		t.Fatalf("unreachable node: KeepWarm = %d, %v, starts %d -> %d", n, err, starts, vmm.callCount("start"))
	}
	vmm.controlErr = nil

	vmm.mu.Lock()
	delete(vmm.running, always.DatabaseID) // the node rebooted
	vmm.mu.Unlock()
	if n, err := s.KeepWarm(ctx); err != nil || n != 1 {
		t.Fatalf("lost VM: KeepWarm = %d, %v", n, err)
	}
	got, _ := store.Get(ctx, always.DatabaseID)
	if got.State != StateRunning || vmm.callCount("start") != starts+1 {
		t.Fatalf("lost VM not rebooted: %+v, starts %d", got, vmm.callCount("start"))
	}
}

func TestSetAlwaysOnTogglesIdleParking(t *testing.T) {
	s, store, _ := newTestScheduler(t, 4096)
	ctx := context.Background()
	vm := provision(t, s)
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SetAlwaysOn(ctx, vm.DatabaseID, true); err != nil || !got.AlwaysOn {
		t.Fatalf("SetAlwaysOn(true) = %+v, %v", got, err)
	}
	_ = store.Touch(ctx, vm.DatabaseID, time.Now().Add(-time.Hour))
	if n, _ := s.ParkIdle(ctx); n != 0 {
		t.Fatal("parked after turning always-on")
	}
	// Back to scale-to-zero: the idle clock restarts now, so it is not
	// parked for the hour it spent always-on.
	if got, err := s.SetAlwaysOn(ctx, vm.DatabaseID, false); err != nil || got.AlwaysOn {
		t.Fatalf("SetAlwaysOn(false) = %+v, %v", got, err)
	}
	if n, _ := s.ParkIdle(ctx); n != 0 {
		t.Fatal("parked immediately after turning scale-to-zero back on")
	}
	if _, err := s.SetAlwaysOn(ctx, uuid.NewString(), true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown database = %v", err)
	}
}
