// spec: §6.2
package sched

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func cpuAdmissionRequest(instance string) Request {
	return Request{
		Instance:            instance,
		AppID:               "app-" + instance,
		Plan:                api.PlanScale,
		RAMMB:               128,
		VCPU:                1,
		CPUMillicores:       1000,
		MaxConcurrency:      1,
		NodeID:              "node-a",
		NodeCeilingMB:       100_000,
		VCPUBudget:          160,
		CPUBudgetMillicores: 4000,
	}
}

func TestAdmit_CPUMillicoreBudgetEnforcedAndReleased(t *testing.T) {
	t.Parallel()
	ledger := NewNodeLedger()
	for i := 0; i < 4; i++ {
		if err := ledger.Admit(cpuAdmissionRequest(fmt.Sprintf("fit-%d", i))); err != nil {
			t.Fatalf("admit %d: %v", i, err)
		}
	}
	if got := ledger.UsedCPUMillicoresForNode("node-a"); got != 4000 {
		t.Fatalf("reserved CPU = %d, want 4000", got)
	}
	if err := ledger.Admit(cpuAdmissionRequest("over")); err == nil {
		t.Fatal("fifth 1000m instance on a 4000m node must be rejected")
	}
	if got := ledger.UsedCPUMillicoresForNode("node-a"); got != 4000 {
		t.Fatalf("failed admit changed reserved CPU to %d, want 4000", got)
	}

	ledger.Release("fit-0")
	if err := ledger.Admit(cpuAdmissionRequest("replacement")); err != nil {
		t.Fatalf("replacement after release: %v", err)
	}
}

func TestAdmit_CPUOvercommitRecoveryAccountsExistingWithoutAllowingGrowth(t *testing.T) {
	t.Parallel()
	ledger := NewNodeLedger()
	for i := 0; i < 5; i++ {
		req := cpuAdmissionRequest(fmt.Sprintf("existing-%d", i))
		req.AllowCPUOvercommitRecovery = true
		if err := ledger.Admit(req); err != nil {
			t.Fatalf("recover existing %d: %v", i, err)
		}
	}
	if got := ledger.UsedCPUMillicoresForNode("node-a"); got != 5000 {
		t.Fatalf("recovered reserved CPU = %d, want 5000", got)
	}
	if err := ledger.Admit(cpuAdmissionRequest("new-while-over")); err == nil {
		t.Fatal("normal admission must remain blocked while recovered node is over budget")
	}

	ledger.Release("existing-0")
	ledger.Release("existing-1")
	if err := ledger.Admit(cpuAdmissionRequest("new-after-drain")); err != nil {
		t.Fatalf("admission after node drains below budget: %v", err)
	}
}

func TestAdmit_CPUMillicoreZeroBudgetPreservesLegacySeam(t *testing.T) {
	t.Parallel()
	ledger := NewNodeLedger()
	req := cpuAdmissionRequest("legacy")
	req.CPUBudgetMillicores = 0
	if err := ledger.Admit(req); err != nil {
		t.Fatalf("legacy request without physical CPU budget: %v", err)
	}
	if got := ledger.UsedCPUMillicoresForNode("node-a"); got != 1000 {
		t.Fatalf("legacy request still must be accounted: got %d, want 1000", got)
	}
}
