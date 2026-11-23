package buildqueue_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/buildqueue"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

var now = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return now.Add(-d) }

func TestBuildQueueAndRunning(t *testing.T) {
	in := buildqueue.Input{Now: now, Window: 24 * time.Hour, SlotsState: buildqueue.SlotsObserved, Active: []state.OperatorBuildRow{
		{ID: "q-old", Kind: "railpack", Status: "queued", EnqueuedAt: ago(10 * time.Minute)},
		{ID: "q-new", Kind: "dockerfile", Status: "queued", EnqueuedAt: ago(time.Minute)},
		// Deferred until later: not waiting yet.
		{ID: "q-later", Kind: "railpack", Status: "queued", EnqueuedAt: ago(time.Hour), NotBefore: now.Add(time.Minute)},
		// Became due two minutes ago: waits from not_before, not enqueue.
		{ID: "q-due", Kind: "railpack", Status: "queued", EnqueuedAt: ago(time.Hour), NotBefore: ago(2 * time.Minute)},
		// Held behind an identical build: queued and listed, but its long
		// wait is deliberate.
		{ID: "q-held", Kind: "tarball", Status: "queued", CoalescedWith: "r-long", EnqueuedAt: ago(30 * time.Minute)},
		{ID: "r-long", Kind: "railpack", Status: "running", NodeName: "fsn-2", EnqueuedAt: ago(20 * time.Minute), StartedAt: ago(16 * time.Minute)},
		{ID: "r-short", Kind: "github", Status: "running", EnqueuedAt: ago(time.Minute), StartedAt: ago(30 * time.Second)},
	}, Slots: map[string]api.ObsBuilderNodeSlots{"fsn-2": {Limit: 3, Reserved: 1, InUse: 1, ActiveReserved: 1}, "fsn-3": {Limit: 2, Reserved: 1}}}
	out := buildqueue.Build(in)
	tot := out.Totals
	if tot.Queued != 4 || tot.Deferred != 1 || tot.Held != 1 || tot.Running != 2 || tot.WaitingOverWarn != 1 || tot.RunningOverTimeout != 1 || tot.OldestWaitSeconds != 600 {
		t.Fatalf("totals = %+v", tot)
	}
	// The deferred build is counted, not listed.
	if len(out.Queued) != 4 || out.Queued[0].ID != "q-held" || !out.Queued[0].Coalesced || out.Queued[1].ID != "q-old" || out.Queued[2].ID != "q-due" || out.Queued[2].WaitSeconds != 120 {
		t.Fatalf("queued order = %+v", out.Queued)
	}
	if out.Running[0].ID != "r-long" || out.Running[0].DurationSeconds != 960 || out.Running[0].WaitSeconds != 240 {
		t.Fatalf("running = %+v", out.Running)
	}
	if len(out.Nodes) != 3 || out.Nodes[0].Node != "fsn-2" || !out.Nodes[0].Observed || out.Nodes[0].RunningBuilds != 1 || out.Nodes[0].Limit != 3 {
		t.Fatalf("nodes = %+v", out.Nodes)
	}
	if out.Nodes[1].Node != "fsn-3" || out.Nodes[1].RunningBuilds != 0 || out.Nodes[2].Node != "unrecorded" || out.Nodes[2].Observed || out.Nodes[2].RunningBuilds != 1 {
		t.Fatalf("nodes = %+v", out.Nodes)
	}
	if out.Kinds[0].Kind != "railpack" || out.Kinds[0].Queued != 2 || out.Kinds[0].Running != 1 {
		t.Fatalf("kinds = %+v", out.Kinds)
	}
}

func TestBuildFinishedWindow(t *testing.T) {
	done := func(id, status, class, cache string, dur time.Duration, preempt int) state.OperatorBuildRow {
		start := ago(time.Hour)
		return state.OperatorBuildRow{ID: id, Kind: "railpack", Status: status, FailureClass: class, CacheStatus: cache,
			Preemptions: preempt, EnqueuedAt: start.Add(-30 * time.Second), StartedAt: start, FinishedAt: start.Add(dur)}
	}
	in := buildqueue.Input{Now: now, Window: time.Hour, SlotsState: buildqueue.SlotsUnavailable, Finished: []state.OperatorBuildRow{
		done("ok-1", "succeeded", "", "hit", 30*time.Second, 0),
		done("ok-2", "succeeded", "", "miss", 90*time.Second, 1),
		done("oom", "failed", "oom", "miss", 200*time.Second, 0),
		done("to", "failed", "timeout", "", 900*time.Second, 0),
		done("bare", "failed", "", "invalidated", 10*time.Second, 0),
		// Cancelled while queued: counted, but no duration.
		{ID: "cx", Kind: "dockerfile", Status: "cancelled", EnqueuedAt: ago(time.Hour), FinishedAt: ago(50 * time.Minute)},
	}}
	out := buildqueue.Build(in)
	tot := out.Totals
	if tot.Finished != 6 || tot.Succeeded != 2 || tot.Failed != 3 || tot.Cancelled != 1 || tot.Preempted != 1 {
		t.Fatalf("totals = %+v", tot)
	}
	if tot.CacheHits != 1 || tot.CacheMisses != 2 || tot.CacheInvalidated != 1 || tot.DurationP50Seconds != 90 || tot.DurationP95Seconds != 900 || tot.WaitP95Seconds != 30 {
		t.Fatalf("totals = %+v", tot)
	}
	if len(out.Slowest) != 5 || out.Slowest[0].ID != "to" || out.Slowest[4].ID != "bare" {
		t.Fatalf("slowest = %+v", out.Slowest)
	}
	if len(out.RecentFailures) != 3 || out.RecentFailures[0].ID != "oom" {
		t.Fatalf("recent failures = %+v", out.RecentFailures)
	}
	want := []api.ObsBuildFailureCount{{Class: "oom", Count: 1}, {Class: "timeout", Count: 1}, {Class: "unclassified", Count: 1}}
	if fmt.Sprint(out.Failures) != fmt.Sprint(want) {
		t.Fatalf("failures = %+v", out.Failures)
	}
	if out.SlotsState != buildqueue.SlotsUnavailable || len(out.Nodes) != 0 {
		t.Fatalf("slots = %q %+v", out.SlotsState, out.Nodes)
	}
}

