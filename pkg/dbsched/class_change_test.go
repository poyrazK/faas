// adr: 1027 — a database joins and leaves the production class: turning
// replication on places a standby that attaches after the restart; turning
// it off retires the standby (database stopped first, then the standby,
// then its volume) and drops the primary's slot once the primary runs.
package dbsched

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestResizeTurnsReplicationOnAndOff(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	id := vm.DatabaseID
	if _, err := s.Wake(ctx, id); err != nil {
		t.Fatal(err)
	}

	got, err := s.Resize(ctx, id, Shape{VcpuCount: 2, MemMiB: 768, VolumeBytes: 1 << 30, Replicated: true})
	if err != nil || got.State != StateRunning || !got.Replicated || got.StandbyNode != "db-b" || got.ResizePending {
		t.Fatalf("resize into production = %+v, %v", got, err)
	}
	s.standbyWG.Wait()
	if got, _ = store.Get(ctx, id); !got.StandbySynced || !got.StandbyRunning {
		t.Fatalf("after joining production: %+v; want a synchronized standby", got)
	}

	vmm.mu.Lock()
	vmm.onNode = nil
	vmm.mu.Unlock()
	got, err = s.Resize(ctx, id, Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil || got.State != StateRunning || got.Replicated || got.StandbyNode != "" || got.StandbySynced || !got.DropStandbySlot {
		t.Fatalf("resize out of production = %+v, %v", got, err)
	}
	calls := vmm.nodeCalls()
	if !strings.Contains(calls, "discard@db-b") || strings.Index(calls, "stop@db-b") < strings.Index(calls, "stop@db-a") ||
		strings.Index(calls, "discard@db-b") < strings.Index(calls, "stop@db-b") {
		t.Fatalf("calls %s; want the primary stopped, then the standby, then its volume discarded", calls)
	}
	if _, on := vmm.replicationOf(id); on != "" {
		t.Fatalf("the standby still runs on %s", on)
	}
	if _, err := s.CheckStandbys(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ = store.Get(ctx, id); got.DropStandbySlot {
		t.Fatalf("the slot drop is still owed after the monitor ran: %+v", got)
	}
	if mode, _ := vmm.replicationOf(id); mode != dbguest.ReplicationOff {
		t.Fatalf("primary replication %s; want off (slot dropped)", mode)
	}
}

// A parked database leaves production without a wake; its slot is dropped
// once it next runs.
func TestLeavingProductionWhileParked(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	vm := wakeReplicated(t, s)
	id := vm.DatabaseID
	if err := s.Park(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := s.Resize(ctx, id, Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil || got.State != StateParked || got.Replicated || !got.DropStandbySlot {
		t.Fatalf("resize of a parked production database = %+v, %v", got, err)
	}
	if !strings.Contains(vmm.nodeCalls(), "discard@db-b") {
		t.Fatalf("the standby volume stayed: %s", vmm.nodeCalls())
	}
	if _, err := s.CheckStandbys(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ = store.Get(ctx, id); !got.DropStandbySlot {
		t.Fatal("the slot drop was recorded done while the primary was parked")
	}
	if _, err := s.Wake(ctx, id); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	if _, err := s.CheckStandbys(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ = store.Get(ctx, id); got.DropStandbySlot || got.StandbyRunning {
		t.Fatalf("after the wake: %+v; want the slot dropped and no standby", got)
	}
}

// Joining production needs room for a standby on another node; a refusal
// leaves the database as it was. Joining again before the old slot was
// dropped reuses the slot.
func TestJoiningProductionNeedsAStandbyNode(t *testing.T) {
	s, store, _ := newStandbyScheduler(t, "db-a")
	ctx := context.Background()
	vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resize(ctx, vm.DatabaseID, Shape{VcpuCount: 2, MemMiB: 768, VolumeBytes: 1 << 30, Replicated: true}); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("joining production with one node = %v, want ErrNoCapacity", err)
	}
	if got, _ := store.Get(ctx, vm.DatabaseID); got.Replicated || got.ResizePending || got.VcpuCount != 1 {
		t.Fatalf("after the refusal: %+v", got)
	}

	s2, store2, _ := newStandbyScheduler(t, "db-a", "db-b")
	parked := wakeReplicated(t, s2)
	if err := s2.Park(ctx, parked.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Resize(ctx, parked.DatabaseID, Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	back, err := s2.Resize(ctx, parked.DatabaseID, Shape{VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, Replicated: true})
	if err != nil || !back.Replicated || back.DropStandbySlot || back.StandbyNode != "db-b" {
		t.Fatalf("joining again = %+v, %v; want replicated with the slot kept", back, err)
	}
	if got, _ := store2.Get(ctx, parked.DatabaseID); got.StandbySynced {
		t.Fatalf("a new standby counts as synchronized: %+v", got)
	}
}
