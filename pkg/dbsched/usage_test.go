// adr: 1027
package dbsched

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSumUsageOverlapsAndHours(t *testing.T) {
	from := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	rows := []UsageRow{
		// Ran 09:30-10:15 at 2 vCPUs: 15 min in the window.
		{Meter: MeterCompute, Start: from.Add(-30 * time.Minute), End: from.Add(15 * time.Minute), Rate: 2},
		// Still running since 10:45.
		{Meter: MeterCompute, Start: from.Add(45 * time.Minute), Rate: 2},
		// 2 TiB for the whole window: must not overflow.
		{Meter: MeterStorage, Start: from.Add(-24 * time.Hour), Rate: 2 << 40},
		{Meter: MeterHistory, Start: from.Add(30 * time.Minute), End: from.Add(30*time.Minute + 1500*time.Millisecond), Rate: 1000},
		{Meter: MeterEgress, Start: from, Rate: 7},
		{Meter: MeterEgress, Start: from.Add(-time.Hour), Rate: 1000},
		{Meter: MeterEgress, Start: to, Rate: 1000},
		// Entirely outside.
		{Meter: MeterCompute, Start: to.Add(time.Minute), End: to.Add(2 * time.Minute), Rate: 2},
	}
	u := sumUsage(rows, from, to, to.Add(time.Hour))
	want := Usage{ActiveSeconds: 30 * 60, ComputeUnitSeconds: 2 * 30 * 60, StorageByteSeconds: (2 << 40) * 3600, HistoryByteSeconds: 1500, EgressBytes: 7}
	if u != want {
		t.Fatalf("usage = %+v, want %+v", u, want)
	}
	// Asked at 10:50, the open rows count until 10:50, not to 11:00.
	now := from.Add(50 * time.Minute)
	u = sumUsage(rows, from, to, now)
	if u.ActiveSeconds != 20*60 || u.StorageByteSeconds != (2<<40)*50*60 {
		t.Fatalf("usage at 10:50 = %+v", u)
	}
}

// TestSchedulerMetersADatabase follows one database through provision,
// a wake, a park, an archive sample and some egress, on a fixed clock.
func TestSchedulerMetersADatabase(t *testing.T) {
	ctx := context.Background()
	s, store, vmm := newTestScheduler(t, 4096)
	hour := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	clock := hour.Add(10 * time.Minute)
	now := func() time.Time { return clock }
	store.now, s.now = now, now

	vm := provision(t, s)
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(10 * time.Minute)
	if err := s.Park(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(5 * time.Minute)
	vmm.archiveBytes = map[string]int64{vm.DatabaseID: 5000}
	if n, err := s.SampleArchives(ctx); err != nil || n != 1 {
		t.Fatalf("sample archives = %d, %v", n, err)
	}
	if err := s.RecordEgress(ctx, vm.DatabaseID, 1234); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEgress(ctx, vm.DatabaseID, -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative egress = %v", err)
	}
	clock = hour.Add(2 * time.Hour)
	u, err := s.Usage(ctx, vm.DatabaseID, hour, hour.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{
		ActiveSeconds: 600, ComputeUnitSeconds: 600, // 1 vCPU for the 10 minutes it ran
		StorageByteSeconds: (1 << 30) * 50 * 60, // provisioned at :10
		HistoryByteSeconds: 5000 * 35 * 60,      // sampled at :25
		EgressBytes:        1234,
	}
	if u != want {
		t.Fatalf("usage = %+v, want %+v", u, want)
	}
	if _, err := s.Usage(ctx, vm.DatabaseID, hour, hour); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty window = %v", err)
	}
}

// A lost node's databases are sized through a live node when the archive
// is shared, so the bucket prune of their history shows on the meter;
// without a shared archive they keep their last sample.
func TestSampleArchivesOfALostNodeThroughALiveOne(t *testing.T) {
	ctx := context.Background()
	s, store, vmm := newMoveScheduler(t)
	start := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	clock := start
	now := func() time.Time { return clock }
	store.now, s.now = now, now
	vm := provisionOn(t, s, "db-a")
	vmm.archiveBytes = map[string]int64{vm.DatabaseID: 5000}
	if n, err := s.SampleArchives(ctx); err != nil || n != 1 {
		t.Fatalf("sample = %d, %v", n, err)
	}

	clock = clock.Add(10 * time.Minute)
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeUnavailable})
	vmm.archiveBytes[vm.DatabaseID] = 2000 // the bucket copy, pruned
	vmm.onNode = nil
	if n, err := s.SampleArchives(ctx); err != nil || n != 1 {
		t.Fatalf("sample with db-a lost = %d, %v", n, err)
	}
	if got := calls(vmm, "size@"); len(got) != 1 || got[0] != "size@db-b" {
		t.Fatalf("sized through %v; want db-b only", got)
	}
	clock = clock.Add(10 * time.Minute)
	u, err := s.Usage(ctx, vm.DatabaseID, start, clock)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(5000*600 + 2000*600); u.HistoryByteSeconds != want {
		t.Fatalf("history = %d byte-seconds, want %d", u.HistoryByteSeconds, want)
	}

	// Without a shared archive no other node reaches it.
	s.cfg.SharedArchive = false
	vmm.onNode = nil
	if n, err := s.SampleArchives(ctx); err != nil || n != 0 || len(calls(vmm, "size@")) != 0 {
		t.Fatalf("sample without a shared archive = %d, %v (calls %v)", n, err, calls(vmm, "size@"))
	}
}
