package main

import "github.com/onebox-faas/faas/pkg/scheddgrpc"

// preferLiveStatsRows keys the fleet's stats rows by instance, keeping at most
// one row per instance (ADR-20261104090000). A live-migrated instance keeps
// its ID: for a few minutes the node it left serves its departed row, with the
// old node's high cumulative network counters, while the new node serves its
// live row, counting again from zero. Which row a plain map kept changed from
// refresh to refresh, and the durable checkpoint read every switch from the
// low to the high reading as new bytes, counting the old node's egress twice.
// A live row therefore always wins. An instance only departed rows report is
// kept only when exactly one node reports it: two departed readings come from
// two nodes' counters, and choosing either could count bytes twice, so both
// are dropped and at most the final seconds go unmetered.
func preferLiveStatsRows(rows []scheddgrpc.InstanceStatsRow) map[string]scheddgrpc.InstanceStatsRow {
	live := make(map[string]scheddgrpc.InstanceStatsRow, len(rows))
	departed := make(map[string][]scheddgrpc.InstanceStatsRow)
	for _, r := range rows {
		if r.Departed {
			departed[r.InstanceID] = append(departed[r.InstanceID], r)
			continue
		}
		live[r.InstanceID] = r
	}
	for id, readings := range departed {
		if _, ok := live[id]; ok || len(readings) != 1 {
			continue
		}
		live[id] = readings[0]
	}
	return live
}
