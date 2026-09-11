// spec: §6.2

package sched

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func proReq(instance, app string) Request {
	return Request{Instance: instance, AppID: app, Plan: api.PlanPro, RAMMB: 512, VCPU: 2, MaxConcurrency: 5}
}

func TestAdmitBasic(t *testing.T) {
	l := NewLedger()
	if err := l.Admit(proReq("i1", "app1")); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if l.ResidentRAM() != 512+api.PerVMOverheadMB {
		t.Errorf("resident = %d, want %d", l.ResidentRAM(), 512+api.PerVMOverheadMB)
	}
	if l.Concurrency("app1") != 1 {
		t.Errorf("concurrency = %d, want 1", l.Concurrency("app1"))
	}
	want := api.RAMAdmissionCeilingMB - l.ResidentRAM()
	if got := l.HeadroomMB(); got != want {
		t.Errorf("HeadroomMB = %d, want %d (ceiling - resident)", got, want)
	}
}

func TestHeadroomMB_EmptyLedger(t *testing.T) {
	l := NewLedger()
	if got := l.HeadroomMB(); got != api.RAMAdmissionCeilingMB {
		t.Errorf("HeadroomMB on empty = %d, want %d", got, api.RAMAdmissionCeilingMB)
	}
}

func TestAdmitEnforcesConcurrency(t *testing.T) {
	l := NewLedger()
	// Pro allows 5 concurrent; the app is configured to 2.
	for i := 0; i < 2; i++ {
		r := proReq(fmt.Sprintf("i%d", i), "app1")
		r.MaxConcurrency = 2
		if err := l.Admit(r); err != nil {
			t.Fatalf("admit %d: %v", i, err)
		}
	}
	r := proReq("i3", "app1")
	r.MaxConcurrency = 2
	err := l.Admit(r)
	if err == nil {
		t.Fatal("3rd instance should exceed app concurrency of 2")
	}
	var prob *api.Problem
	if !errors.As(err, &prob) || prob.Code != api.CodePlanLimitConcur {
		t.Errorf("expected plan_limit_concurrency, got %v", err)
	}
	// A different app is unaffected.
	if err := l.Admit(proReq("j1", "app2")); err != nil {
		t.Errorf("other app should still admit: %v", err)
	}
}

func TestAdmitRefusesAtRAMCeiling(t *testing.T) {
	l := NewLedger()
	// Fill to just under the ceiling with 1024 MB Scale instances (1032 each).
	admitted := 0
	for i := 0; ; i++ {
		r := Request{Instance: fmt.Sprintf("i%d", i), AppID: fmt.Sprintf("a%d", i), Plan: api.PlanScale, RAMMB: 1024, VCPU: 1, MaxConcurrency: 20}
		if err := l.Admit(r); err != nil {
			var prob *api.Problem
			if !errors.As(err, &prob) || prob.Code != api.CodeCapacity {
				t.Fatalf("expected capacity refusal, got %v", err)
			}
			break
		}
		admitted++
	}
	// Ceiling 47600 / 1032 = 46.1 → 46 fit, 47th refused.
	if admitted != 46 {
		t.Errorf("admitted %d before refusal, want 46 (47600/1032)", admitted)
	}
	if l.ResidentRAM() > api.RAMAdmissionCeilingMB {
		t.Errorf("resident %d exceeded ceiling %d — INVARIANT §6.2-2 BROKEN", l.ResidentRAM(), api.RAMAdmissionCeilingMB)
	}
}

func TestAdmitRefusesAtVCPUExhaustion(t *testing.T) {
	l := NewLedger()
	// Tiny RAM so RAM never binds; 160 vCPU slots is the limit.
	admitted := 0
	for i := 0; ; i++ {
		r := Request{Instance: fmt.Sprintf("i%d", i), AppID: fmt.Sprintf("a%d", i), Plan: api.PlanFree, RAMMB: 128, VCPU: 2, MaxConcurrency: 1}
		if err := l.Admit(r); err != nil {
			break
		}
		admitted++
	}
	if admitted != api.VCPUSlots/2 {
		t.Errorf("admitted %d, want %d (160 slots / 2 vcpu)", admitted, api.VCPUSlots/2)
	}
}

func TestReleaseFreesResources(t *testing.T) {
	l := NewLedger()
	if err := l.Admit(proReq("i1", "app1")); err != nil {
		t.Fatal(err)
	}
	l.Release("i1")
	if l.ResidentRAM() != 0 || l.Concurrency("app1") != 0 || l.UsedVCPU() != 0 {
		t.Errorf("release did not fully free: ram=%d conc=%d vcpu=%d", l.ResidentRAM(), l.Concurrency("app1"), l.UsedVCPU())
	}
	// The freed slot admits again.
	if err := l.Admit(proReq("i2", "app1")); err != nil {
		t.Errorf("should admit after release: %v", err)
	}
}

