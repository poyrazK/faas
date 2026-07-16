package sched

import (
	"errors"
	"fmt"
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
