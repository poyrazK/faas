// adr: 1027 — production class: a replicated database's standby attaches
// after a wake, becomes synchronous, stays synchronized through a clean
// park, is given up (record first) when it stops keeping up, moves off
// nodes that drain, and takes over only when synchronized.
package dbsched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// orderedVMM fails the test if a primary turns asynchronous while the
// record still lets its standby be promoted.
type orderedVMM struct {
	*fakeVMM
	t     *testing.T
	store Store
}

func (o orderedVMM) Replication(ctx context.Context, node Node, id string, mode dbguest.ReplicationMode, standbyNode string) (dbguest.ReplicationStatus, error) {
	if mode == dbguest.ReplicationAsync || mode == dbguest.ReplicationOff {
		if vm, err := o.store.Get(ctx, id); err == nil && vm.StandbySynced {
			o.t.Errorf("%s: the primary turned %s while its standby was still synchronized", id, mode)
		}
	}
	return o.fakeVMM.Replication(ctx, node, id, mode, standbyNode)
}

func newStandbyScheduler(t *testing.T, nodes ...string) (*Scheduler, *MemStore, *fakeVMM) {
	t.Helper()
	store, vmm := NewMemStore(), newFakeVMM()
	for i, id := range nodes {
		// Earlier nodes have more room, so placement is predictable.
		seedNodes(t, store, Node{ID: id, TargetURL: "unix:///" + id, AdmissionCeilingMB: 4096, VolumeCapacityBytes: int64(100-i) << 30, Lifecycle: NodeActive})
	}
	s := New(store, orderedVMM{fakeVMM: vmm, t: t, store: store}, Config{IdleTimeout: time.Minute, ReadyTimeout: 2 * time.Second, WakeWait: time.Second,
		WakeQueueWait: 300 * time.Millisecond, StandbySeedTimeout: 2 * time.Second, StandbySyncTimeout: 500 * time.Millisecond, StandbyRetry: time.Nanosecond}, nil)
	t.Cleanup(s.standbyWG.Wait)
	return s, store, vmm
}

