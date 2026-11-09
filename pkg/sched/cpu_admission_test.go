// adr: 20261109172421

package sched_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func cpuAdmissionNodes() []state.ComputeNode {
	a, b := sched.Node("a-id", "a", 0, 8000), sched.Node("b-id", "b", 0, 8000)
	a.VCPUBudget, b.VCPUBudget = 24, 24
	a.VPCPUs, b.VPCPUs = 3, 3 // 24,000 millicores of physical CPU budget
	return []state.ComputeNode{a, b}
}

// Observing, CPU decides where an instance goes, never whether it runs: a
// node within its CPU budgets is still preferred, a fleet over them is placed
// on memory and counted, and memory still refuses.
func TestPlacementObservingCPUAdmission(t *testing.T) {
	nodes := cpuAdmissionNodes()
	scale := sched.Request{RAMMB: 512, VCPU: 4, CPUMillicores: 1000}
	for _, tc := range []struct {
		name               string
		usedMB, usedVCPU   map[string]int64
		usedCPU            map[string]int64
		r                  sched.Request
		enforceErr         bool
		observeNode        string // "" = refused, "any" = placed on either node
		observeCount       float64
		observeRefusalCode string
	}{
		{
			name:   "a node within its budgets wins either way",
			usedMB: map[string]int64{}, usedVCPU: map[string]int64{"a-id": 24, "b-id": 4}, usedCPU: map[string]int64{},
			r: scale, observeNode: "b-id",
		},
		{
			name:   "every node at its vCPU budget",
			usedMB: map[string]int64{"a-id": 2000, "b-id": 3000}, usedVCPU: map[string]int64{"a-id": 24, "b-id": 24}, usedCPU: map[string]int64{},
			r: scale, enforceErr: true, observeNode: "any", observeCount: 1,
		},
		{
			name:   "every node at its CPU budget",
			usedMB: map[string]int64{}, usedVCPU: map[string]int64{}, usedCPU: map[string]int64{"a-id": 24000, "b-id": 24000},
			r: scale, enforceErr: true, observeNode: "any", observeCount: 1,
		},
		{
			name:   "memory still refuses",
			usedMB: map[string]int64{"a-id": 7900, "b-id": 7900}, usedVCPU: map[string]int64{"a-id": 24, "b-id": 24}, usedCPU: map[string]int64{},
			r: scale, enforceErr: true, observeRefusalCode: "node_ram",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, enforceErr := sched.XChooseWithCPUAdmission(nodes, tc.usedMB, tc.usedVCPU, tc.usedCPU, tc.r, false)
			if (enforceErr != nil) != tc.enforceErr {
				t.Fatalf("enforce: err = %v, want error %v", enforceErr, tc.enforceErr)
			}
			p, counted, err := sched.XChooseWithCPUAdmission(nodes, tc.usedMB, tc.usedVCPU, tc.usedCPU, tc.r, true)
			if tc.observeNode == "" {
				var problem *api.Problem
				if !errors.As(err, &problem) || problem.Code != api.CodeCapacity {
					t.Fatalf("observe: err = %v, want a capacity refusal", err)
				}
				blockers := sched.AdmissionBlockers(err)
				if len(blockers) == 0 || blockers[0].Code != tc.observeRefusalCode {
					t.Fatalf("observe: blockers = %+v, want %s first", blockers, tc.observeRefusalCode)
				}
				return
			}
			if err != nil || (tc.observeNode != "any" && p.NodeID != tc.observeNode) || p.NodeID == "" || counted != tc.observeCount {
				t.Fatalf("observe: placed %q (err %v), counted %v; want %q, %v", p.NodeID, err, counted, tc.observeNode, tc.observeCount)
			}
		})
	}
}

// Observing, the ledger's vCPU and CPU headroom checks count what they would
// have refused and admit; its memory check still refuses.
func TestLedgerObservingCPUAdmission(t *testing.T) {
	l := sched.NewNodeLedger()
	base := sched.Request{Plan: api.PlanScale, RAMMB: 128, VCPU: 4, MaxConcurrency: 1,
		NodeID: "n", NodeCeilingMB: 4000, VCPUBudget: 8, CPUMillicores: 1000, CPUBudgetMillicores: 2000}
	admit := func(id string, r sched.Request) error {
		r.Instance, r.AppID = id, "app-"+id
		return l.Admit(r)
	}
	for _, id := range []string{"1", "2"} {
		if err := admit(id, base); err != nil {
			t.Fatalf("filler %s: %v", id, err)
		}
	}
	if err := admit("3", base); err == nil || !strings.Contains(err.Error(), "vCPU headroom") {
		t.Fatalf("enforcing: err = %v, want a vCPU headroom refusal", err)
	}
	l.XSetCPUAdmissionObserve(true)
	if err := admit("3", base); err != nil {
		t.Fatalf("observing: %v, want admitted on memory", err)
	}
	if v, c := l.XCPUAdmissionWouldRefuse(sched.CPUGateLedgerVCPU), l.XCPUAdmissionWouldRefuse(sched.CPUGateLedgerCPU); v != 1 || c != 1 {
		t.Fatalf("would refuse: vcpu %v, cpu %v; want 1 and 1", v, c)
	}
	big := base
	big.RAMMB = 4000
	if err := admit("4", big); err == nil || !strings.Contains(err.Error(), "RAM headroom") {
		t.Fatalf("observing: err = %v, want memory to still refuse", err)
	}
}

// Observing, the durable node reservation is asked to check memory only.
func TestDurableGuardObservingCPUAdmissionBooksNoCompute(t *testing.T) {
	store := state.NewMemStore()
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	if got := e.XAdmissionCompute("n", 4, 1000); got.VCPU != 4 || got.CPUMillicores != 1000 {
		t.Fatalf("enforcing: compute = %+v, want 4 vCPU and 1000 m", got)
	}
	if err := e.SetCPUAdmission(api.CPUAdmissionObserve); err != nil {
		t.Fatal(err)
	}
	if got := e.XAdmissionCompute("n", 4, 1000); got != (state.NodeCompute{}) {
		t.Fatalf("observing: compute = %+v, want none", got)
	}
	if err := e.SetCPUAdmission("off"); err == nil {
		t.Fatal(`SetCPUAdmission("off") succeeded, want an error`)
	}
}
