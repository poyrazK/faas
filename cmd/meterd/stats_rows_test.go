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
