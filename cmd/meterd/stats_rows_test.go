package main

// adr: 20261104090000

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/scheddgrpc"
)

// TestPreferLiveStatsRows pins ADR-20261104090000: a live-migrated instance's
// departed row on the node it left never shadows its live row on the new
// node, whatever order the fleet merge returns them in, and two departed
// readings for one instance are both dropped.
func TestPreferLiveStatsRows(t *testing.T) {
	oldNode := scheddgrpc.InstanceStatsRow{InstanceID: "vm-moved", NodeID: "node-a", NetTxBytes: 9_000_000, Departed: true}
	newNode := scheddgrpc.InstanceStatsRow{InstanceID: "vm-moved", NodeID: "node-b", NetTxBytes: 4_000}
	parked := scheddgrpc.InstanceStatsRow{InstanceID: "vm-parked", NodeID: "node-a", NetTxBytes: 700, Departed: true}
	twiceA := scheddgrpc.InstanceStatsRow{InstanceID: "vm-twice", NodeID: "node-a", NetTxBytes: 50, Departed: true}
	twiceB := scheddgrpc.InstanceStatsRow{InstanceID: "vm-twice", NodeID: "node-b", NetTxBytes: 80, Departed: true}
	for _, order := range [][]scheddgrpc.InstanceStatsRow{
		{oldNode, newNode, parked, twiceA, twiceB},
		{newNode, oldNode, twiceB, parked, twiceA},
	} {
		got := preferLiveStatsRows(order)
		if row := got["vm-moved"]; row.Departed || row.NodeID != "node-b" || row.NetTxBytes != 4_000 {
			t.Fatalf("moved instance = %+v, want the new node's live row", row)
		}
		if row, ok := got["vm-parked"]; !ok || row.NetTxBytes != 700 {
			t.Fatalf("parked instance = %+v (%v), want its one departed reading", row, ok)
		}
		if row, ok := got["vm-twice"]; ok {
			t.Fatalf("instance with two departed readings kept %+v, want none", row)
		}
	}
}

// TestSourceFinalStatsRows pins ADR-20261122090000: the departed row of a
// live-migrated instance is kept beside its live row as the source node's
// final reading; a departed row with no live row, or from the live row's own
// node, is not a migration source.
func TestSourceFinalStatsRows(t *testing.T) {
	oldNode := scheddgrpc.InstanceStatsRow{InstanceID: "vm-moved", NodeID: "node-a", NetTxBytes: 9_000_000, Departed: true}
	newNode := scheddgrpc.InstanceStatsRow{InstanceID: "vm-moved", NodeID: "node-b", NetTxBytes: 4_000}
	parked := scheddgrpc.InstanceStatsRow{InstanceID: "vm-parked", NodeID: "node-a", NetTxBytes: 700, Departed: true}
	restarted := scheddgrpc.InstanceStatsRow{InstanceID: "vm-restarted", NodeID: "node-a", NetTxBytes: 10, Departed: true}
	restartedLive := scheddgrpc.InstanceStatsRow{InstanceID: "vm-restarted", NodeID: "node-a", NetTxBytes: 20}
	rows := []scheddgrpc.InstanceStatsRow{oldNode, newNode, parked, restarted, restartedLive}
	got := sourceFinalStatsRows(rows, preferLiveStatsRows(rows))
	if row, ok := got["vm-moved"]; !ok || row.NodeID != "node-a" || row.NetTxBytes != 9_000_000 {
		t.Fatalf("moved instance source = %+v (%v), want node-a's final reading", row, ok)
	}
	if len(got) != 1 {
		t.Fatalf("source rows = %+v, want only the migrated instance", got)
	}
}
