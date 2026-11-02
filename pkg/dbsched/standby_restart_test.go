// adr: 1027 — a stale standby of a current primary restarts alone onto
// its node's current base: the primary runs on and stays synchronous, so
// commits only wait for the standby's boot; with resuming off the record
// stops trusting the standby before the primary turns asynchronous.
package dbsched

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestSampleStorageRestartsAStaleStandbyAlone(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	s.cfg.Metrics = NewMetrics(prometheus.NewRegistry(), "schedd")
	ctx := context.Background()
	vm := wakeReplicated(t, s)
	vmm.mu.Lock()
	vmm.stale["db-b/"+vm.DatabaseID] = true
	vmm.mu.Unlock()
	mark := callsMark(vmm)

	got, err := s.SampleStorage(ctx)
	if err != nil || got.Stale != 1 || got.StandbyRestarted != 1 || got.Restarted != 0 {
		t.Fatalf("sample = %+v, %v; want the stale standby restarted alone", got, err)
	}
	if mode, _ := vmm.replicationOf(vm.DatabaseID); mode != dbguest.ReplicationSync {
		t.Fatalf("primary while its standby restarts: %s; want synchronous", mode)
	}
	s.standbyWG.Wait()
	if after, _ := store.Get(ctx, vm.DatabaseID); after.State != StateRunning || !after.StandbyRunning || !after.StandbySynced {
		t.Fatalf("after the restart: %+v", after)
	}
	calls := callsSince(vmm, mark)
	if !strings.Contains(calls, "stop@db-b") || !strings.Contains(calls, "start_standby@db-b<db-a") ||
		strings.Contains(calls, "stop@db-a") || strings.Contains(calls, "repl:async@db-a") {
		t.Fatalf("calls %s; want only the standby stopped and booted again, the primary synchronous throughout", calls)
	}
	if again, err := s.SampleStorage(ctx); err != nil || again.Stale != 0 {
		t.Fatalf("next sample = %+v, %v; want nothing stale", again, err)
	}
	if v := testutil.ToFloat64(s.cfg.Metrics.standbyRestarts.WithLabelValues("ok")); v != 1 {
		t.Fatalf("standby restarts = %v", v)
	}
}

func TestRestartStandbyWithResumingOffGoesAsynchronousFirst(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	s.cfg.StandbyResumeTimeout = -1
	ctx := context.Background()
	vm := wakeReplicated(t, s)
	mark := callsMark(vmm)
	if err := s.RestartStandby(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	calls := callsSince(vmm, mark)
	if async, stop := strings.Index(calls, "repl:async@db-a"), strings.Index(calls, "stop@db-b"); async < 0 || stop < async {
		t.Fatalf("calls %s; want the primary asynchronous before its standby stops", calls)
	}
	if after, _ := store.Get(ctx, vm.DatabaseID); !after.StandbyRunning || !after.StandbySynced {
		t.Fatalf("after the restart: %+v; want the standby synchronous again", after)
	}
}

func TestRestartStandbyRefusesWithoutARunningSynchronizedStandby(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	plain := provision(t, s)
	if err := s.RestartStandby(ctx, plain.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("no standby = %v", err)
	}
	vm, _ := parkedSynchronized(t, s, store, vmm)
	if err := s.RestartStandby(ctx, vm.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("parked = %v", err)
	}
}
