// adr: 366
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

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