func TestBeginSnapshotReleasesConcurrencyNotRAM(t *testing.T) {
	l := NewLedger()
	r := proReq("i1", "app1")
	r.MaxConcurrency = 1
	if err := l.Admit(r); err != nil {
		t.Fatal(err)
	}
	// While snapshotting: still resident (RAM held) but no longer counts for
	// concurrency, so a fresh instance of the same app may start (§6.2-1).
	l.BeginSnapshot("i1")
	if l.ResidentRAM() != 512+api.PerVMOverheadMB {
		t.Errorf("RAM should still be held during snapshot: %d", l.ResidentRAM())
	}
	if l.Concurrency("app1") != 0 {
		t.Errorf("concurrency should drop during snapshot: %d", l.Concurrency("app1"))
	}
	r2 := proReq("i2", "app1")
	r2.MaxConcurrency = 1
	if err := l.Admit(r2); err != nil {
		t.Errorf("a replacement instance should admit while the old one snapshots: %v", err)
	}
	// Now both hold RAM.
	if l.ResidentRAM() != 2*(512+api.PerVMOverheadMB) {
		t.Errorf("both instances should be resident: %d", l.ResidentRAM())
	}
}

func TestAdmitRejectsDuplicate(t *testing.T) {
	l := NewLedger()
	if err := l.Admit(proReq("i1", "app1")); err != nil {
		t.Fatal(err)
	}
	if err := l.Admit(proReq("i1", "app1")); err == nil {
		t.Error("admitting the same instance twice should error")
	}
}

// TestConcurrentAdmitReleaseNoCorruption stresses the ledger under concurrency;
// with -race this guards the accounting against data races and drift.
func TestConcurrentAdmitReleaseNoCorruption(t *testing.T) {
	l := NewLedger()
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inst := fmt.Sprintf("i%d", i)
			r := Request{Instance: inst, AppID: fmt.Sprintf("a%d", i%10), Plan: api.PlanHobby, RAMMB: 256, VCPU: 1, MaxConcurrency: 100}
			if err := l.Admit(r); err == nil {
				l.Release(inst)
			}
		}(i)
	}
	wg.Wait()
	// Everything released → back to zero, no drift.
	if l.ResidentRAM() != 0 || l.UsedVCPU() != 0 {
		t.Errorf("ledger drifted after concurrent admit/release: ram=%d vcpu=%d", l.ResidentRAM(), l.UsedVCPU())
	}
}

// Tier A5 / ADR-066: KindMigration reservations count toward
// per-node RAM + vCPU ceilings (invariant §6.2-2 re-stated
// per-node) but NOT per-app concurrency (§6.2-1). The migration
// target was already counted on the source node, so bumping on
// the destination would double-count and briefly cap an app
// that's mid-migration.
//
// The test pins: (a) Plan is allowed to be zero-valued for
// KindMigration (admission.go Admit skips api.LimitsFor on this
// branch); (b) per-node RAM is reserved; (c) per-app concurrency
// stays at zero for the migration target; (d) Release frees both
// the per-node RAM and the per-app counter (which is zero, but
// Release must still walk the bookkeeping without crashing).
func TestAdmitMigrationKindSkipsPerAppConcurrency(t *testing.T) {
	l := NewLedger()
	destNode := "node-b"
	if err := l.Admit(Request{
		Instance: "mig-1",
		RAMMB:    512,
		VCPU:     2,
		Kind:     KindMigration,
		NodeID:   destNode,
		// AppID + Plan deliberately left zero to exercise the
		// KindMigration short-circuit in admission.go.
	}); err != nil {
		t.Fatalf("admit migration: %v", err)
	}
	// Per-node RAM reserved (512 + PerVMOverheadMB).
	wantRAM := 512 + api.PerVMOverheadMB
	if got := l.ResidentRAMForNode(destNode); got != wantRAM {
		t.Errorf("ResidentRAMForNode(%q) = %d, want %d (Tier A5 must reserve destination RAM)", destNode, got, wantRAM)
	}
	// Per-node vCPU reserved.
	if got := l.UsedVCPUForNode(destNode); got != 2 {
		t.Errorf("UsedVCPUForNode(%q) = %d, want 2 (Tier A5 must reserve destination vCPU)", destNode, got)
	}
	// Per-app concurrency NOT bumped (KindMigration skips §6.2-1
	// to avoid double-counting across the source + destination).
	if got := l.Concurrency(""); got != 0 {
		t.Errorf("Concurrency(\"\") = %d, want 0 (KindMigration must skip per-app bump)", got)
	}
	// Release frees everything; subsequent per-node reads return
	// zero and the per-node bucket is dropped (Release's
	// residentRAM==0 && usedVCPU==0 GC at admission.go:280-282).
	l.Release("mig-1")
	if got := l.ResidentRAMForNode(destNode); got != 0 {
		t.Errorf("post-release ResidentRAMForNode(%q) = %d, want 0", destNode, got)
	}
	if got := l.UsedVCPUForNode(destNode); got != 0 {
		t.Errorf("post-release UsedVCPUForNode(%q) = %d, want 0", destNode, got)
	}
	if l.NodeCount() != 0 {
		t.Errorf("post-release NodeCount = %d, want 0 (empty per-node bucket should be GC'd)", l.NodeCount())
	}
}

