// adr: 20261031190030 — frozen VMs yield their memory to admission: the
// reaper parks them on nodes compression cannot relieve, and a wake refused
// for RAM parks them before any running instance.
package sched_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// yieldFleet is a compressFleet whose frozen parks are recorded and release
// the reservation, as a real park does.
func yieldFleet(t *testing.T) (*compressFleet, *[]string) {
	t.Helper()
	f := newCompressFleet(t)
	parks := &[]string{}
	*f.e.FrozenYieldOne() = func(_ context.Context, id, reason string) bool {
		*parks = append(*parks, id+":"+reason)
		f.e.Ledger().Release(id)
		return true
	}
	return f, parks
}

func TestFrozenYieldParksWhenTheNodeCannotCompress(t *testing.T) {
	f, parks := yieldFleet(t)
	f.outcome = "unavailable"
	for i := range 6 { // 6 x 150 MB = 900 MB, 90% of the ceiling
		f.freeze(t, fmt.Sprintf("v%d", i), 150)
		f.now = f.now.Add(time.Second)
	}
	f.tick(2 * time.Minute)
	// 900 -> 750 after v0 (not under 750), -> 600 after v1.
	if want := []string{"v0:admission", "v1:admission"}; !slices.Equal(*parks, want) {
		t.Fatalf("parked %v, want %v", *parks, want)
	}
}

func TestFrozenYieldWaitsWhileCompressionHasVMsToTry(t *testing.T) {
	f, parks := yieldFleet(t)
	f.outcome = "insufficient" // tried, but nothing freed
	for i := range 6 {
		f.freeze(t, fmt.Sprintf("v%d", i), 150)
	}
	f.tick(2 * time.Minute)
	if len(*parks) != 0 {
		t.Fatalf("parked %v while %d VMs were still untried", *parks, 6-api.FrozenCompressPerNodePerTick)
	}
	f.tick(10 * time.Second) // the last two are tried; compression is exhausted
	if len(*parks) == 0 {
		t.Fatal("nothing parked once compression had tried every VM")
	}
	if (*parks)[0] != "v0:admission" {
		t.Fatalf("parked %v first, want the longest frozen, v0", (*parks)[0])
	}
}

func TestFrozenYieldSparesRecentlyFrozenVMs(t *testing.T) {
	f, parks := yieldFleet(t)
	f.outcome = "unavailable"
	for i := range 6 {
		f.freeze(t, fmt.Sprintf("v%d", i), 150)
	}
	f.tick(time.Duration(api.FrozenCompressMinAgeSeconds-1) * time.Second)
	if len(*parks) != 0 {
		t.Fatalf("parked %v, all frozen under %d s ago", *parks, api.FrozenCompressMinAgeSeconds)
	}
}

func TestFrozenYieldLimitsEachTick(t *testing.T) {
	f, parks := yieldFleet(t)
	f.outcome = "unavailable"
	for i := range 100 { // 100 x 9 MB = 900 MB; 17 parks bring it under 750
		f.freeze(t, fmt.Sprintf("v%02d", i), 9)
		f.now = f.now.Add(time.Second)
	}
	f.tick(2 * time.Minute)
	if len(*parks) != api.FrozenYieldPerNodePerTick {
		t.Fatalf("parked %d in one tick, want %d", len(*parks), api.FrozenYieldPerNodePerTick)
	}
}

// refusedWake builds a real RAM refusal: the ledger refuses a request larger
// than the node's headroom.
func refusedWake(t *testing.T, f *compressFleet, ramMB int) error {
	t.Helper()
	err := f.e.Ledger().Admit(sched.Request{Instance: "wake", AppID: "waker", Plan: api.PlanScale, RAMMB: ramMB, VCPU: 2,
		CPUMillicores: 1000, MaxConcurrency: 4, NodeID: "n1", NodeCeilingMB: 1000})
	if err == nil {
		t.Fatal("the ledger admitted the request; the test needs a RAM refusal")
	}
	return err
}

