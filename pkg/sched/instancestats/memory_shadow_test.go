// adr: 909 — shadow mode sums today's reservations and the measured charges
// over the instances invariant 2 counts, per node.
package instancestats

import (
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
	got := memoryShadow(instances, rows, nil)
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

func TestReaderMeasuredMB(t *testing.T) {
	r := NewReader()
	now := time.Unix(1_800_000_000, 0)
	if _, _, ok := r.MeasuredMB("n1", now); ok {
		t.Fatal("measurement before the first tick")
	}
	p := &Poller{Reader: r}
	p.publishMemoryShadow([]state.Instance{
		{ID: "a", NodeID: "n1", State: string(state.StateRunning), RAMMB: 1024},
	}, []InstanceStat{{InstanceID: "a", RSSMB: 200}}, now)
	maxAge := time.Duration(api.MeasuredAdmissionSampleMaxAgeMillis) * time.Millisecond
	cases := []struct {
		name   string
		node   string
		at     time.Time
		wantMB int
		wantOK bool
	}{
		{"fresh", "n1", now.Add(maxAge), 250, true},
		{"stale", "n1", now.Add(maxAge + time.Millisecond), 0, false},
		{"unmeasured node", "n2", now, 0, false},
	}
	for _, tc := range cases {
		mb, asOf, ok := r.MeasuredMB(tc.node, tc.at)
		if mb != tc.wantMB || ok != tc.wantOK || (ok && !asOf.Equal(now)) {
			t.Errorf("%s: MeasuredMB = %d, %v, %v; want %d, %v, %v", tc.name, mb, asOf, ok, tc.wantMB, now, tc.wantOK)
		}
	}
}

// adr: 20261017 connection survival, §2 memory (the ADR-909 amendment):
// a node's instance bridges add to its measured charge and reserve nothing.
func TestMemoryShadowChargesInstanceBridges(t *testing.T) {
	instances := []state.Instance{{ID: "a", NodeID: "n1", State: string(state.StateRunning), RAMMB: 1024}}
	rows := []InstanceStat{{InstanceID: "a", RSSMB: 200}}
	got := memoryShadow(instances, rows, map[string]int{"n1": 40, "n2": 12, "": 9, "n3": -5})
	if got["n1"] != (NodeMemoryShadow{ReservedMB: 1032, MeasuredMB: 250 + 40}) {
		t.Fatalf("n1 = %+v", got["n1"])
	}
	if got["n2"] != (NodeMemoryShadow{MeasuredMB: 12}) {
		t.Fatalf("n2 (bridges only) = %+v", got["n2"])
	}
	if _, ok := got[""]; ok {
		t.Fatal("charged bridges to no node")
	}
	if _, ok := got["n3"]; ok {
		t.Fatal("charged a negative bridge sample")
	}
}
