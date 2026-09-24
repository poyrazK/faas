// spec: §6.2
package sched

import (
	"fmt"
	"testing"
	"time"

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

func TestAdmit_StartupCPUBoostIsReservedUntilExpiry(t *testing.T) {
	t.Parallel()
	ledger := NewNodeLedger()
	req := cpuAdmissionRequest("boosted")
	req.CPUMillicores = 250
	req.CPUStartupBoostMillicores = 1000
	req.CPUStartupBoostUntil = time.Now().Add(time.Second)
	if err := ledger.Admit(req); err != nil {
		t.Fatalf("admit boosted reservation: %v", err)
	}
	if got := ledger.UsedCPUMillicoresForNode("node-a"); got != 1000 {
		t.Fatalf("boosted CPU reservation = %d, want peak 1000", got)
	}

	actualExpiry := time.Now().Add(20 * time.Millisecond)
	if !ledger.SetCPUStartupBoostUntil("boosted", actualExpiry) {
		t.Fatal("SetCPUStartupBoostUntil did not find the admitted reservation")
	}
	time.Sleep(time.Until(actualExpiry) + 5*time.Millisecond)
	if got := ledger.UsedCPUMillicoresForNode("node-a"); got != 250 {
		t.Fatalf("expired boost reservation = %d, want sustained 250", got)
	}
	ledger.Release("boosted")
	if got := ledger.UsedCPUMillicoresForNode("node-a"); got != 0 {
		t.Fatalf("released reservation = %d, want 0", got)
	}
}

func TestAdmit_StartupCPUBoostCountsAgainstPhysicalBudget(t *testing.T) {
	t.Parallel()
	ledger := NewNodeLedger()
	req := cpuAdmissionRequest("boost-over-budget")
	req.CPUMillicores = 250
	req.CPUStartupBoostMillicores = 1000
	req.CPUBudgetMillicores = 750
	req.CPUStartupBoostUntil = time.Now().Add(time.Second)
	if err := ledger.Admit(req); err == nil {
		t.Fatal("temporary startup quota must be included in physical CPU admission")
	}
	if got := ledger.UsedCPUMillicoresForNode("node-a"); got != 0 {
		t.Fatalf("failed boosted admit reserved %d millicores, want 0", got)
	}
}

func TestStartupCPUBoostQuotaMatchesVMMDProfile(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		if got := startupCPUBoostQuota(plan, 250); got != api.DefaultAppCPUMillicores {
			t.Errorf("startupCPUBoostQuota(%s, 250) = %d, want %d", plan, got, api.DefaultAppCPUMillicores)
		}
		if got := startupCPUBoostQuota(plan, 1000); got != 1000 {
			t.Errorf("startupCPUBoostQuota(%s, 1000) = %d, want configured quota 1000", plan, got)
		}
	}
}
