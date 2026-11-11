// adr: 1027 — schedd samples running databases' storage without waking
// any, and counts the ones the guard turned read-only.
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

func TestSampleStorageCountsRunningDatabases(t *testing.T) {
	s, _, vmm := newTestScheduler(t, 4096)
	reg := prometheus.NewRegistry()
	s.cfg.Metrics = NewMetrics(reg, "schedd")
	ctx := context.Background()
	full, low, fine, parked := provision(t, s), provision(t, s), provision(t, s), provision(t, s)
	for _, vm := range []VM{full, low, fine} {
		if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
			t.Fatal(err)
		}
	}
	vmm.mu.Lock()
	vmm.storage[full.DatabaseID] = &dbguest.StorageStatus{TotalBytes: 1 << 30, FreeBytes: 100 << 20, ReadOnly: true, Low: true}
	vmm.storage[low.DatabaseID] = &dbguest.StorageStatus{TotalBytes: 1 << 30, FreeBytes: 200 << 20, Low: true}
	vmm.storage[fine.DatabaseID] = &dbguest.StorageStatus{TotalBytes: 1 << 30, FreeBytes: 900 << 20}
	vmm.storage[parked.DatabaseID] = &dbguest.StorageStatus{TotalBytes: 1 << 30, FreeBytes: 1, ReadOnly: true}
	starts := len(vmm.calls)
	vmm.mu.Unlock()

	got, err := s.SampleStorage(ctx)
	if err != nil || got != (StorageSample{Sampled: 3, Low: 2, ReadOnly: 1}) {
		t.Fatalf("sample = %+v, %v", got, err)
	}
	vmm.mu.Lock()
	woke := vmm.calls[starts:]
	vmm.mu.Unlock()
	for _, call := range woke {
		if call == "start" {
			t.Fatalf("sampling woke a database: %v", woke)
		}
	}
	for level, want := range map[string]float64{"sampled": 3, "low": 2, "read_only": 1} {
		if v := testutil.ToFloat64(s.cfg.Metrics.storage.WithLabelValues(level)); v != want {
			t.Errorf("%s = %v, want %v", level, v, want)
		}
	}
}

// Stale databases are counted; idle always-on ones restart onto the
// current base, a few per sample, and busy or scale-to-zero ones wait.
func TestSampleStorageRestartsIdleStaleAlwaysOnDatabases(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 8192)
	reg := prometheus.NewRegistry()
	s.cfg.Metrics, s.cfg.StaleRestarts = NewMetrics(reg, "schedd"), 1
	ctx := context.Background()
	idle1, idle2, busy, scaleToZero, fresh := provision(t, s), provision(t, s), provision(t, s), provision(t, s), provision(t, s)
	for _, vm := range []VM{idle1, idle2, busy, scaleToZero, fresh} {
		if vm.DatabaseID != scaleToZero.DatabaseID {
			if _, err := s.SetAlwaysOn(ctx, vm.DatabaseID, true); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
			t.Fatal(err)
		}
	}
	long := time.Now().Add(-time.Hour)
	for _, vm := range []VM{idle1, idle2, scaleToZero} {
		if err := store.Touch(ctx, vm.DatabaseID, long); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Touch(ctx, busy.DatabaseID, time.Now()); err != nil {
		t.Fatal(err)
	}
	vmm.mu.Lock()
	for _, vm := range []VM{idle1, idle2, busy, scaleToZero} {
		vmm.stale["db-a/"+vm.DatabaseID] = true
	}
	stops := countCalls(vmm.calls, "stop")
	vmm.mu.Unlock()

	got, err := s.SampleStorage(ctx)
	if err != nil || got.Stale != 4 || got.Restarted != 1 {
		t.Fatalf("sample = %+v, %v; want 4 stale, 1 restarted", got, err)
	}
	vmm.mu.Lock()
	restarted := countCalls(vmm.calls, "stop") - stops
	vmm.mu.Unlock()
	if restarted != 1 {
		t.Fatalf("%d databases stopped, want 1", restarted)
	}
	for _, vm := range []VM{idle1, idle2, busy, scaleToZero, fresh} {
		if got, err := store.Get(ctx, vm.DatabaseID); err != nil || got.State != StateRunning {
			t.Fatalf("%s after the sample = %v, %v; want running", vm.DatabaseID, got.State, err)
		}
	}
	if v := testutil.ToFloat64(s.cfg.Metrics.stale); v != 4 {
		t.Fatalf("stale gauge = %v", v)
	}
	if v := testutil.ToFloat64(s.cfg.Metrics.staleRestart.WithLabelValues("ok")); v != 1 {
		t.Fatalf("stale restarts = %v", v)
	}
}

