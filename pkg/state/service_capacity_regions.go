package state

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

type regionalServiceCapacity struct {
	api.ServiceRegionCapacityProtection
	largestFree int64
	nodes       map[string]ServiceCapacitySlots
	excess      bool
}

// For a final legacy population T, each node must retain at least
// T-(C-slots[node]-pinned) legacy replicas: those replicas disappear if that
// host fails. The remaining pinned reservation must fit beside every survivor.
// The minimum occupancies form a convex feasibility interval above current T.
func regionalLegacyCapacity(nodes map[string]ServiceCapacitySlots, pinned, fleet, legacy, largestFree int64) int64 {
	if pinned == 0 {
		var used int64
		for _, node := range nodes {
			used += node.Used - node.LegacyUsed
		}
		return max(0, fleet-used)
	}
	if pinned > fleet-legacy-largestFree {
		return 0
	}
	upper := fleet - pinned
	for _, node := range nodes {
		upper = min(upper, fleet-pinned-(node.Used-node.LegacyUsed))
	}
	if upper < legacy {
		return 0
	}
	lower := legacy
	for lower < upper {
		middle := lower + (upper-lower+1)/2
		var needed int64
		feasible := true
		for _, node := range nodes {
			minimum := max(node.LegacyUsed, middle-(fleet-node.Slots-pinned))
			needed += minimum
			feasible = feasible && minimum <= node.Slots-(node.Used-node.LegacyUsed)
		}
		if feasible && needed <= middle {
			lower = middle
		} else {
			upper = middle - 1
		}
	}
	return lower
}

func (s *serviceCapacitySnapshot) applyRegionalServiceCapacity(registered []string) {
	byRegion := make(map[string]*regionalServiceCapacity)
	get := func(id string) *regionalServiceCapacity {
		if r := byRegion[id]; r != nil {
			return r
		}
		r := &regionalServiceCapacity{ServiceRegionCapacityProtection: api.ServiceRegionCapacityProtection{ComputeRegion: id, PlacementsFit: true}, nodes: make(map[string]ServiceCapacitySlots)}
		byRegion[id] = r
		return r
	}
	for _, id := range registered {
		if id != "" {
			get(id)
		}
	}
	var pinnedTotal int64
	for key, demand := range s.Demands {
		if demand.Region == "" {
			s.Report.LegacyReservedReplicas += demand.Count
			continue
		}
		r := get(demand.Region)
		r.ReservedReplicas += demand.Count
		r.DesiredReplicas += demand.Desired
		r.excess = r.excess || s.Actual[key] > demand.Count
		pinnedTotal += demand.Count
	}
	for id, node := range s.Placement {
		r := get(node.Region)
		r.nodes[id] = node
		r.HealthyNodes++
		r.FleetSlots += node.Slots
		r.LegacyResidentReplicas += node.LegacyUsed
		r.PlacementsFit = r.PlacementsFit && node.Fits
		r.largestFree = max(r.largestFree, max(0, node.Slots-node.LegacyUsed))
	}
	ids := make([]string, 0, len(byRegion))
	for id := range byRegion {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	protected, normalFit := true, true
	var legacyCapacity int64
	for _, id := range ids {
		r := byRegion[id]
		r.AvailableSlots = max(0, r.FleetSlots-r.LegacyResidentReplicas)
		r.FailoverSlots = max(0, r.AvailableSlots-r.largestFree)
		r.LegacyPlacementCapacity = regionalLegacyCapacity(r.nodes, r.ReservedReplicas, r.FleetSlots, r.LegacyResidentReplicas, r.largestFree)
		legacyCapacity += r.LegacyPlacementCapacity
		switch {
		case !s.Report.Enabled:
			r.State = "disabled"
		case r.HealthyNodes >= api.ServiceCapacityMinimumHosts && r.PlacementsFit && !r.excess && r.ReservedReplicas <= r.FailoverSlots:
			r.State = "protected"
		case r.PlacementsFit && r.ReservedReplicas <= r.AvailableSlots:
			r.State = "degraded"
		default:
			r.State = "needs_hardware"
		}
		if r.ReservedReplicas > 0 {
			protected = protected && r.State == "protected"
			normalFit = normalFit && r.PlacementsFit && r.ReservedReplicas <= r.AvailableSlots
		}
		if id != "" {
			s.Report.Regions = append(s.Report.Regions, r.ServiceRegionCapacityProtection)
		}
	}
	s.Report.LegacyPlacementCapacity = min(legacyCapacity, max(0, s.Report.FailoverSlots-pinnedTotal))
	if s.Report.Enabled {
		if !normalFit {
			s.Report.State = "needs_hardware"
		} else if s.Report.State == "protected" && (!protected || s.Report.LegacyReservedReplicas > s.Report.LegacyPlacementCapacity) {
			s.Report.State = "degraded"
		}
	}
	for id, node := range s.Placement {
		r := byRegion[node.Region]
		physical := node.Slots > node.Used
		node.PinnedAllowed = physical
		node.LegacyAllowed = physical && r.AvailableSlots > r.ReservedReplicas
		if s.Report.State == "protected" && r.ReservedReplicas > 0 {
			// Other regions may supply at most their own stable legacy capacity.
			neededHere := max(r.LegacyResidentReplicas, s.Report.LegacyReservedReplicas-(legacyCapacity-r.LegacyPlacementCapacity))
			minimum := max(node.LegacyUsed, neededHere-(r.FleetSlots-node.Slots-r.ReservedReplicas))
			node.PinnedAllowed = physical && minimum < node.Slots-(node.Used-node.LegacyUsed)
			nextPopulation := max(r.LegacyResidentReplicas+1, s.Report.LegacyReservedReplicas-(legacyCapacity-r.LegacyPlacementCapacity))
			var needed int64
			for peerID, peer := range r.nodes {
				lower := peer.LegacyUsed
				if peerID == id {
					lower++
				}
				needed += max(lower, nextPopulation-(r.FleetSlots-peer.Slots-r.ReservedReplicas))
			}
			node.LegacyAllowed = physical && nextPopulation <= r.LegacyPlacementCapacity && needed <= nextPopulation
		}
		s.Placement[id] = node
	}
}
