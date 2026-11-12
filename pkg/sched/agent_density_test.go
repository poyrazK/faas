// adr: 20261027090015 §3, 20261027090014 §2, 20261109172421 — how many Pro
// agent runs one 16 CPU / 64 GB agent node admits, with today's switches and
// with measured charge at a range of version peaks. CPU decides where, never
// whether (ADR-20261109172421), so RAM is the only gate that binds and the
// runs' CPU budget is past it long before RAM fills.
package sched_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestAgentNodeDensity(t *testing.T) {
	sizing := api.DeriveNodeSizingForRole(64<<10, 16, api.NodeShapeComputeOnly)
	cpuBudget := sizing.TenantCPUs * 1000 * api.CPUOvercommit
	// A Pro run: the plan's largest RAM and vCPUs, as agentd sizes it.
	ram, vcpu := api.AgentRunRAMMBMax[api.PlanPro.PlanIndex()], api.AgentRunVCPUMax[api.PlanPro.PlanIndex()]
	for _, tc := range []struct {
		name   string
		charge bool
		peakMB int
		want   int
	}{
		{name: "configured RAM", want: 24},
		{name: "measured charge, no history yet", charge: true, want: 24},
		{name: "measured charge, peak 1 GiB", charge: true, peakMB: 1024, want: 48},
		{name: "measured charge, peak 300 MB", charge: true, peakMB: 300, want: 161},
		{name: "measured charge, peak 120 MB", charge: true, peakMB: 120, want: 388},
		{name: "measured charge, peak 64 MB", charge: true, peakMB: 64, want: 690},
	} {
		t.Run(tc.name, func(t *testing.T) {
			memory := &fakeMeasurement{mb: map[string]int{"agents-1": 0}, ok: true}
			l, clock := newMeasuredLedger(memory)
			if tc.charge {
				l.EnableAgentMeasuredCharge((&fakeAgentMemory{ok: true}).read)
			}
			admitted := 0
			var refusal error
			for ; admitted < 5000 && refusal == nil; admitted++ {
				// A fresh stats tick sees every run admitted so far.
				clock.tick()
				memory.asOf = clock.now()
				clock.tick()
				refusal = l.Admit(sched.Request{Instance: fmt.Sprintf("agent-%d", admitted), Plan: api.PlanPro, RAMMB: ram,
					VCPU: vcpu, CPUMillicores: vcpu * 1000, NodeID: "agents-1", NodeCeilingMB: sizing.AdmissionCeilingMB,
					VCPUBudget: sizing.VCPUSlots, CPUBudgetMillicores: cpuBudget, Kind: sched.KindAgentRun, AgentPeakMB: tc.peakMB})
			}
			admitted--
			t.Logf("%d runs (%d vCPU against a %d vCPU budget), then: %v", admitted, admitted*vcpu, sizing.VCPUSlots, refusal)
			if admitted != tc.want || refusal == nil || !strings.Contains(refusal.Error(), "RAM headroom") {
				t.Fatalf("admitted %d runs then %v; want %d, then the RAM gate", admitted, refusal, tc.want)
			}
		})
	}
}