func countCalls(calls []string, name string) int {
	n := 0
	for _, c := range calls {
		if c == name {
			n++
		}
	}
	return n
}

// An operator's restart parks and wakes a running database, leaves a
// parked one for its next wake and refuses one in any other state.
func TestRestartParksAndWakesARunningDatabase(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 8192)
	ctx := context.Background()
	vm := provision(t, s)
	if got, err := s.Restart(ctx, vm.DatabaseID); err != nil || got.State != StateParked {
		t.Fatalf("restart of a parked database = %v, %v; want it left parked", got.State, err)
	}
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	vmm.mu.Lock()
	stops, starts := countCalls(vmm.calls, "stop"), countCalls(vmm.calls, "start")
	vmm.mu.Unlock()
	if got, err := s.Restart(ctx, vm.DatabaseID); err != nil || got.State != StateRunning {
		t.Fatalf("restart = %v, %v", got.State, err)
	}
	vmm.mu.Lock()
	stopped, started := countCalls(vmm.calls, "stop")-stops, countCalls(vmm.calls, "start")-starts
	vmm.mu.Unlock()
	if stopped != 1 || started != 1 {
		t.Fatalf("restart stopped %d and started %d VMs, want 1 and 1", stopped, started)
	}
	if err := store.Transition(ctx, vm.DatabaseID, []State{StateRunning}, StateFailed, TransitionOptions{LastError: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Restart(ctx, vm.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("restart of a failed database = %v, want ErrConflict", err)
	}
}

// A standby low on space while its primary is not is counted: it fills
// first, and a full standby ends the primary's failover. Both low is the
// primary's guard at work, and only the primary counts.
func TestSampleStorageCountsStandbysLowerThanTheirPrimary(t *testing.T) {
	s, _, vmm := newStandbyScheduler(t, "db-a", "db-b")
	s.cfg.Metrics = NewMetrics(prometheus.NewRegistry(), "schedd")
	ctx := context.Background()
	diverged, both := wakeReplicated(t, s), wakeReplicated(t, s)
	low := &dbguest.StorageStatus{TotalBytes: 1 << 30, FreeBytes: 150 << 20, Low: true}
	vmm.mu.Lock()
	vmm.storage[diverged.DatabaseID] = &dbguest.StorageStatus{TotalBytes: 1 << 30, FreeBytes: 700 << 20}
	vmm.standbyStorage[diverged.DatabaseID] = low
	vmm.storage[both.DatabaseID] = &dbguest.StorageStatus{TotalBytes: 1 << 30, FreeBytes: 100 << 20, Low: true, ReadOnly: true}
	vmm.standbyStorage[both.DatabaseID] = low
	vmm.mu.Unlock()

	got, err := s.SampleStorage(ctx)
	if err != nil || got != (StorageSample{Sampled: 2, Low: 1, ReadOnly: 1, StandbyLow: 1}) {
		t.Fatalf("sample = %+v, %v; want one standby low below a primary that is not", got, err)
	}
	if v := testutil.ToFloat64(s.cfg.Metrics.storage.WithLabelValues("standby_low")); v != 1 {
		t.Fatalf("standby_low = %v", v)
	}
}
