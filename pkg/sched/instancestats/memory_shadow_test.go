// adr: 909 — shadow mode sums today's reservations and the measured charges
// over the instances invariant 2 counts, per node.
package instancestats

import (
	"math"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemoryShadow(t *testing.T) {
	instances := []state.Instance{
		{ID: "a", NodeID: "n1", State: string(state.StateRunning), RAMMB: 1024},
		{ID: "b", NodeID: "n1", State: string(state.StateWarm), RAMMB: 512},
		{ID: "c", NodeID: "n1", State: string(state.StateParked), RAMMB: 1024},
		{ID: "d", NodeID: "n2", State: string(state.StateRunning), RAMMB: 256},
		{ID: "e", NodeID: "", State: string(state.StateRunning), RAMMB: 256},
	}
	rows := []InstanceStat{
		{InstanceID: "a", RSSMB: 200},
		{InstanceID: "b", RSSMB: math.NaN()},
		{InstanceID: "c", RSSMB: 100},
	}
	got := memoryShadow(instances, rows)
	want := map[string]NodeMemoryShadow{
		// a: 1032 reserved, 250 measured; b: no sample, 520 both; c parked.
		"n1": {ReservedMB: 1032 + 520, MeasuredMB: 250 + 520},
		// d: no row at all books the full reservation.
		"n2": {ReservedMB: 264, MeasuredMB: 264},
	}
	if len(got) != len(want) {
		t.Fatalf("nodes = %v, want %v", got, want)
	}
	for node, w := range want {
		if got[node] != w {
			t.Errorf("%s = %+v, want %+v", node, got[node], w)
		}
	}
}
