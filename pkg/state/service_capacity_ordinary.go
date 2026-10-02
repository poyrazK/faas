package state

import "github.com/onebox-faas/faas/pkg/api"

func newServiceCapacityPlacement(report api.ServiceCapacityProtection, nodes map[string]ServiceCapacitySlots, ordinaryInputs bool) ServiceCapacityPlacement {
	p := ServiceCapacityPlacement{Enabled: report.Enabled, Nodes: nodes, report: report, ordinaryInputs: ordinaryInputs}
	if !p.Enabled || !ordinaryInputs {
		return p
	}
	p.regions = make(map[string]*regionalServiceCapacity)
	for id, node := range nodes {
		cell := p.regions[node.Region]
		if cell == nil {
			cell = &regionalServiceCapacity{nodes: make(map[string]ServiceCapacitySlots)}
			p.regions[node.Region] = cell
		}
		cell.nodes[id] = node
		cell.FleetSlots += node.Slots
		cell.LegacyResidentReplicas += node.LegacyUsed
		cell.largestFree = max(cell.largestFree, node.Slots-node.LegacyUsed)
	}
	for _, region := range report.Regions {
		if cell := p.regions[region.ComputeRegion]; cell != nil {
			cell.ReservedReplicas = region.ReservedReplicas
		}
	}
	for _, cell := range p.regions {
		cell.LegacyPlacementCapacity = regionalLegacyCapacity(cell.nodes, cell.ReservedReplicas, cell.FleetSlots, cell.LegacyResidentReplicas, cell.largestFree)
		p.totalLegacyCapacity += cell.LegacyPlacementCapacity
	}
	return p
}

// AllowsOrdinary checks whether a new non-service resident preserves the
// current host-loss certificate and a stable placement for pending legacy
// service intent. RAM includes VM overhead and sidecars, CPU includes the
// certificate's conservative configured quota, and VCPU is the guest topology.
// This is a read-only hint; database admission rechecks the actual write.
func (p ServiceCapacityPlacement) AllowsOrdinary(nodeID string, ramMB, cpuMillicores, vcpu int64) bool {
	if !p.Enabled {
		return true
	}
	r := p.report
	if !p.ordinaryInputs || r.State != "protected" || r.ReplicaRAMMB <= 0 || r.ReplicaCPUMillicores <= 0 || r.ReplicaVCPU <= 0 {
		return false
	}
	node, healthy := p.Nodes[nodeID]
	if !healthy || !node.Fits || ramMB <= 0 || cpuMillicores <= 0 || vcpu <= 0 || ramMB > node.RAMBudgetMB || cpuMillicores > node.CPUBudgetMillicores || vcpu > node.VCPUBudget {
		return false
	}
	nextSlots := min((node.RAMBudgetMB-ramMB)/r.ReplicaRAMMB, (node.CPUBudgetMillicores-cpuMillicores)/r.ReplicaCPUMillicores, (node.VCPUBudget-vcpu)/r.ReplicaVCPU)
	if nextSlots < node.Used {
		return false
	}
	if nextSlots == node.Slots {
		return true
	}
	var largest int64
	for id, peer := range p.Nodes {
		slots := peer.Slots
		if id == nodeID {
			slots = nextSlots
		}
		largest = max(largest, slots)
	}
	fleet := r.FleetSlots - node.Slots + nextSlots
	if r.ReservedReplicas > fleet-largest {
		return false
	}
	cell := p.regions[node.Region]
	if cell == nil {
		return false
	}
	nextNodes := make(map[string]ServiceCapacitySlots, len(cell.nodes))
	var largestFree int64
	for id, peer := range cell.nodes {
		if id == nodeID {
			peer.Slots = nextSlots
		}
		nextNodes[id] = peer
		largestFree = max(largestFree, peer.Slots-peer.LegacyUsed)
	}
	cellFleet := cell.FleetSlots - node.Slots + nextSlots
	if cell.ReservedReplicas > cellFleet-cell.LegacyResidentReplicas-largestFree {
		return false
	}
	nextLegacyCapacity := regionalLegacyCapacity(nextNodes, cell.ReservedReplicas, cellFleet, cell.LegacyResidentReplicas, largestFree)
	return r.LegacyReservedReplicas <= p.totalLegacyCapacity-cell.LegacyPlacementCapacity+nextLegacyCapacity
}