func TestBuildCapsLists(t *testing.T) {
	var active, finished []state.OperatorBuildRow
	for i := range api.ObsBuildQueueListMax + 5 {
		active = append(active, state.OperatorBuildRow{ID: fmt.Sprint("q", i), Kind: "tarball", Status: "queued", EnqueuedAt: ago(time.Duration(i) * time.Second)})
		finished = append(finished, state.OperatorBuildRow{ID: fmt.Sprint("f", i), Kind: "tarball", Status: "failed",
			EnqueuedAt: ago(time.Hour), StartedAt: ago(time.Hour), FinishedAt: ago(time.Hour - time.Duration(i)*time.Second)})
	}
	out := buildqueue.Build(buildqueue.Input{Now: now, Window: time.Hour, Active: active, Finished: finished, ActiveTruncated: true})
	if len(out.Queued) != api.ObsBuildQueueListMax || len(out.Slowest) != api.ObsBuildQueueSlowestMax || len(out.RecentFailures) != api.ObsBuildQueueSlowestMax || !out.ActiveTruncated {
		t.Fatalf("caps: queued=%d slowest=%d failures=%d", len(out.Queued), len(out.Slowest), len(out.RecentFailures))
	}
}

func TestBuildEmptyReportHasStableShape(t *testing.T) {
	out := buildqueue.Build(buildqueue.Input{Now: now, Window: time.Hour, SlotsState: buildqueue.SlotsNotConfigured})
	if out.Queued == nil || out.Running == nil || out.Slowest == nil || out.RecentFailures == nil || out.Kinds == nil || out.Failures == nil || out.Nodes == nil {
		t.Fatalf("nil list in %+v", out)
	}
	if out.BuildTimeoutSeconds != api.BuildTimeoutSeconds || out.WindowSeconds != 3600 {
		t.Fatalf("report = %+v", out)
	}
}

func TestSlotsFromSamples(t *testing.T) {
	g := func(name, node, kind string, v float64) promql.VectorSample {
		l := map[string]string{"__name__": name, "node": node, "job": "builderd", "instance": node + ":9102"}
		if kind != "" {
			l["kind"] = kind
		}
		return promql.VectorSample{Labels: l, Value: v}
	}
	at := func(s promql.VectorSample, instance string) promql.VectorSample {
		s.Labels["instance"] = instance
		return s
	}
	got := buildqueue.SlotsFromSamples([]promql.VectorSample{
		g("builderd_build_slots_limit", "fsn-2", "", 4), g("builderd_build_slots_reserved", "fsn-2", "", 1),
		g("builderd_build_slots_in_use", "fsn-2", "", 3), g("builderd_build_slots_active", "fsn-2", "reserved", 2),
		g("builderd_build_slots_active", "fsn-2", "borrowed", 1), g("builderd_build_slots_limit", "", "", 9),
		// Two targets claim fsn-4: neither is trusted.
		at(g("builderd_build_slots_limit", "fsn-4", "", 2), "10.0.0.4:9102"), at(g("builderd_build_slots_limit", "fsn-4", "", 8), "10.0.0.5:9102"),
	})
	want := api.ObsBuilderNodeSlots{Limit: 4, Reserved: 1, InUse: 3, ActiveReserved: 2, Opportunistic: 1, ActiveBorrowed: 1}
	if len(got) != 2 || got["fsn-2"] != want || got["fsn-4"] != (api.ObsBuilderNodeSlots{Conflicting: true}) {
		t.Fatalf("slots = %+v", got)
	}
	nodes := buildqueue.Build(buildqueue.Input{Now: now, Window: time.Hour, Slots: got}).Nodes
	if len(nodes) != 2 || !nodes[0].Observed || nodes[1].Node != "fsn-4" || nodes[1].Observed || !nodes[1].Conflicting {
		t.Fatalf("nodes = %+v", nodes)
	}
	if _, state := buildqueue.ReadSlots(context.Background(), nil); state != buildqueue.SlotsNotConfigured {
		t.Fatalf("nil client state = %q", state)
	}
}
