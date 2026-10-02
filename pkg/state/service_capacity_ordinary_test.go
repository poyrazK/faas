// adr: 464
package state

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Independently enumerate legacy final allocations and remove every host.
// Existing survivors stay fixed; ordinary resource growth only shrinks slots.
func exhaustiveOrdinaryCapacityFits(nodes map[string]ServiceCapacitySlots, pinned map[string]int64, legacyTarget int64) bool {
	var fleet, largest, pinnedTotal, legacyCapacity int64
	byRegion := make(map[string]map[string]ServiceCapacitySlots)
	for id, node := range nodes {
		if node.Slots < node.Used {
			return false
		}
		fleet += node.Slots
		largest = max(largest, node.Slots)
		if byRegion[node.Region] == nil {
			byRegion[node.Region] = make(map[string]ServiceCapacitySlots)
		}
		byRegion[node.Region][id] = node
	}
	for region, rows := range byRegion {
		pinnedTotal += pinned[region]
		for failed := range rows {
			var free int64
			for id, node := range rows {
				if id != failed {
					free += node.Slots - node.LegacyUsed
				}
			}
			if free < pinned[region] {
				return false
			}
		}
		legacyCapacity += exhaustiveRegionalLegacyCapacity(rows, pinned[region])
	}
	return pinnedTotal+legacyTarget <= fleet-largest && legacyTarget <= legacyCapacity
}

func TestOrdinaryCapacityPlacementExhaustive(t *testing.T) {
	random := rand.New(rand.NewSource(438))
	checked := 0
	for scenario := range 1500 {
		nodes := make(map[string]ServiceCapacitySlots)
		pinned := make(map[string]int64)
		var fleet, largest, legacyTarget, pinnedTotal int64
		for _, region := range []string{"", "eu-fsn", "us-ash"} {
			for i := range 2 {
				slots := int64(2 + random.Intn(3))
				legacy := int64(random.Intn(2))
				residentPinned := int64(0)
				if region != "" {
					residentPinned = int64(random.Intn(2))
				}
				nodes[fmt.Sprintf("%s-%d", region, i)] = ServiceCapacitySlots{Slots: slots, Used: legacy + residentPinned, LegacyUsed: legacy, Region: region, Fits: true,
					RAMBudgetMB: slots*10 + int64(random.Intn(10)), CPUBudgetMillicores: slots*10 + int64(random.Intn(10)), VCPUBudget: slots*2 + int64(random.Intn(2))}
				pinned[region] += residentPinned
				legacyTarget += legacy
				fleet += slots
				largest = max(largest, slots)
			}
			if region != "" {
				pinned[region] += int64(random.Intn(2))
			}
			pinnedTotal += pinned[region]
		}
		legacyTarget += int64(random.Intn(4))
		if !exhaustiveOrdinaryCapacityFits(nodes, pinned, legacyTarget) {
			continue
		}
		report := api.ServiceCapacityProtection{Enabled: true, State: "protected", ReplicaRAMMB: 10, ReplicaCPUMillicores: 10, ReplicaVCPU: 2, FleetSlots: fleet, FailoverSlots: fleet - largest, ReservedReplicas: pinnedTotal + legacyTarget, LegacyReservedReplicas: legacyTarget}
		for _, region := range []string{"eu-fsn", "us-ash"} {
			report.Regions = append(report.Regions, api.ServiceRegionCapacityProtection{ComputeRegion: region, ReservedReplicas: pinned[region]})
		}
		placement := newServiceCapacityPlacement(report, nodes, true)
		for id, node := range nodes {
			ram, cpu, vcpu := int64(1+random.Intn(31)), int64(1+random.Intn(31)), int64(1+random.Intn(7))
			budgetFit := ram <= node.RAMBudgetMB && cpu <= node.CPUBudgetMillicores && vcpu <= node.VCPUBudget
			next := make(map[string]ServiceCapacitySlots, len(nodes))
			for key, row := range nodes {
				next[key] = row
			}
			row := node
			row.Slots = min((node.RAMBudgetMB-ram)/10, (node.CPUBudgetMillicores-cpu)/10, (node.VCPUBudget-vcpu)/2)
			next[id] = row
			want := budgetFit && exhaustiveOrdinaryCapacityFits(next, pinned, legacyTarget)
			if got := placement.AllowsOrdinary(id, ram, cpu, vcpu); got != want {
				t.Fatalf("scenario=%d node=%s cost=%d/%d/%d nodes=%+v pinned=%v legacy=%d hint=%t want=%t", scenario, id, ram, cpu, vcpu, nodes, pinned, legacyTarget, got, want)
			}
			checked++
		}
	}
	if checked < 1000 {
		t.Fatalf("too few protected prospective cases: %d", checked)
	}
}

func TestOrdinaryCapacityPlacementMissingInputsAndDegradation(t *testing.T) {
	if !(ServiceCapacityPlacement{}).AllowsOrdinary("legacy", 136, 1000, 1) {
		t.Fatal("disabled policy changed placement")
	}
	for _, state := range []string{"protected", "degraded", "needs_hardware"} {
		p := newServiceCapacityPlacement(api.ServiceCapacityProtection{Enabled: true, State: state}, nil, false)
		if p.AllowsOrdinary("node", 136, 1000, 1) {
			t.Fatalf("missing ordinary inputs admitted in %s", state)
		}
	}
}
