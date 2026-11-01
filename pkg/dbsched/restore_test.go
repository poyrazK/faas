// adr: 1027
package dbsched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func restoreRequest(source VM, target time.Time) RestoreRequest {
	return RestoreRequest{
		ProvisionRequest: ProvisionRequest{DatabaseID: uuid.NewString(), Major: source.Major, VcpuCount: 1, MemMiB: 384, VolumeBytes: source.VolumeBytes, RestoreWindowSeconds: 3600},
		Source:           source.DatabaseID, Target: target,
	}
}

// archivedSource provisions a database on an archiving node and waits a
// moment so a target after its creation exists.
func archivedSource(t *testing.T) (*Scheduler, *MemStore, *fakeVMM, VM) {
	t.Helper()
	s, store, vmm := newTestScheduler(t, 4096)
	vmm.archive = true
	source := provision(t, s)
	time.Sleep(2 * time.Millisecond)
	return s, store, vmm, source
}

func TestRestoreRecoversInTheBackgroundAndParks(t *testing.T) {
	s, store, vmm, source := archivedSource(t)
	ctx := context.Background()
	target := time.Now()
	req := restoreRequest(source, target)
	vm, err := s.Restore(ctx, req)
	if err != nil || vm.State != StateProvisioning || vm.NodeID != source.NodeID {
		t.Fatalf("Restore = %+v, %v; want provisioning on the source's node", vm, err)
	}
	s.restoreWG.Wait()
	got, err := store.Get(ctx, vm.DatabaseID)
	if err != nil || got.State != StateParked || !got.Bootstrapped || got.LastBackupAt.IsZero() {
		t.Fatalf("restored = %+v, %v; want parked, bootstrapped, with its own backup", got, err)
	}
	want := target.UTC().Truncate(time.Microsecond)
	if got.RestoreSource != source.DatabaseID || !got.RestoreTarget.Equal(want) || got.RestoreWindowSeconds != 3600 {
		t.Fatalf("lineage = %s %v %d", got.RestoreSource, got.RestoreTarget, got.RestoreWindowSeconds)
	}
	if len(vmm.restoreBoots) != 1 || !vmm.restoreBoots[0].Equal(want) {
		t.Fatalf("restore boots = %v", vmm.restoreBoots)
	}
	// The source committed and archived past the target before recovery.
	if after, ok := vmm.flushes[source.DatabaseID]; !ok || !after.Equal(want) {
		t.Fatalf("source flush = %v %v", after, ok)
	}
	if vmm.running[vm.DatabaseID] {
		t.Fatal("restored VM still running")
	}
	// Repeating the request returns the database; another target conflicts.
	if again, err := s.Restore(ctx, req); err != nil || again.DatabaseID != vm.DatabaseID || again.State != StateParked {
		t.Fatalf("repeat = %+v, %v", again, err)
	}
	other := req
	other.Target = target.Add(-time.Millisecond)
	if _, err := s.Restore(ctx, other); !errors.Is(err, ErrConflict) {
		t.Fatalf("other target = %v, want ErrConflict", err)
	}
	// A later wake boots the restored volume normally.
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil || len(vmm.restoreBoots) != 1 {
		t.Fatalf("wake after restore = %v, restore boots %d", err, len(vmm.restoreBoots))
	}
}

