package operatorobs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func readRuntimeBuilderHealth(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) *RuntimeBuilderHealth {
	out := &RuntimeBuilderHealth{Items: []RuntimeBuilderItem{}}
	out.Counts.Expected = result.job.Expected
	samples := runtimeGaugeSamples{reason: "source_unavailable"}
	keys := map[string]string{}
	if result.samples != nil {
		for key := range result.samples.up {
			keys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
		}
		if len(keys) > 0 {
			samples = readRuntimeBuilderSamples(ctx, client, *result.samples, now)
		}
	}
	for _, target := range result.items {
		item := assessRuntimeBuilderItem(target, keys[target.TargetID], samples, result.samples, selected, now)
		accountRuntimeBuilderItem(&out.Counts, item)
		out.Items = append(out.Items, item)
	}
	out.Incomplete = runtimeBuilderIncomplete(out.Counts, result.job.State != "available")
	out.State, out.Reason = runtimeBuilderState(out.Counts, out.Incomplete)
	slices.SortStableFunc(out.Items, func(a, b RuntimeBuilderItem) int { return runtimeBuilderPriority(a) - runtimeBuilderPriority(b) })
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}

func runtimeBuilderIncomplete(c RuntimeBuilderCounts, inventoryUnknown bool) bool {
	return inventoryUnknown || c.Missing+c.Stale+c.Conflicting+c.Unavailable+c.Unexpected > 0
}

func runtimeBuilderState(c RuntimeBuilderCounts, incomplete bool) (string, string) {
	switch {
	case c.OverdueWorkers > 0:
		return "overdue", "worker_progress_overdue"
	case incomplete:
		return "incomplete", "incomplete_builder_evidence"
	case c.Complete > 0:
		return "observed", ""
	default:
		return "unknown", "builder_evidence_unavailable"
	}
}

func runtimeBuilderItemState(item RuntimeBuilderItem) string {
	states := []string{item.Worker.State, item.Slots.State}
	for _, q := range item.Queues {
		states = append(states, q.State)
	}
	for _, state := range states {
		switch state {
		case "missing", "stale", "conflicting", "unavailable":
			return state
		}
	}
	return "available"
}

func assessRuntimeBuilderItem(target api.ObsRuntimeBuildItem, key string, samples runtimeGaugeSamples, builds *runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) RuntimeBuilderItem {
	out := RuntimeBuilderItem{Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, Queues: []RuntimeBuilderQueue{}}
	bad, observedAt := runtimeTargetSourceStatus(target, key, builds, selected, now)
	out.ScrapeObservedAt = observedAt
	component := func(prefix, queue string, names []string) (RuntimeBuilderSignal, map[string]float64) {
		if bad.State != "" {
			return bad, nil
		}
		return samples.component(key, prefix, queue, names, selected, now)
	}
	s, v := component("builderd_build_worker_", "", []string{"started_timestamp_seconds", "last_attempt_timestamp_seconds", "last_completed_timestamp_seconds", "enabled", "inflight", "interval_seconds", "busy_allowance_seconds"})
	out.Worker = runtimeBuilderWorker(s, v, now)
	s, v = component("builderd_build_slots_", "", []string{"in_use", "limit"})
	out.Slots = runtimeBuilderSlots(s, v)
	for _, queue := range []string{"build", "cancel"} {
		s, v = component("builderd_notification_queue_", queue, []string{"waiting", "active", "capacity", "workers", "oldest_enqueued_timestamp_seconds", "enabled"})
		out.Queues = append(out.Queues, runtimeBuilderQueue(s, queue, v, now))
	}
	out.State = runtimeBuilderItemState(out)
	return out
}

func accountRuntimeBuilderItem(c *RuntimeBuilderCounts, item RuntimeBuilderItem) {
	if item.TargetID != "" {
		c.Observed++
	}
	if item.Unexpected {
		c.Unexpected++
	}
	switch item.State {
	case "available":
		c.Complete++
	case "missing":
		c.Missing++
	case "stale":
		c.Stale++
	case "conflicting":
		c.Conflicting++
	default:
		c.Unavailable++
	}
	switch item.Worker.State {
	case "overdue":
		c.OverdueWorkers++
	case "waiting":
		c.WaitingWorkers++
	case "working":
		c.WorkingWorkers++
	case "draining":
		c.DrainingWorkers++
	}
	if item.Slots.Values != nil && item.Slots.Values.Used == item.Slots.Values.Limit {
		c.SaturatedSlots++
	}
	for _, q := range item.Queues {
		if q.Values != nil && q.Values.Waiting > 0 {
			c.WaitingQueues++
		}
	}
}

func runtimeBuilderPriority(item RuntimeBuilderItem) int {
	if item.Worker.State == "overdue" {
		return 0
	}
	if item.State != "available" {
		return 1
	}
	return 2
}
