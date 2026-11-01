package state_test

// adr: 20261101091815

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type nodeMemoryReportTestStore = contractTestStore

func TestMemNodeMemoryReports(t *testing.T) {
	testNodeMemoryReports(t, state.NewMemStore())
}

// A node's report keeps only its newest measurement, and a reader asks only
// for reports measured after a cutoff.
func testNodeMemoryReports(t *testing.T, store nodeMemoryReportTestStore) {
	ctx := context.Background()
	node, err := store.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{Name: "fsn-memory-report", TargetURL: "tcp://10.0.9.4:7000",
		VPCPUs: 8, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8593, VCPUBudget: 80, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	used := 700
	if err := store.UpsertNodeMemoryReport(ctx, state.NodeMemoryReport{NodeID: node.ID, MeasuredMB: 900, UsedMB: &used, FrozenInstances: 3, MeasuredAt: t0}); err != nil {
		t.Fatal(err)
	}
	// An older measurement arriving late does not replace the newer one.
	if err := store.UpsertNodeMemoryReport(ctx, state.NodeMemoryReport{NodeID: node.ID, MeasuredMB: 100, MeasuredAt: t0.Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	report := func(since time.Time) (state.NodeMemoryReport, bool) {
		t.Helper()
		reports, err := store.NodeMemoryReports(ctx, since)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range reports {
			if r.NodeID == node.ID {
				return r, true
			}
		}
		return state.NodeMemoryReport{}, false
	}
	got, ok := report(t0.Add(-time.Minute))
	if !ok || got.MeasuredMB != 900 || got.UsedMB == nil || *got.UsedMB != 700 || got.FrozenInstances != 3 || !got.MeasuredAt.Equal(t0) {
		t.Fatalf("report = %+v, %v; want the newest", got, ok)
	}
	if _, ok := report(t0); ok {
		t.Fatal("a report measured at the cutoff was listed")
	}
	// A newer measurement without a vmmd report clears used memory.
	if err := store.UpsertNodeMemoryReport(ctx, state.NodeMemoryReport{NodeID: node.ID, MeasuredMB: 950, FrozenInstances: 1, MeasuredAt: t0.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if got, ok := report(t0); !ok || got.MeasuredMB != 950 || got.UsedMB != nil {
		t.Fatalf("report after a newer measurement = %+v, %v", got, ok)
	}
}

// A migrating VM holds memory on its node, so the live listing the stats
// poller reads includes it.
func TestListAllInstancesIncludesMigrating(t *testing.T) {
	store, ctx, _, app, dep := state.MemCoverageFixture(t)
	ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateMigrating), 128, "node", "")
	if err != nil {
		t.Fatal(err)
	}
	all, err := store.ListAllInstances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range all {
		if row.ID == ins.ID {
			return
		}
	}
	t.Fatalf("ListAllInstances = %+v, missing the migrating instance", all)
}