// wakeReplicated provisions a replicated database, wakes it and waits for
// its standby's attach.
func wakeReplicated(t *testing.T, s *Scheduler) VM {
	t.Helper()
	ctx := context.Background()
	vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, Replicated: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	got, err := s.Get(ctx, vm.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWakeAttachesASynchronousStandby(t *testing.T) {
	s, _, vmm := newStandbyScheduler(t, "db-a", "db-b")
	vm := wakeReplicated(t, s)
	if vm.NodeID != "db-a" || vm.StandbyNode != "db-b" || !vm.StandbyRunning || !vm.StandbySynced || vm.State != StateRunning {
		t.Fatalf("after the wake: %+v; want running on db-a with a synchronized standby on db-b", vm)
	}
	if mode, on := vmm.replicationOf(vm.DatabaseID); mode != dbguest.ReplicationSync || on != "db-b" {
		t.Fatalf("primary %s, standby on %q", mode, on)
	}
	if calls := vmm.nodeCalls(); !strings.Contains(calls, "repl:async@db-a") || !strings.Contains(calls, "start_standby@db-b<db-a") ||
		strings.Index(calls, "start_standby@db-b") > strings.Index(calls, "repl:sync@db-a") {
		t.Fatalf("calls %s; want async (role and slot), then the standby, then sync", calls)
	}
}

func TestCleanParkKeepsTheStandbySynchronizedAndAWakeDoesNot(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	vm := wakeReplicated(t, s)
	if err := s.Park(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	parked, _ := store.Get(ctx, vm.DatabaseID)
	if parked.State != StateParked || !parked.StandbySynced || parked.StandbyRunning {
		t.Fatalf("after a clean park: %+v; want parked with its standby stopped and synchronized", parked)
	}
	if calls := vmm.nodeCalls(); strings.LastIndex(calls, "stop@db-b") < strings.LastIndex(calls, "stop@db-a") {
		t.Fatalf("calls %s; the standby stops after its primary", calls)
	}
	// A wake whose standby cannot boot leaves the primary asynchronous and
	// the standby unsynchronized: the primary committed without it.
	vmm.standbyErr = errors.New("standby node full")
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	woke, _ := store.Get(ctx, vm.DatabaseID)
	if woke.StandbySynced || woke.StandbyRunning || woke.State != StateRunning {
		t.Fatalf("after a wake without its standby: %+v", woke)
	}
	if mode, _ := vmm.replicationOf(vm.DatabaseID); mode != dbguest.ReplicationAsync {
		t.Fatalf("primary mode %s after a failed attach; want async", mode)
	}
	vmm.standbyErr = nil
	if _, err := s.CheckStandbys(ctx); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	if got, _ := store.Get(ctx, vm.DatabaseID); !got.StandbySynced {
		t.Fatalf("the monitor did not attach the standby again: %+v", got)
	}
}

func TestMonitorDropsAStandbyThatStopsKeepingUp(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	vm := wakeReplicated(t, s)
	vmm.mu.Lock()
	vmm.unsynced[vm.DatabaseID] = true
	vmm.mu.Unlock()
	dropped, err := s.CheckStandbys(ctx)
	if err != nil || dropped != 1 {
		t.Fatalf("check = %d, %v; want one standby dropped", dropped, err)
	}
	got, _ := store.Get(ctx, vm.DatabaseID)
	if got.StandbySynced || got.StandbyRunning {
		t.Fatalf("after the drop: %+v", got)
	}
	if mode, on := vmm.replicationOf(vm.DatabaseID); mode != dbguest.ReplicationAsync || on != "" {
		t.Fatalf("primary %s with a standby on %q; want async, standby stopped", mode, on)
	}
	vmm.mu.Lock()
	vmm.unsynced[vm.DatabaseID] = false
	vmm.mu.Unlock()
	if _, err := s.CheckStandbys(ctx); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	if got, _ := store.Get(ctx, vm.DatabaseID); !got.StandbySynced || got.StandbyNode != "db-b" {
		t.Fatalf("not attached again: %+v", got)
	}
}

func TestStandbysMoveOffADrainingNode(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b", "db-c")
	ctx := context.Background()
	running := wakeReplicated(t, s)
	parked := wakeReplicated(t, s)
	if err := s.Park(ctx, parked.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if running.StandbyNode != "db-b" || parked.StandbyNode != "db-b" {
		t.Fatalf("standbys on %s and %s; want db-b", running.StandbyNode, parked.StandbyNode)
	}
	seedNodes(t, store, Node{ID: "db-b", TargetURL: "unix:///db-b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 99 << 30, Lifecycle: NodeDraining})
	if dropped, err := s.CheckStandbys(ctx); err != nil || dropped != 1 {
		t.Fatalf("check = %d, %v; want the running database's standby dropped", dropped, err)
	}
	if mode, _ := vmm.replicationOf(running.DatabaseID); mode != dbguest.ReplicationOff {
		t.Fatalf("primary mode %s; a standby leaving its node also drops its slot", mode)
	}
	if got, _ := store.Get(ctx, parked.DatabaseID); got.StandbyNode != "db-c" || got.StandbySynced {
		t.Fatalf("parked database's standby: %+v; want moved to db-c, unsynchronized", got)
	}
	if _, err := s.CheckStandbys(ctx); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	if got, _ := store.Get(ctx, running.DatabaseID); got.StandbyNode != "db-c" || !got.StandbySynced {
		t.Fatalf("running database's standby: %+v; want synchronous on db-c", got)
	}
}

func TestFailoverPromotesARunningSynchronizedStandby(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b", "db-c")
	ctx := context.Background()
	vm := wakeReplicated(t, s)
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///db-a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeUnavailable})
	if n, err := s.SettleLostNodes(ctx); err != nil || n != 1 {
		t.Fatalf("settle = %d, %v", n, err)
	}
	got, _ := store.Get(ctx, vm.DatabaseID)
	if got.NodeID != "db-b" || got.State != StateRunning || got.StandbyNode != "" || got.PromotePending || got.LastError != errCodeFailedOver {
		t.Fatalf("after failover: %+v; want running on db-b", got)
	}
	if calls := vmm.nodeCalls(); vmm.callCount("promote") != 1 || strings.LastIndex(calls, "stop@db-a") < 0 {
		t.Fatalf("calls %s; want the old primary fenced and the standby promoted", calls)
	}
	// A new standby attaches to the new primary.
	if _, err := s.CheckStandbys(ctx); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	if got, _ := store.Get(ctx, vm.DatabaseID); got.StandbyNode != "db-c" || !got.StandbySynced {
		t.Fatalf("after failover, standby %+v; want a synchronous standby on db-c", got)
	}
	if calls := vmm.nodeCalls(); !strings.Contains(calls, "start_standby@db-c<db-b") {
		t.Fatalf("calls %s; want the new standby streaming from db-b", calls)
	}
}

func TestFailoverWhileParkedPromotesOnTheNextBoot(t *testing.T) {
	s, store, _ := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	vm := wakeReplicated(t, s)
	if err := s.Park(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///db-a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeUnavailable})
	if n, err := s.SettleLostNodes(ctx); err != nil || n != 1 {
		t.Fatalf("settle = %d, %v", n, err)
	}
	got, _ := store.Get(ctx, vm.DatabaseID)
	if got.NodeID != "db-b" || got.State != StateParked || !got.PromotePending {
		t.Fatalf("after failover while parked: %+v", got)
	}
	woke, err := s.Wake(ctx, vm.DatabaseID)
	if err != nil || woke.NodeID != "db-b" || woke.PromotePending {
		t.Fatalf("wake = %+v, %v; want running on db-b, promotion done", woke, err)
	}
}

func TestNoFailoverToAStandbyThatIsNotSynchronized(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	vmm.standbyErr = errors.New("no standby")
	vm := wakeReplicated(t, s)
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///db-a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeUnavailable})
	if _, err := s.SettleLostNodes(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(ctx, vm.DatabaseID)
	if got.NodeID != "db-a" || got.State != StateParked || got.LastError != errCodeNodeLost || vmm.callCount("promote") != 0 {
		t.Fatalf("after losing a node without a synchronized standby: %+v; want parked there (node_lost)", got)
	}
}

func TestStandbyVolumesSurviveTheOrphanSweepAndGoWithTheDatabase(t *testing.T) {
	s, _, vmm := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	vm := wakeReplicated(t, s)
	if err := s.Park(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CollectOrphans(ctx); err != nil || n != 0 {
		t.Fatalf("orphan sweep removed %d (%v); the standby's volume is not an orphan", n, err)
	}
	if err := s.Delete(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if calls := vmm.nodeCalls(); !strings.Contains(calls, "discard@db-b") || !strings.Contains(calls, "delete@db-a") {
		t.Fatalf("calls %s; want the standby volume discarded and the primary deleted", calls)
	}
}
