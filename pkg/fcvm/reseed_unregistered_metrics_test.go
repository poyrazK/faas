package fcvm

// adr: 20261109140000 — vmmd counts the runtimes a guest reports as missed by
// its reseed barrier, on a closed label set.

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/resumetimingproto"
)

func TestObserveReseedUnregistered(t *testing.T) {
	m := NewWakePhaseMetrics()
	m.ObserveReseedUnregistered(resumetimingproto.Timings{ReseedUnregistered: 4, ReseedUnregisteredRuntimes: "bun,node-sidecar,python,ruby"})
	m.ObserveReseedUnregistered(resumetimingproto.Timings{ReseedUnregistered: 1, ReseedUnregisteredRuntimes: "bun"})
	m.ObserveReseedUnregistered(resumetimingproto.Timings{}) // a covered guest
	var nilMetrics *WakePhaseMetrics
	nilMetrics.ObserveReseedUnregistered(resumetimingproto.Timings{ReseedUnregistered: 1, ReseedUnregisteredRuntimes: "bun"})
	for _, tc := range []struct {
		runtime, scope string
		want           float64
	}{
		{"bun", "workload", 2}, {"node", "sidecar", 1}, {"python", "workload", 1},
		{"other", "workload", 1}, {"node", "workload", 0}, {"deno", "workload", 0},
	} {
		if got := testutil.ToFloat64(m.reseedUnregistered.WithLabelValues(tc.runtime, tc.scope)); got != tc.want {
			t.Errorf("%s/%s = %v, want %v", tc.runtime, tc.scope, got, tc.want)
		}
	}
	// The label set is closed: 7 runtimes × 2 scopes, pre-registered.
	if n := testutil.CollectAndCount(m.reseedUnregistered); n != 14 {
		t.Fatalf("series = %d, want 14", n)
	}
}
