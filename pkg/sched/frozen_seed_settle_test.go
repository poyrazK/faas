// adr: 1151, 20261031190030 — a frozen VM re-booked after a restart takes its
// first measured charge in place of the stand-in.
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestSeededFrozenChargeSettlesOnce(t *testing.T) {
	l := sched.NewNodeLedger()
	if err := l.Admit(sched.Request{Instance: "f1", AppID: "a1", Plan: api.PlanScale, RAMMB: 1024, VCPU: 2,
		CPUMillicores: 1000, MaxConcurrency: 4, NodeID: "n1", NodeCeilingMB: 8000, Kind: sched.KindFrozen,
		FrozenChargeMB: api.MeasuredChargeFloorMB}); err != nil {
		t.Fatal(err)
	}
	// A 240 MB VM measured after the restart: 300 MB, not the 64 MB stand-in.
	if !l.SettleSeededFrozenCharge("f1", 300) {
		t.Fatal("the seeded reservation did not settle")
	}
	if charge, _ := l.FrozenChargeOf("f1"); charge != 300 || l.ResidentRAMForNode("n1") != 300 {
		t.Fatalf("charge %d MB, node %d MB; want 300 for both", charge, l.ResidentRAMForNode("n1"))
	}
	// Once settled, only compression lowers it.
	if l.SettleSeededFrozenCharge("f1", 20) {
		t.Fatal("a settled reservation settled again")
	}
	if charge, _ := l.FrozenChargeOf("f1"); charge != 300 {
		t.Fatalf("charge %d MB after a second settle, want 300", charge)
	}
	// A reservation frozen live already books its measurement.
	if err := l.Admit(sched.Request{Instance: "r1", AppID: "a2", Plan: api.PlanScale, RAMMB: 1024, VCPU: 2,
		CPUMillicores: 1000, MaxConcurrency: 4, NodeID: "n1", NodeCeilingMB: 8000}); err != nil {
		t.Fatal(err)
	}
	if !l.Freeze("r1", 150) || l.SettleSeededFrozenCharge("r1", 500) {
		t.Fatal("a live-frozen reservation settled as if seeded")
	}
}
