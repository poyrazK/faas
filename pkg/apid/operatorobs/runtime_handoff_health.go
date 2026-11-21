package operatorobs

import (
	"cmp"
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func runtimeHandoffMetricNames() []string {
	return []string{"imaged_build_handoff_collection_success", "imaged_build_handoff_collection_last_success_timestamp_seconds", "imaged_build_handoff_pending", "imaged_build_handoff_oldest_finished_timestamp_seconds", "imaged_build_handoff_recovery_enabled", "imaged_build_handoff_recovery_inflight", "imaged_build_handoff_recovery_last_attempt_timestamp_seconds", "imaged_build_handoff_recovery_last_completed_timestamp_seconds"}
}

func runtimeHandoffQuery() string { return runtimeGaugeQuery("imaged", runtimeHandoffMetricNames()) }

func readRuntimeHandoffHealth(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) *RuntimeHandoffHealth {
	out := &RuntimeHandoffHealth{Items: []RuntimeHandoffItem{}}
	out.Counts.Expected = result.job.Expected
	keys, known := map[string]string{}, map[string]bool{}
	for _, name := range runtimeHandoffMetricNames() {
		known[name+"/"] = true
	}
	samples := runtimeGaugeSamples{reason: "source_unavailable"}
	if result.samples != nil {
		for key := range result.samples.up {
			keys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
		}
		if len(keys) > 0 {
			samples = readRuntimeGaugeSamples(ctx, client, *result.samples, "imaged", runtimeHandoffQuery(), known, now)
		}
	}
	for _, target := range result.items {
		item := assessRuntimeHandoffItem(target, keys[target.TargetID], samples, result.samples, selected, now)
		accountRuntimeHandoffItem(&out.Counts, item)
		out.Items = append(out.Items, item)
	}
	out.Incomplete = runtimeHandoffIncomplete(out.Counts, result.job.State != "available")
	out.State, out.Reason = runtimeHandoffState(out.Counts, out.Incomplete)
	slices.SortStableFunc(out.Items, func(a, b RuntimeHandoffItem) int {
		if p := cmp.Compare(runtimeHandoffPriority(a), runtimeHandoffPriority(b)); p != 0 {
			return p
		}
		if a.Backlog.Values != nil && b.Backlog.Values != nil {
			return cmp.Compare(b.Backlog.Values.OldestWaitSeconds, a.Backlog.Values.OldestWaitSeconds)
		}
		return 0
	})
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}

func runtimeHandoffIncomplete(c RuntimeHandoffCounts, inventoryUnknown bool) bool {
	return inventoryUnknown || c.Missing+c.Stale+c.Conflicting+c.Unavailable+c.Unexpected > 0
}

func runtimeHandoffState(c RuntimeHandoffCounts, incomplete bool) (string, string) {
	switch {
	case c.WaitingScopes > 0:
		return "waiting", "pending_handoffs_observed"
	case incomplete:
		return "incomplete", "incomplete_handoff_evidence"
	case c.Complete > 0:
		return "empty", ""
	default:
		return "unknown", "handoff_evidence_unavailable"
	}
}

func runtimeHandoffItemState(item RuntimeHandoffItem) string {
	for _, state := range []string{item.Collection.State, item.Backlog.State, item.Recovery.State} {
		if slices.Contains([]string{"missing", "stale", "conflicting", "unavailable"}, state) {
			return state
		}
	}
	return "available"
}

func assessRuntimeHandoffItem(target api.ObsRuntimeBuildItem, key string, samples runtimeGaugeSamples, builds *runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) RuntimeHandoffItem {
	bad, observedAt := runtimeTargetSourceStatus(target, key, builds, selected, now)
	out := RuntimeHandoffItem{Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, ScrapeObservedAt: observedAt}
	component := func(names []string) (RuntimeBuilderSignal, map[string]float64) {
		if bad.State != "" {
			return bad, nil
		}
		return samples.coherentComponent(key, "imaged_build_handoff_", names, selected, now)
	}
	s, v := component([]string{"collection_success", "collection_last_success_timestamp_seconds"})
	out.Collection = runtimeHandoffCollection(s, v, now)
	s, v = component([]string{"recovery_enabled", "recovery_inflight", "recovery_last_attempt_timestamp_seconds", "recovery_last_completed_timestamp_seconds"})
	out.Recovery = runtimeHandoffRecovery(s, v, now)
	out.Backlog = RuntimeHandoffBacklog{RuntimeHandoffSignal: runtimeHandoffBacklogPrerequisite(out.Collection, selected.after, now)}
	if out.Backlog.State == "" {
		s, v = component([]string{"collection_success", "collection_last_success_timestamp_seconds", "pending", "oldest_finished_timestamp_seconds"})
		out.Backlog = runtimeHandoffBacklog(s, v, now)
	}
	out.State = runtimeHandoffItemState(out)
	return out
}

func accountRuntimeHandoffItem(c *RuntimeHandoffCounts, item RuntimeHandoffItem) {
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
	switch item.Backlog.State {
	case "waiting":
		c.WaitingScopes++
	case "empty":
		c.EmptyScopes++
	}
	if item.Collection.Values != nil && !item.Collection.Values.Success {
		c.UnsuccessfulCollections++
	}
	switch item.Recovery.State {
	case "working":
		c.WorkingRecoveryScopes++
	case "waiting":
		c.WaitingRecoveryScopes++
	case "stopping":
		c.StoppingRecoveryScopes++
	case "not_started":
		c.NotStartedRecoveryScopes++
	}
}

func runtimeHandoffPriority(item RuntimeHandoffItem) int {
	if item.Backlog.State == "waiting" {
		return 0
	}
	if item.State != "available" {
		return 1
	}
	return 2
}
