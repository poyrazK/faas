// adr: 1027 — schedd samples running databases' storage without waking
// any, and counts the ones the guard turned read-only.
package dbsched

import (
	"context"
	"testing"

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