func TestRestoreRejectsWhatCannotBeRestored(t *testing.T) {
	s, _, _, source := archivedSource(t)
	ctx := context.Background()
	now := time.Now()
	cases := map[string]func(*RestoreRequest){
		"future target":    func(r *RestoreRequest) { r.Target = now.Add(time.Hour) },
		"before source":    func(r *RestoreRequest) { r.Target = source.CreatedAt.Add(-time.Second) },
		"other major":      func(r *RestoreRequest) { r.Major = 16 },
		"smaller volume":   func(r *RestoreRequest) { r.VolumeBytes = source.VolumeBytes / 2 },
		"into itself":      func(r *RestoreRequest) { r.DatabaseID = source.DatabaseID },
		"no target":        func(r *RestoreRequest) { r.Target = time.Time{} },
		"negative window":  func(r *RestoreRequest) { r.RestoreWindowSeconds = -1 },
		"malformed source": func(r *RestoreRequest) { r.Source = "nope" },
	}
	for name, mutate := range cases {
		req := restoreRequest(source, now)
		mutate(&req)
		if _, err := s.Restore(ctx, req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: Restore = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := s.Restore(ctx, RestoreRequest{ProvisionRequest: restoreRequest(source, now).ProvisionRequest, Source: uuid.NewString(), Target: now}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown source = %v, want ErrNotFound", err)
	}

	// A point older than the source's window may already be pruned.
	windowed, err := s.Provision(ctx, ProvisionRequest{DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	point := windowed.CreatedAt.Add(time.Millisecond)
	s.now = func() time.Time { return point.Add(2 * time.Minute) }
	if _, err := s.Restore(ctx, restoreRequest(windowed, point)); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "window") {
		t.Errorf("point outside the window = %v, want ErrInvalid", err)
	}
	s.now = time.Now

	// A source on a node without archiving has no base backup.
	s2, _, _ := newTestScheduler(t, 4096)
	unarchived := provision(t, s2)
	if _, err := s2.Restore(ctx, restoreRequest(unarchived, time.Now())); !errors.Is(err, ErrInvalid) {
		t.Errorf("source without backups = %v, want ErrInvalid", err)
	}
}

func TestRestoreFailureFailsTheDatabase(t *testing.T) {
	s, store, vmm, source := archivedSource(t)
	vmm.restoreFail = true
	vm, err := s.Restore(context.Background(), restoreRequest(source, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	s.restoreWG.Wait()
	got, _ := store.Get(context.Background(), vm.DatabaseID)
	if got.State != StateFailed || got.LastError != errCodeRestore || got.Bootstrapped || vmm.running[vm.DatabaseID] {
		t.Fatalf("failed restore = %+v (running %v)", got, vmm.running[vm.DatabaseID])
	}
}

func TestProvisionRefusesARestore(t *testing.T) {
	s, store, _, source := archivedSource(t)
	ctx := context.Background()
	req := restoreRequest(source, time.Now())
	// Placed but not yet picked up, as after a schedd restart.
	if _, err := store.Place(ctx, VM{DatabaseID: req.DatabaseID, NodeID: source.NodeID, Major: 17, VcpuCount: 1, MemMiB: 384,
		VolumeBytes: source.VolumeBytes, RestoreSource: source.DatabaseID, RestoreTarget: req.Target}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Provision(ctx, req.ProvisionRequest); !errors.Is(err, ErrConflict) {
		t.Fatalf("Provision over a restore = %v, want ErrConflict", err)
	}
	// The sweep resumes it.
	if err := s.ResumeRestores(ctx); err != nil {
		t.Fatal(err)
	}
	s.restoreWG.Wait()
	if got, _ := store.Get(ctx, req.DatabaseID); got.State != StateParked {
		t.Fatalf("resumed restore ended %s", got.State)
	}
}

func TestDeleteStopsARunningRestore(t *testing.T) {
	s, store, vmm, source := archivedSource(t)
	vmm.restoreHold = make(chan struct{})
	ctx := context.Background()
	vm, err := s.Restore(ctx, restoreRequest(source, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for vmm.callCount("start") < 3 { // source provision, source wake, restore boot
		if time.Now().After(deadline) {
			t.Fatal("restore never booted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := s.Delete(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, vm.DatabaseID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted restore still recorded: %v", err)
	}
	if _, volume := vmm.volumes[vm.DatabaseID]; vmm.running[vm.DatabaseID] || volume {
		t.Fatal("deleted restore left its VM or volume")
	}
	s.restoreWG.Wait()
}

// twoNodeScheduler has db-a (small) and db-b; the source lands on db-a.
func twoNodeScheduler(t *testing.T, shared bool) (*Scheduler, *MemStore, *fakeVMM, VM) {
	t.Helper()
	store, vmm := NewMemStore(), newFakeVMM()
	vmm.archive = true
	seedNodes(t, store,
		Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 3 << 30, Lifecycle: NodeActive},
		Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 2 << 30, Lifecycle: NodeActive},
	)
	s := New(store, vmm, Config{IdleTimeout: time.Minute, ReadyTimeout: 2 * time.Second, WakeWait: time.Second, SharedArchive: shared}, nil)
	source := provision(t, s)
	if source.NodeID != "db-a" {
		t.Fatalf("source on %s", source.NodeID)
	}
	time.Sleep(2 * time.Millisecond)
	return s, store, vmm, source
}

func TestRestoreLeavesAFullSourceNodeOnlyWithASharedArchive(t *testing.T) {
	ctx := context.Background()
	for _, shared := range []bool{false, true} {
		s, store, vmm, source := twoNodeScheduler(t, shared)
		// Fill db-a's remaining 2 GiB.
		if _, err := store.Place(ctx, VM{DatabaseID: uuid.NewString(), NodeID: "db-a", Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 2 << 30}); err != nil {
			t.Fatal(err)
		}
		vm, err := s.Restore(ctx, restoreRequest(source, time.Now()))
		if !shared {
			if !errors.Is(err, ErrNoCapacity) {
				t.Fatalf("unshared archive, full source node = %+v, %v", vm, err)
			}
			continue
		}
		if err != nil || vm.NodeID != "db-b" {
			t.Fatalf("shared archive = %+v, %v; want db-b", vm, err)
		}
		s.restoreWG.Wait()
		got, _ := store.Get(ctx, vm.DatabaseID)
		if got.State != StateParked || len(vmm.untilEnd) != 1 || vmm.untilEnd[0] {
			t.Fatalf("restore on db-b = %+v, until-end %v", got, vmm.untilEnd)
		}
		if _, ok := vmm.flushes[source.DatabaseID]; !ok {
			t.Fatal("a reachable source was not flushed")
		}
	}
}

// After its node is lost, the source cannot flush: the restore recovers
// from the bucket to the end of the archive at most, and records how far.
func TestRestoreAfterTheSourceNodeIsLost(t *testing.T) {
	ctx := context.Background()
	s, store, vmm, source := twoNodeScheduler(t, false)
	target := time.Now()
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 3 << 30, Lifecycle: NodeUnavailable})
	if _, err := s.Restore(ctx, restoreRequest(source, target)); !errors.Is(err, ErrNodeUnavailable) {
		t.Fatalf("lost node without a shared archive = %v, want ErrNodeUnavailable", err)
	}

	s.cfg.SharedArchive = true
	vmm.recoveredTo = target.Add(-time.Minute).UTC().Truncate(time.Microsecond)
	vm, err := s.Restore(ctx, restoreRequest(source, target))
	if err != nil || vm.NodeID != "db-b" {
		t.Fatalf("restore after node loss = %+v, %v", vm, err)
	}
	s.restoreWG.Wait()
	got, _ := store.Get(ctx, vm.DatabaseID)
	if got.State != StateParked || !got.RestoreRecoveredTo.Equal(vmm.recoveredTo) {
		t.Fatalf("restored = %+v; want parked, recovered to %v", got, vmm.recoveredTo)
	}
	if len(vmm.untilEnd) != 1 || !vmm.untilEnd[0] {
		t.Fatalf("restore boot until-end = %v", vmm.untilEnd)
	}
	if _, ok := vmm.flushes[source.DatabaseID]; ok {
		t.Fatal("the scheduler tried to flush a source on a lost node")
	}
	// The record keeps the requested target as its lineage.
	if !got.RestoreTarget.Equal(target.UTC().Truncate(time.Microsecond)) {
		t.Fatalf("lineage target = %v", got.RestoreTarget)
	}
}
