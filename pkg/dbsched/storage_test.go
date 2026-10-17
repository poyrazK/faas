// adr: 1027 — schedd samples running databases' storage without waking
// any, and counts the ones the guard turned read-only.
package dbsched

import (
	"context"
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
