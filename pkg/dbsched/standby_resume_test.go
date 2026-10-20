// adr: 1027 — a wake resumes a standby that was synchronized when the
// database parked: the record never stops trusting it and the primary is
// synchronous before its first client, so no commit is acknowledged
// without the standby; a standby that cannot come back in time, or at
// all, is given up record first.
package dbsched

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// parkedSynchronized is a production-class database parked cleanly with
// its standby synchronized, and the node calls made so far.
func parkedSynchronized(t *testing.T, s *Scheduler, store *MemStore, vmm *fakeVMM) (VM, int) {
	t.Helper()
	ctx := context.Background()
	vm := wakeReplicated(t, s)
	if err := s.Park(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if parked, _ := store.Get(ctx, vm.DatabaseID); !parked.StandbySynced || parked.StandbyRunning {
		t.Fatalf("after a clean park: %+v; want the standby stopped and synchronized", parked)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	return vm, len(vmm.onNode)
}

func callsSince(vmm *fakeVMM, mark int) string {
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	return strings.Join(vmm.onNode[mark:], " ")
}

func TestWakeResumesASynchronizedStandby(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	s.cfg.Metrics = NewMetrics(prometheus.NewRegistry(), "schedd")
	ctx := context.Background()
	vm, mark := parkedSynchronized(t, s, store, vmm)

	woke, err := s.Wake(ctx, vm.DatabaseID)
	if mode, _ := vmm.replicationOf(vm.DatabaseID); err != nil || !woke.StandbySynced || mode != dbguest.ReplicationSync {
		t.Fatalf("wake = %+v, %v, primary %s; want it synchronous, the standby still trusted, before the first client", woke, err, mode)
	}
	s.standbyWG.Wait()
	if got, _ := store.Get(ctx, vm.DatabaseID); !got.StandbyRunning || !got.StandbySynced {
		t.Fatalf("after the resume: %+v", got)
	}
	calls := callsSince(vmm, mark)
	if strings.Contains(calls, "repl:async@db-a") || !strings.Contains(calls, "start_standby@db-b<db-a") ||
		strings.Index(calls, "repl:sync@db-a") > strings.Index(calls, "start_standby@db-b") {
		t.Fatalf("calls %s; want the primary synchronous before the standby boots, and never asynchronous", calls)
	}
	if v := testutil.ToFloat64(s.cfg.Metrics.standbyResumes.WithLabelValues("ok")); v != 1 {
		t.Fatalf("resumes = %v", v)
	}
}

// The standby boots but never catches up: past StandbyResumeTimeout the
// record stops trusting it, then the primary stops waiting.
func TestAResumeThatTimesOutTurnsThePrimaryAsynchronous(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	vm, _ := parkedSynchronized(t, s, store, vmm)
	s.cfg.StandbyResumeTimeout = 100 * time.Millisecond
	vmm.mu.Lock()
	vmm.unsynced[vm.DatabaseID] = true
	vmm.mu.Unlock()
	started := time.Now()
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	got, _ := store.Get(ctx, vm.DatabaseID)
	if mode, _ := vmm.replicationOf(vm.DatabaseID); got.StandbySynced || got.StandbyRunning || mode != dbguest.ReplicationAsync {
		t.Fatalf("after a timed-out resume: %+v, primary %s", got, mode)
	}
	if took := time.Since(started); took > s.cfg.StandbySyncTimeout {
		t.Fatalf("the resume waited %v, past its timeout", took)
	}
}

// A standby whose node cannot take it back is given up at once.
func TestAResumeWhoseStandbyCannotBeClaimedTurnsThePrimaryAsynchronous(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	vm, _ := parkedSynchronized(t, s, store, vmm)
	seedNodes(t, store, Node{ID: "db-b", TargetURL: "unix:///db-b", AdmissionCeilingMB: 128, VolumeCapacityBytes: 99 << 30, Lifecycle: NodeActive})
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	got, _ := store.Get(ctx, vm.DatabaseID)
	if mode, _ := vmm.replicationOf(vm.DatabaseID); got.StandbySynced || mode != dbguest.ReplicationAsync {
		t.Fatalf("after an unclaimable resume: %+v, primary %s", got, mode)
	}
}

// No resume onto a draining node, or with resuming turned off: the wake
// stops trusting the standby before the boot, as before.
func TestWakeWithoutAResumableStandbyStartsAsynchronous(t *testing.T) {
	for name, prepare := range map[string]func(*Scheduler, *MemStore){
		"draining node": func(_ *Scheduler, store *MemStore) {
			seedNodes(t, store, Node{ID: "db-b", TargetURL: "unix:///db-b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 99 << 30, Lifecycle: NodeDraining})
		},
		"resuming off": func(s *Scheduler, _ *MemStore) { s.cfg.StandbyResumeTimeout = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			s, store, vmm := newStandbyScheduler(t, "db-a", "db-b", "db-c")
			vm, _ := parkedSynchronized(t, s, store, vmm)
			prepare(s, store)
			woke, err := s.Wake(context.Background(), vm.DatabaseID)
			if mode, _ := vmm.replicationOf(vm.DatabaseID); err != nil || woke.StandbySynced || mode == dbguest.ReplicationSync {
				t.Fatalf("wake = %+v, %v, primary %s; want it asynchronous and the standby untrusted", woke, err, mode)
			}
			s.standbyWG.Wait()
		})
	}
}

// callsMark is how many node calls the fake has seen, for callsSince.
func callsMark(vmm *fakeVMM) int {
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	return len(vmm.onNode)
}
