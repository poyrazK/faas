// adr: 1027 — restore drills prove each node's backups restore: one a day
// per node, settled into a record, and never left behind.
package dbsched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRestoreDrillsRunDailyPerNodeAndCleanUp(t *testing.T) {
	s, store, vmm, source := archivedSource(t)
	s.cfg.Metrics = NewMetrics(prometheus.NewRegistry(), "schedd")
	ctx := context.Background()
	// A drill recovers to a minute ago: past the source's creation.
	clock := time.Now().Add(2 * time.Minute)
	s.now = func() time.Time { return clock }

	got, err := s.RunDrills(ctx)
	if err != nil || got.Started != 1 {
		t.Fatalf("first pass = %+v, %v; want one drill started", got, err)
	}
	running, _ := store.RunningDrills(ctx)
	if len(running) != 1 || running[0].SourceID != source.DatabaseID || running[0].NodeID != source.NodeID {
		t.Fatalf("running drills = %+v", running)
	}
	drill := running[0]
	s.restoreWG.Wait()

	// The restore parked the drill's database: the next pass records it
	// and deletes the database and its archive, and starts nothing new.
	if got, err := s.RunDrills(ctx); err != nil || got.OK != 1 || got.Started != 0 {
		t.Fatalf("second pass = %+v, %v; want one drill passed, none started", got, err)
	}
	if _, err := store.Get(ctx, drill.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the drill's database survived: %v", err)
	}
	vmm.mu.Lock()
	dropped := countCalls(vmm.calls, "drop_archive")
	vmm.mu.Unlock()
	if dropped != 1 {
		t.Fatalf("drill archives dropped = %d, want 1", dropped)
	}
	sums, _ := store.DrillsByNode(ctx)
	if sums[source.NodeID].LastOK.IsZero() {
		t.Fatalf("node summary = %+v", sums)
	}
	if v := testutil.ToFloat64(s.cfg.Metrics.drills.WithLabelValues("ok")); v != 1 {
		t.Fatalf("ok drills = %v", v)
	}
	if v := testutil.ToFloat64(s.cfg.Metrics.drillLastOK.WithLabelValues(source.NodeID)); v == 0 {
		t.Fatal("the node's last passing drill is not reported")
	}

	// A day later the node drills again; a failed restore is recorded as
	// such and cleaned up too.
	clock = clock.Add(25 * time.Hour)
	vmm.mu.Lock()
	vmm.restoreFail = true
	vmm.mu.Unlock()
	if got, err := s.RunDrills(ctx); err != nil || got.Started != 1 {
		t.Fatalf("next day = %+v, %v", got, err)
	}
	s.restoreWG.Wait()
	if got, err := s.RunDrills(ctx); err != nil || got.Failed != 1 {
		t.Fatalf("after a failed restore = %+v, %v", got, err)
	}
	if running, _ := store.RunningDrills(ctx); len(running) != 0 {
		t.Fatalf("drills left running: %+v", running)
	}
	if v := testutil.ToFloat64(s.cfg.Metrics.drills.WithLabelValues("failed")); v != 1 {
		t.Fatalf("failed drills = %v", v)
	}
	// Only the source remains.
	if vms, _ := store.List(ctx, allStates...); len(vms) != 1 || vms[0].DatabaseID != source.DatabaseID {
		t.Fatalf("databases after the drills = %+v", vms)
	}
}

func TestRestoreDrillsTimeOutAndCanBeTurnedOff(t *testing.T) {
	s, store, vmm, _ := archivedSource(t)
	ctx := context.Background()
	clock := time.Now().Add(2 * time.Minute)
	s.now = func() time.Time { return clock }
	hold := make(chan struct{})
	vmm.mu.Lock()
	vmm.restoreHold = hold
	vmm.mu.Unlock()
	if got, err := s.RunDrills(ctx); err != nil || got.Started != 1 {
		t.Fatalf("start = %+v, %v", got, err)
	}
	if got, _ := s.RunDrills(ctx); got.Running != 1 {
		t.Fatalf("a restoring drill = %+v; want running", got)
	}
	clock = clock.Add(s.cfg.DrillTimeout + time.Minute)
	got, err := s.RunDrills(ctx)
	close(hold)
	s.restoreWG.Wait()
	if err != nil || got.Failed != 1 {
		t.Fatalf("past the timeout = %+v, %v", got, err)
	}

	s.cfg.DrillEvery = -1
	clock = clock.Add(48 * time.Hour)
	if got, err := s.RunDrills(ctx); err != nil || got != (DrillSample{}) {
		t.Fatalf("drills off = %+v, %v", got, err)
	}
	if running, _ := store.RunningDrills(ctx); len(running) != 0 {
		t.Fatalf("drills off left %+v", running)
	}
}

func TestPickDrillSourceSkipsDrillsAndUnbackedDatabases(t *testing.T) {
	target := time.Now()
	old := target.Add(-time.Hour)
	vms := []VM{
		{DatabaseID: "a0000000-0000-4000-8000-000000000001", NodeID: "db-a", Bootstrapped: true, LastBackupAt: old, CreatedAt: old},
		{DatabaseID: "a0000000-0000-4000-8000-000000000002", NodeID: "db-a", Bootstrapped: true, CreatedAt: old},
		{DatabaseID: "a0000000-0000-4000-8000-000000000003", NodeID: "db-b", Bootstrapped: true, LastBackupAt: old, CreatedAt: old},
		{DatabaseID: "a0000000-0000-4000-8000-000000000004", NodeID: "db-a", Bootstrapped: true, LastBackupAt: old, CreatedAt: old},
		// Backed up after the target: no base backup precedes it.
		{DatabaseID: "a0000000-0000-4000-8000-000000000005", NodeID: "db-a", Bootstrapped: true, LastBackupAt: target.Add(time.Second), CreatedAt: target.Add(time.Second)},
	}
	drills := map[string]bool{"a0000000-0000-4000-8000-000000000004": true}
	got, ok := pickDrillSource(vms, drills, "db-a", "2026-10-17", target)
	if !ok || got.DatabaseID != "a0000000-0000-4000-8000-000000000001" {
		t.Fatalf("source = %+v, %v", got, ok)
	}
	if _, ok := pickDrillSource(vms, drills, "db-c", "2026-10-17", target); ok {
		t.Fatal("picked a source on a node without databases")
	}
	// The pick is the same for every replica on a day.
	again, _ := pickDrillSource(vms, drills, "db-a", "2026-10-17", target)
	if again.DatabaseID != got.DatabaseID {
		t.Fatal("the pick is not deterministic")
	}
}
