// adr: 1027 — restore drills prove each node's backups restore: one a day
// per node, recovered to just after a mark the source commits, passed
// only if the copy holds that mark, settled into a record, and never left
// behind.
package dbsched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestRestoreDrillsRunDailyPerNodeAndCleanUp(t *testing.T) {
	s, store, vmm, source := archivedSource(t)
	s.cfg.Metrics = NewMetrics(prometheus.NewRegistry(), "schedd")
	ctx := context.Background()
	clock := time.Now().Add(2 * time.Minute)
	s.now = func() time.Time { return clock }
	markedAt := time.Now().UTC().Truncate(time.Microsecond)
	vmm.mu.Lock()
	vmm.markAt = markedAt
	vmm.mu.Unlock()

	got, err := s.RunDrills(ctx)
	if err != nil || got.Started != 1 {
		t.Fatalf("first pass = %+v, %v; want one drill started", got, err)
	}
	running, _ := store.RunningDrills(ctx)
	if len(running) != 1 || running[0].SourceID != source.DatabaseID || running[0].NodeID != source.NodeID {
		t.Fatalf("running drills = %+v", running)
	}
	drill := running[0]
	// The source committed the drill's mark; the drill recovers to just
	// after it.
	vmm.mu.Lock()
	mark := vmm.marks[source.DatabaseID]
	vmm.mu.Unlock()
	if mark.drill != drill.ID || !drill.TargetAt.Equal(markedAt) {
		t.Fatalf("mark = %+v, target %v; want drill %s marked at the target %v", mark, drill.TargetAt, drill.ID, markedAt)
	}
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
	dropped, checked := countCalls(vmm.calls, "drop_archive"), countCalls(vmm.calls, "check_drill")
	vmm.mu.Unlock()
	if dropped != 1 || checked != 1 {
		t.Fatalf("drill archives dropped = %d, copies checked = %d; want 1 each", dropped, checked)
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
	vmm.markAt = clock.Add(-time.Second)
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

func TestRestoreDrillFailsACopyWithoutTheSourcesMark(t *testing.T) {
	s, store, vmm, source := archivedSource(t)
	ctx := context.Background()
	clock := time.Now().Add(2 * time.Minute)
	s.now = func() time.Time { return clock }
	if got, err := s.RunDrills(ctx); err != nil || got.Started != 1 {
		t.Fatalf("start = %+v, %v", got, err)
	}
	running, _ := store.RunningDrills(ctx)
	s.restoreWG.Wait()
	// The copy came back without the source's last commit.
	vmm.mu.Lock()
	delete(vmm.marks, running[0].ID)
	vmm.mu.Unlock()
	if got, err := s.RunDrills(ctx); err != nil || got.Failed != 1 {
		t.Fatalf("settle = %+v, %v; want the drill failed", got, err)
	}
	store.mu.Lock()
	d := store.drills[running[0].ID]
	store.mu.Unlock()
	if d.Result != DrillFailed || d.ErrorCode != dbguest.ErrCodeDrillMarkMissing {
		t.Fatalf("drill = %+v; want failed with %s", d, dbguest.ErrCodeDrillMarkMissing)
	}
	if vms, _ := store.List(ctx, allStates...); len(vms) != 1 || vms[0].DatabaseID != source.DatabaseID {
		t.Fatalf("databases after the drill = %+v", vms)
	}
}

func TestRestoreDrillWithoutAMarkDoesNotRestore(t *testing.T) {
	cases := []struct {
		name    string
		markErr string
		ahead   time.Duration
		result  DrillResult
		code    string
	}{
		{name: "outdated guest", markErr: dbguest.ErrCodeInvalidRequest, result: DrillSkipped, code: "drill_source_outdated"},
		{name: "mark failed", markErr: dbguest.ErrCodeDrillMarkFailed, result: DrillFailed, code: dbguest.ErrCodeDrillMarkFailed},
		{name: "source clock far ahead", ahead: time.Minute, result: DrillFailed, code: dbguest.ErrCodeDrillMarkFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, store, vmm, _ := archivedSource(t)
			ctx := context.Background()
			clock := time.Now().Add(2 * time.Minute)
			s.now = func() time.Time { return clock }
			vmm.mu.Lock()
			vmm.markErr = tc.markErr
			if tc.ahead > 0 {
				vmm.markAt = clock.Add(tc.ahead)
			}
			vmm.mu.Unlock()
			got, err := s.RunDrills(ctx)
			if err != nil || got.Started != 0 || got.Failed+got.Skipped != 1 {
				t.Fatalf("pass = %+v, %v; want one drill ended unstarted", got, err)
			}
			store.mu.Lock()
			var drills []Drill
			for _, d := range store.drills {
				drills = append(drills, d)
			}
			store.mu.Unlock()
			if len(drills) != 1 || drills[0].Result != tc.result || drills[0].ErrorCode != tc.code {
				t.Fatalf("drills = %+v; want one %s %s", drills, tc.result, tc.code)
			}
			vmm.mu.Lock()
			boots := len(vmm.restoreBoots)
			vmm.mu.Unlock()
			if boots != 0 {
				t.Fatalf("restore boots = %d; a drill without a mark must not restore", boots)
			}
		})
	}
}

func TestRestoreDrillWaitsForASourceClockAhead(t *testing.T) {
	s, store, vmm, _ := archivedSource(t)
	ctx := context.Background()
	offset := 2 * time.Minute
	s.now = func() time.Time { return time.Now().Add(offset) }
	markedAt := s.now().Add(50 * time.Millisecond).UTC().Truncate(time.Microsecond)
	vmm.mu.Lock()
	vmm.markAt = markedAt
	vmm.mu.Unlock()
	if got, err := s.RunDrills(ctx); err != nil || got.Started != 1 {
		t.Fatalf("start = %+v, %v; want the drill started once its target passed", got, err)
	}
	running, _ := store.RunningDrills(ctx)
	if len(running) != 1 || !running[0].TargetAt.Equal(markedAt) {
		t.Fatalf("running = %+v; want the target %v", running, markedAt)
	}
	s.restoreWG.Wait()
}
