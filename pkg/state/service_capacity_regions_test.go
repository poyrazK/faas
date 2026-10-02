// adr: 437
package state

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Enumerate final legacy allocations while keeping every existing survivor
// fixed. For each allocation, independently remove every host and require
// enough remaining room for the complete pinned target. This oracle does not
// use the solver's minimum-occupancy bound or binary search.
func exhaustiveRegionalLegacyCapacity(nodes map[string]ServiceCapacitySlots, pinned int64) int64 {
	rows := make([]ServiceCapacitySlots, 0, len(nodes))
	for _, node := range nodes {
		rows = append(rows, node)
	}
	best := int64(0)
	allocation := make([]int64, len(rows))
	var visit func(int)
	visit = func(index int) {
		if index < len(rows) {
			node := rows[index]
			for legacy := node.LegacyUsed; legacy <= node.Slots-(node.Used-node.LegacyUsed); legacy++ {
				allocation[index] = legacy
				visit(index + 1)
			}
			return
		}
		var population, available int64
		for i, node := range rows {
			population += allocation[i]
			available += node.Slots - allocation[i]
		}
		if available < pinned {
			return
		}
		if pinned > 0 {
			for failed := range rows {
				var surviving int64
				for i, node := range rows {
					if i != failed {
						surviving += node.Slots - allocation[i]
					}
				}
				if surviving < pinned {
					return
				}
			}
		}
		best = max(best, population)
	}
	visit(0)
	return best
}

func TestRegionalLegacyCapacityExhaustive(t *testing.T) {
	random := rand.New(rand.NewSource(437))
	checkedHints := 0
	for scenario := range 2500 {
		nodes := map[string]ServiceCapacitySlots{}
		var fleet, legacy, largestFree, pinnedUsed, largest int64
		for i := range 2 + random.Intn(2) {
			slots := int64(1 + random.Intn(4))
			l := int64(random.Intn(int(slots + 1)))
			p := int64(random.Intn(int(slots - l + 1)))
			nodes[fmt.Sprint(i)] = ServiceCapacitySlots{Slots: slots, Used: l + p, LegacyUsed: l, Region: "eu-fsn", Fits: true}
			fleet += slots
			legacy += l
			pinnedUsed += p
			largestFree = max(largestFree, slots-l)
			largest = max(largest, slots)
		}
		pinned := pinnedUsed + int64(random.Intn(4))
		got := regionalLegacyCapacity(nodes, pinned, fleet, legacy, largestFree)
		want := exhaustiveRegionalLegacyCapacity(nodes, pinned)
		if got != want {
			t.Fatalf("scenario=%d nodes=%+v pinned=%d capacity=%d want=%d", scenario, nodes, pinned, got, want)
		}
		// Verify placement hints against the same oracle after freezing one more
		// pinned or unrestricted survivor, with pending intent still outstanding.
		upper := min(want, fleet-largest-pinned)
		if upper <= legacy || pinned == 0 {
			continue
		}
		target := legacy + 1 + int64(random.Intn(int(upper-legacy)))
		snapshot := serviceCapacitySnapshot{Report: api.ServiceCapacityProtection{Enabled: true, State: "protected", FailoverSlots: fleet - largest}, Demands: map[string]capacityResources{"pinned": {Region: "eu-fsn", Count: pinned}, "legacy": {Count: target}}, Placement: nodes}
		snapshot.applyRegionalServiceCapacity(nil)
		if snapshot.Report.State != "protected" {
			t.Fatalf("feasible scenario refused: %+v", snapshot.Report)
		}
		for id, node := range snapshot.Placement {
			physical := node.Slots > node.Used
			for _, kind := range []string{"pinned", "legacy"} {
				next := make(map[string]ServiceCapacitySlots, len(nodes))
				for key, row := range nodes {
					next[key] = row
				}
				frozen := node
				frozen.Used++
				if kind == "legacy" {
					frozen.LegacyUsed++
				}
				next[id] = frozen
				allowed := physical && exhaustiveRegionalLegacyCapacity(next, pinned) >= target
				hint := node.PinnedAllowed
				if kind == "legacy" {
					hint = node.LegacyAllowed
				}
				if hint != allowed {
					t.Fatalf("scenario=%d kind=%s node=%s nodes=%+v pinned=%d legacy=%d hint=%t want=%t", scenario, kind, id, nodes, pinned, target, hint, allowed)
				}
				checkedHints++
			}
		}
	}
	if checkedHints < 100 {
		t.Fatalf("too few protected placement cases: %d", checkedHints)
	}
}
