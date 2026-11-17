// adr: 366
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// The vCPU and CPU budgets a flexible job is held to keep one vCPU and one
// default app's CPU back for app wakes (ADR-366). They are counted, not
// refused (ADR-20261109172421), so the gate is read through XCPUOverBudget:
// a flexible job may fill the budget up to the reserve and no further, and
// standard work may use the reserve.
func TestFlexibleJobCPUBudgetsKeepTheReserve(t *testing.T) {
	const vcpuBudget, cpuBudget = 4, 4000
	req := func(vcpu, cpu int, flexible bool) sched.Request {
		return sched.Request{Instance: "job", NodeID: "node-1", Plan: api.PlanScale, RAMMB: 128,
			VCPU: vcpu, CPUMillicores: cpu, Kind: sched.KindJob, Flexible: flexible,
			NodeCeilingMB: 100_000, VCPUBudget: vcpuBudget, CPUBudgetMillicores: cpuBudget}
	}
	ledger := sched.NewNodeLedger()
	cpuFit := cpuBudget - api.DefaultAppCPUMillicores
	for _, tc := range []struct {
		name      string
		vcpu, cpu int
		flexible  bool
		wantOver  bool
	}{
		{"flexible fills up to the reserve", vcpuBudget - 1, cpuFit, true, false},
		{"flexible vCPU into the reserve", vcpuBudget, cpuFit, true, true},
		{"flexible CPU into the reserve", vcpuBudget - 1, cpuFit + 1, true, true},
		{"standard work uses the vCPU reserve", vcpuBudget, cpuFit, false, false},
		{"standard work uses the CPU reserve", vcpuBudget - 1, cpuBudget, false, false},
	} {
		err := ledger.XCPUOverBudget(req(tc.vcpu, tc.cpu, tc.flexible))
		if (err != nil) != tc.wantOver {
			t.Errorf("%s: over budget = %v, want %v", tc.name, err, tc.wantOver)
		}
	}
}

// A flexible job leaves an app wake's worth of memory free. Its vCPU and CPU
// reserve only count since memory became the only capacity limit
// (ADR-20261109172421), so the RAM reserve binds here: two 600 MB jobs fit
// the node, but not beside the 520 MB reserve.
func TestFlexibleJobLeavesAppWakeReserve(t *testing.T) {
	node := state.ComputeNode{ID: "node-1", Active: true, VPCPUs: 2, VCPUBudget: 2, AdmissionCeilingMB: 1600}
	job := sched.Request{Instance: "job-1", NodeID: node.ID, Plan: api.PlanHobby, RAMMB: 600,
		VCPU: 1, CPUMillicores: 1000, Kind: sched.KindJob, Flexible: true,
		NodeCeilingMB: node.AdmissionCeilingMB, VCPUBudget: node.VCPUBudget, CPUBudgetMillicores: 2000}
	choose := func(usedRAM, usedVCPU, usedCPU int64, flexible bool) error {
		candidate := job
		candidate.Flexible = flexible
		_, err := sched.ChoosePlacementWithCPU([]state.ComputeNode{node},
			map[string]int64{node.ID: usedRAM}, map[string]int64{node.ID: usedVCPU},
			map[string]int64{node.ID: usedCPU}, candidate)
		return err
	}
	if err := choose(0, 0, 0, true); err != nil {
		t.Fatalf("first flexible placement: %v", err)
	}
	if err := choose(608, 1, 1000, true); err == nil {
		t.Fatal("second flexible job consumed the app wake reserve")
	}
	if err := choose(608, 1, 1000, false); err != nil {
		t.Fatalf("standard work should use remaining capacity: %v", err)
	}
	ledger := sched.NewNodeLedger()
	if err := ledger.Admit(job); err != nil {
		t.Fatalf("first flexible admission: %v", err)
	}
	job.Instance = "job-2"
	if err := ledger.Admit(job); err == nil {
		t.Fatal("ledger admitted flexible job into app wake reserve")
	}
	job.Flexible = false
	if err := ledger.Admit(job); err != nil {
		t.Fatalf("standard work should use remaining capacity: %v", err)
	}
}
