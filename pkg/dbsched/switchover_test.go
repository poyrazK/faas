// adr: 1027 — a planned switchover hands a production-class database to
// its synchronized standby losing nothing: a running primary stops
// cleanly before the standby is promoted, a parked database's next boot
// promotes it, a failed promotion parks the database where it was, and
// evacuating a draining node switches databases over instead of moving
// them.
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

func TestSwitchoverHandsARunningDatabaseToItsStandby(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b", "db-c")
	s.cfg.Metrics = NewMetrics(prometheus.NewRegistry(), "schedd")
	ctx := context.Background()
	vm := wakeReplicated(t, s)
	vmm.mu.Lock()
	mark := len(vmm.calls)
	vmm.mu.Unlock()
	if err := s.Switchover(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(ctx, vm.DatabaseID)
	if got.NodeID != "db-b" || got.State != StateRunning || got.StandbySynced || got.LastError != errCodeFailedOver {
		t.Fatalf("after the switchover: %+v; want running on db-b, a new standby to come", got)
	}
	vmm.mu.Lock()
	calls := strings.Join(vmm.calls[mark:], " ")
	promoted := vmm.promoted[vm.DatabaseID]
	vmm.mu.Unlock()
	if stop, promote := strings.Index(calls, "stop"), strings.Index(calls, "promote"); stop < 0 || promote < stop {
		t.Fatalf("calls %s; want the primary stopped before the standby is promoted", calls)
	}
	if promoted != "db-b" {
		t.Fatalf("promoted on %q", promoted)
	}
	if v := testutil.ToFloat64(s.cfg.Metrics.failovers.WithLabelValues(failoverSwitchover, "ok")); v != 1 {
		t.Fatalf("switchovers = %v", v)
	}
	// The monitor gives it a new standby. (The fake keeps one volume per
	// database, so the old primary's node cannot take it here.)
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///db-a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeDraining})
	if _, err := s.CheckStandbys(ctx); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	if got, _ := store.Get(ctx, vm.DatabaseID); !got.StandbySynced || got.StandbyNode != "db-c" {
		t.Fatalf("after the monitor: %+v; want a new synchronized standby", got)
	}
}

func TestSwitchoverOfAParkedDatabasePromotesOnItsNextBoot(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	vm, _ := parkedSynchronized(t, s, store, vmm)
	if err := s.Switchover(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(ctx, vm.DatabaseID); got.NodeID != "db-b" || got.State != StateParked || !got.PromotePending {
		t.Fatalf("after the switchover: %+v; want parked on db-b, promoting on its next boot", got)
	}
	woke, err := s.Wake(ctx, vm.DatabaseID)
	if err != nil || woke.NodeID != "db-b" || woke.PromotePending {
		t.Fatalf("wake = %+v, %v", woke, err)
	}
}

func TestSwitchoverRefusesWithoutASynchronizedStandby(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	plain := provision(t, s)
	if err := s.Switchover(ctx, plain.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("a database without a standby = %v", err)
	}
	vm := wakeReplicated(t, s)
	seedNodes(t, store, Node{ID: "db-b", TargetURL: "unix:///db-b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 99 << 30, Lifecycle: NodeDraining})
	if err := s.Switchover(ctx, vm.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("onto a draining node = %v", err)
	}
	vmm.mu.Lock()
	running := vmm.running[vm.DatabaseID]
	vmm.mu.Unlock()
	if !running {
		t.Fatal("a refused switchover stopped the primary")
	}
}

// A promotion that fails after the clean stop parks the database where it
// was, standby still trusted, and its next wake resumes both.
func TestAFailedSwitchoverParksTheDatabaseWhereItWas(t *testing.T) {
	s, store, vmm := newStandbyScheduler(t, "db-a", "db-b")
	ctx := context.Background()
	vm := wakeReplicated(t, s)
	vmm.mu.Lock()
	vmm.promoteErr = dbguest.ErrCodePromoteFailed
	vmm.mu.Unlock()
	if err := s.Switchover(ctx, vm.DatabaseID); err == nil {
		t.Fatal("a failed promotion switched over")
	}
	got, _ := store.Get(ctx, vm.DatabaseID)
	if got.NodeID != "db-a" || got.State != StateParked || !got.StandbySynced || got.StandbyRunning {
		t.Fatalf("after the failed switchover: %+v; want parked on db-a, standby stopped and trusted", got)
	}
	vmm.mu.Lock()
	vmm.promoteErr = ""
	vmm.mu.Unlock()
	if woke, err := s.Wake(ctx, vm.DatabaseID); err != nil || woke.NodeID != "db-a" || !woke.StandbySynced {
		t.Fatalf("wake after the failed switchover = %+v, %v", woke, err)
	}
	s.standbyWG.Wait()
}

func TestEvacuationSwitchesProductionDatabasesOver(t *testing.T) {
	s, store, _ := newStandbyScheduler(t, "db-a", "db-b", "db-c")
	s.cfg.EvacuateDraining, s.cfg.SharedArchive = true, true
	ctx := context.Background()
	busy := wakeReplicated(t, s) // running, not always-on: a move would wait for it to park
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///db-a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeDraining})
	if n, err := s.EvacuateDraining(ctx); err != nil || n != 1 {
		t.Fatalf("evacuation = %d, %v; want the running production database switched over", n, err)
	}
	if got, _ := store.Get(ctx, busy.DatabaseID); got.NodeID != "db-b" || got.State != StateRunning {
		t.Fatalf("after the evacuation: %+v", got)
	}
}