func TestRefusedWakeParksFrozenVMsBeforeRunningOnes(t *testing.T) {
	f, parks := yieldFleet(t)
	for i := range 6 { // 900 MB frozen: 100 MB headroom
		f.freeze(t, fmt.Sprintf("v%d", i), 150)
		f.now = f.now.Add(time.Second)
	}
	// 256 + 8 MB lacks 164 MB: the two longest frozen VMs free 300.
	refusal := refusedWake(t, f, 256)
	if !f.e.ParkForCapacity(t.Context(), "waker", sched.TriggerGateway, refusal) {
		t.Fatal("a wake refused for RAM parked nothing, with frozen VMs on the node")
	}
	if want := []string{"v0:refused_wake", "v1:refused_wake"}; !slices.Equal(*parks, want) {
		t.Fatalf("parked %v, want %v", *parks, want)
	}
}

func TestRefusedWakeKeepsFrozenVMsThatCannotCoverIt(t *testing.T) {
	f, parks := yieldFleet(t)
	f.freeze(t, "v0", 150)
	f.freeze(t, "v1", 150) // 300 MB frozen, 700 MB headroom
	refusal := refusedWake(t, f, 2048)
	_ = f.e.ParkForCapacity(t.Context(), "waker", sched.TriggerGateway, refusal)
	if len(*parks) != 0 {
		t.Fatalf("parked %v; 300 MB of frozen VMs cannot admit a 2 GB wake", *parks)
	}
}

func TestFrozenToYieldPicksTheNodeNeedingFewestParks(t *testing.T) {
	l := sched.NewNodeLedger()
	book := func(id, node string, chargeMB int) {
		if err := l.Admit(sched.Request{Instance: id, AppID: "app-" + id, Plan: api.PlanScale, RAMMB: 1024, VCPU: 2,
			CPUMillicores: 1000, MaxConcurrency: 4, NodeID: node, NodeCeilingMB: 1000, Kind: sched.KindFrozen, FrozenChargeMB: chargeMB}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 9 { // n1: 9 x 100 = 900 MB
		book(fmt.Sprintf("a%d", i), "n1", 100)
	}
	for i := range 3 { // n2: 3 x 300 = 900 MB
		book(fmt.Sprintf("b%d", i), "n2", 300)
	}
	node, ids, ok := l.FrozenToYield(400, 8) // each node lacks 300 MB
	if !ok || node != "n2" || len(ids) != 1 {
		t.Fatalf("FrozenToYield = %q %v %v, want one VM on n2", node, ids, ok)
	}
	if _, _, ok := l.FrozenToYield(50, 8); ok {
		t.Fatal("a request both nodes' headroom fits asked for frozen parks")
	}
	if _, _, ok := l.FrozenToYield(1000, 2); ok {
		t.Fatal("two VMs per node cannot free 900 MB, yet FrozenToYield answered")
	}
}

// Without the test hook a yield is an ordinary frozen park: the VM is
// destroyed, the row parks and the reservation goes.
func TestFrozenYieldParksTheFrozenVM(t *testing.T) {
	f := sched.NewFrozenFixture(t, true)
	(*f.E()).EnableFrozenTier()
	if handled, err := (*f.E()).FreezeIdle(t.Context(), (*f.Ins()).ID, 150); err != nil || !handled {
		t.Fatalf("FreezeIdle: %v, %v", handled, err)
	}
	if !(*f.E()).ParkFrozenToYield(t.Context(), (*f.Ins()).ID, "admission") {
		t.Fatal("the frozen VM did not yield")
	}
	row, err := (*f.Store()).InstanceByID(t.Context(), (*f.Ins()).ID)
	if err != nil || row.State != string(state.StateParked) {
		t.Fatalf("row after the yield: %q, %v; want parked", row.State, err)
	}
	if (*f.E()).Ledger().ResidentFor((*f.Ins()).ID) {
		t.Fatal("the yielded VM kept its reservation")
	}
	if (*f.E()).ParkFrozenToYield(t.Context(), (*f.Ins()).ID, "admission") {
		t.Fatal("a VM no longer frozen yielded again")
	}
}
