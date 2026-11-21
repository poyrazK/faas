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

func runtimeBuildRecoveryMetricNames() []string {
	return []string{"apid_build_recovery_collection_success", "apid_build_recovery_collection_last_success_timestamp_seconds", "faas_build_running_count", "faas_build_running_oldest_started_timestamp_seconds", "faas_build_vm_cleanup_pending", "faas_build_vm_cleanup_oldest_created_timestamp_seconds", "faas_build_vm_cleanup_retry_pending"}
}
func runtimeBuildRecoveryQuery() string {
	return runtimeGaugeQuery("apid", runtimeBuildRecoveryMetricNames())
}

func readRuntimeBuildRecoveryHealth(ctx context.Context, client *promql.Client, result runtimeBuildJobResult, selected RuntimeBuildSelection, now time.Time) *RuntimeBuildRecoveryHealth {
	out := &RuntimeBuildRecoveryHealth{RuntimeBuildRecoverySummary: RuntimeBuildRecoverySummary{Scope: RuntimeBuildRecoveryScope}, Items: []RuntimeBuildRecoveryItem{}}
	out.Counts.Expected = result.job.Expected
	keys, known := map[string]string{}, map[string]bool{}
	for _, name := range runtimeBuildRecoveryMetricNames() {
		known[name+"/"] = true
	}
	samples := runtimeGaugeSamples{reason: "source_unavailable"}
	if result.samples != nil {
		for key := range result.samples.up {
			keys[fmt.Sprintf("%x", sha256.Sum256([]byte(key)))] = key
		}
		if len(keys) > 0 {
			samples = readRuntimeGaugeSamples(ctx, client, *result.samples, "apid", runtimeBuildRecoveryQuery(), known, now)
		}
	}
	for _, target := range result.items {
		item := assessRuntimeBuildRecoveryItem(target, keys[target.TargetID], samples, result.samples, selected, now)
		accountRuntimeBuildRecoveryItem(&out.Counts, item)
		out.Items = append(out.Items, item)
	}
	out.Incomplete = runtimeBuildRecoveryIncomplete(out.Counts, result.job.State != "available")
	out.State, out.Reason = runtimeBuildRecoveryState(out.Counts, out.Incomplete)
	slices.SortStableFunc(out.Items, func(a, b RuntimeBuildRecoveryItem) int {
		if p := cmp.Compare(runtimeBuildRecoveryPriority(a), runtimeBuildRecoveryPriority(b)); p != 0 {
			return p
		}
		return cmp.Compare(runtimeBuildRecoveryOldest(b), runtimeBuildRecoveryOldest(a))
	})
	if len(out.Items) > api.ObsRuntimeBuildPreviewMax {
		out.OmittedItems = len(out.Items) - api.ObsRuntimeBuildPreviewMax
		out.Items = out.Items[:api.ObsRuntimeBuildPreviewMax]
	}
	return out
}

func runtimeBuildRecoveryIncomplete(c RuntimeBuildRecoveryCounts, inventoryUnknown bool) bool {
	return inventoryUnknown || c.Missing+c.Stale+c.Conflicting+c.Unavailable+c.Unexpected > 0
}
func runtimeBuildRecoveryState(c RuntimeBuildRecoveryCounts, incomplete bool) (string, string) {
	switch {
	case c.NonemptyViews > 0:
		return "nonempty", "build_recovery_evidence_observed"
	case incomplete:
		return "incomplete", "incomplete_build_recovery_evidence"
	case c.Complete > 0:
		return "empty", ""
	default:
		return "unknown", "build_recovery_evidence_unavailable"
	}
}
func runtimeBuildRecoveryItemState(item RuntimeBuildRecoveryItem) string {
	for _, s := range []string{item.Collection.State, item.Running.State, item.Cleanup.State} {
		if slices.Contains([]string{"missing", "stale", "conflicting", "unavailable"}, s) {
			return s
		}
	}
	return "available"
}