// Tier A5 / ADR-066: a KindMigration reservation cannot exceed the
// destination node's RAM ceiling. Two migrations sized at the plan
// cap that fit in isolation but overflow the ceiling together must
// be rejected; this pins that invariant §6.2-2 still fires for the
// migration path (KindMigration skips per-app concurrency, NOT RAM).
func TestAdmitMigrationKindHonoursRAMCeiling(t *testing.T) {
	l := NewLedger()
	destNode := "node-b"
	// Use a tight per-node ceiling so the test is hermetic.
	ceiling := 512 + api.PerVMOverheadMB
	// First migration fits.
	if err := l.Admit(Request{
		Instance: "mig-1", RAMMB: 512, VCPU: 1, Kind: KindMigration,
		NodeID: destNode, NodeCeilingMB: ceiling,
	}); err != nil {
		t.Fatalf("first migration admit: %v", err)
	}
	// Second one would overflow.
	err := l.Admit(Request{
		Instance: "mig-2", RAMMB: 512, VCPU: 1, Kind: KindMigration,
		NodeID: destNode, NodeCeilingMB: ceiling,
	})
	if err == nil {
		t.Fatalf("second migration admit: want ErrCapacity, got nil")
	}
	var prob *api.Problem
	if !errors.As(err, &prob) || prob.Code != api.CodeCapacity {
		t.Errorf("second migration: want CodeCapacity, got %v", err)
	}
}

// A snapshot prime is a replacement reservation: the old revision remains
// serving until the new revision has been booted and captured. It must still
// consume node RAM/vCPU, but it must not consume the app's serving
// max_concurrency slot.
func TestAdmitSnapshotPrimeKindSkipsServingConcurrency(t *testing.T) {
	l := NewLedger()
	request := Request{
		AppID: "app-1", Plan: api.PlanScale, RAMMB: 1024, VCPU: 4,
		MaxConcurrency: 1, NodeID: "node-a", NodeCeilingMB: 100000,
		VCPUBudget: 160,
	}
	request.Instance = "old-live"
	if err := l.Admit(request); err != nil {
		t.Fatalf("admit old live instance: %v", err)
	}
	request.Instance = "replacement-prime"
	request.Kind = KindSnapshotPrime
	if err := l.Admit(request); err != nil {
		t.Fatalf("admit snapshot prime alongside maxed app: %v", err)
	}
	if got := l.Concurrency(request.AppID); got != 1 {
		t.Fatalf("serving concurrency = %d, want 1 (prime must not bump it)", got)
	}
	if got := l.ResidentRAMForNode(request.NodeID); got != 2*(request.RAMMB+api.PerVMOverheadMB) {
		t.Fatalf("resident RAM = %d, want both live and prime reservations", got)
	}
	l.Release("replacement-prime")
	if got := l.Concurrency(request.AppID); got != 1 {
		t.Fatalf("serving concurrency after prime release = %d, want 1", got)
	}
}

// Tier A5 / ADR-066: a KindWake reservation with Plan unset still
// fails fast (the existing pre-Tier-A5 contract). Pinning this
// guards the KindMigration branch from being copy-pasted and
// accidentally swallowing the Plan validation for the wake path.
func TestAdmitWakeKindRequiresKnownPlan(t *testing.T) {
	l := NewLedger()
	err := l.Admit(Request{
		Instance: "i1", AppID: "app1",
		RAMMB: 256, VCPU: 1, MaxConcurrency: 5,
		// Plan deliberately empty.
		Kind: KindWake,
	})
	if err == nil {
		t.Fatalf("admit wake with empty Plan: want error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown plan") {
		t.Errorf("admit wake: want 'unknown plan' error, got %v", err)
	}
}