func assessRuntimeBuildRecoveryItem(target api.ObsRuntimeBuildItem, key string, samples runtimeGaugeSamples, builds *runtimeBuildSamples, selected RuntimeBuildSelection, now time.Time) RuntimeBuildRecoveryItem {
	bad, at := runtimeTargetSourceStatus(target, key, builds, selected, now)
	out := RuntimeBuildRecoveryItem{Job: "apid", Node: target.Node, TargetID: target.TargetID, Unexpected: target.Unexpected, ScrapeObservedAt: at}
	component := func(names []string, matchCollection bool) (RuntimeBuilderSignal, map[string]float64) {
		if bad.State != "" {
			return bad, nil
		}
		s, v := samples.coherentComponent(key, "", names, selected, now)
		if matchCollection && s.State == "available" && samples.rows[key][names[0]+"/"].at != samples.rows[key]["apid_build_recovery_collection_success/"].at {
			return RuntimeBuilderSignal{State: "conflicting", Reason: "conflicting_sample_times"}, nil
		}
		return s, v
	}
	s, v := component(runtimeBuildRecoveryMetricNames()[:2], false)
	// The existing collection decoder expects only the two bounded status keys.
	out.Collection = runtimeHandoffCollection(s, map[string]float64{"collection_success": v["apid_build_recovery_collection_success"], "collection_last_success_timestamp_seconds": v["apid_build_recovery_collection_last_success_timestamp_seconds"]}, now)
	prerequisite := runtimeBuildRecoveryPrerequisite(out.Collection, selected.after, now)
	out.Running = RuntimeBuildRecoveryRunning{RuntimeHandoffSignal: prerequisite}
	out.Cleanup = RuntimeBuildRecoveryCleanup{RuntimeHandoffSignal: prerequisite}
	if prerequisite.State == "" {
		s, v = component([]string{"faas_build_running_count", "faas_build_running_oldest_started_timestamp_seconds"}, true)
		out.Running = runtimeBuildRecoveryRunning(s, v, now)
		s, v = component([]string{"faas_build_vm_cleanup_pending", "faas_build_vm_cleanup_oldest_created_timestamp_seconds", "faas_build_vm_cleanup_retry_pending"}, true)
		out.Cleanup = runtimeBuildRecoveryCleanup(s, v, now)
	}
	out.State = runtimeBuildRecoveryItemState(out)
	return out
}

func accountRuntimeBuildRecoveryItem(c *RuntimeBuildRecoveryCounts, item RuntimeBuildRecoveryItem) {
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
	running, cleanup := item.Running.State == "nonempty", item.Cleanup.State == "nonempty"
	if running {
		c.RunningViews++
	}
	if cleanup {
		c.CleanupViews++
	}
	if running || cleanup {
		c.NonemptyViews++
	}
	if item.Running.State == "empty" && item.Cleanup.State == "empty" {
		c.EmptyViews++
	}
	if item.Cleanup.Values != nil && item.Cleanup.Values.Retried > 0 {
		c.RetriedCleanupViews++
	}
	if item.Collection.Values != nil && !item.Collection.Values.Success {
		c.UnsuccessfulCollections++
	}
}

func runtimeBuildRecoveryPriority(item RuntimeBuildRecoveryItem) int {
	if item.Cleanup.State == "nonempty" {
		return 0
	}
	if item.Running.State == "nonempty" {
		return 1
	}
	if item.State != "available" {
		return 2
	}
	return 3
}
func runtimeBuildRecoveryOldest(item RuntimeBuildRecoveryItem) float64 {
	if item.Cleanup.Values != nil && item.Cleanup.Values.OldestAgeSeconds != nil {
		return *item.Cleanup.Values.OldestAgeSeconds
	}
	if item.Running.Values != nil && item.Running.Values.OldestAgeSeconds != nil {
		return *item.Running.Values.OldestAgeSeconds
	}
	return 0
}
