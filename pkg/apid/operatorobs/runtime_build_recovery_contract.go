package operatorobs

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimeBuildRecoveryCountValues(c RuntimeBuildRecoveryCounts) []int {
	return []int{c.Expected, c.Observed, c.Complete, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, c.NonemptyViews, c.EmptyViews, c.RunningViews, c.CleanupViews, c.RetriedCleanupViews, c.UnsuccessfulCollections}
}
func runtimeBuildRecoveryTotal(c RuntimeBuildRecoveryCounts) int {
	return c.Complete + c.Missing + c.Stale + c.Conflicting + c.Unavailable
}

func RuntimeBuildRecoverySummaryValid(base api.ObsRuntimeBuilds, h RuntimeBuildRecoverySummary) bool {
	job := slices.IndexFunc(base.Jobs, func(j api.ObsRuntimeBuildJob) bool { return j.Job == "apid" && j.Daemon == "apid" })
	if !runtimeBuildRecoverySelected(base.ExpectedRole) || job < 0 || h.Scope != RuntimeBuildRecoveryScope {
		return false
	}
	c := h.Counts
	for _, n := range runtimeBuildRecoveryCountValues(c) {
		if n < 0 || n > api.ObsRuntimeBuildSamplesMax+api.ObsRuntimeBuildNodesMax {
			return false
		}
	}
	total := runtimeBuildRecoveryTotal(c)
	if c.Expected != base.Jobs[job].Expected || total < c.Expected || total > api.ObsRuntimeBuildSamplesMax+api.ObsRuntimeBuildNodesMax || c.Observed > min(total, base.Counts.Observed) || c.Unexpected > c.Observed || c.NonemptyViews+c.EmptyViews+c.UnsuccessfulCollections > c.Observed || c.Complete > c.NonemptyViews+c.EmptyViews || c.RetriedCleanupViews > c.CleanupViews || max(c.RunningViews, c.CleanupViews) > c.NonemptyViews || c.NonemptyViews > c.RunningViews+c.CleanupViews {
		return false
	}
	incomplete := runtimeBuildRecoveryIncomplete(c, base.Jobs[job].State != "available")
	state, reason := runtimeBuildRecoveryState(c, incomplete)
	return h.Incomplete == incomplete && h.State == state && h.Reason == reason
}

func RuntimeBuildRecoveryHealthValid(base api.ObsRuntimeBuilds, h *RuntimeBuildRecoveryHealth) bool {
	if h == nil || !RuntimeBuildRecoverySummaryValid(base, h.RuntimeBuildRecoverySummary) || len(h.Items) > api.ObsRuntimeBuildPreviewMax || h.OmittedItems < 0 || len(h.Items)+h.OmittedItems != runtimeBuildRecoveryTotal(h.Counts) {
		return false
	}
	part, seen := RuntimeBuildRecoveryCounts{}, map[string]bool{}
	for _, item := range h.Items {
		key := item.TargetID
		if key == "" {
			key = "apid/missing/" + item.Node
		}
		if seen[key] || !runtimeBuildRecoveryItemValid(base, item) {
			return false
		}
		seen[key] = true
		accountRuntimeBuildRecoveryItem(&part, item)
	}
	full, preview := runtimeBuildRecoveryCountValues(h.Counts), runtimeBuildRecoveryCountValues(part)
	for i := 1; i < len(full); i++ {
		if preview[i] > full[i] || h.OmittedItems == 0 && preview[i] != full[i] {
			return false
		}
	}
	return true
}

func runtimeBuildRecoveryItemValid(base api.ObsRuntimeBuilds, item RuntimeBuildRecoveryItem) bool {
	if item.Job != "apid" || item.Node != "" && !ServingProbeNodeValid(item.Node) || item.TargetID != "" && !runtimeTargetIDValid(item.TargetID) || item.Unexpected && item.TargetID == "" || item.State != runtimeBuildRecoveryItemState(item) {
		return false
	}
	if at := item.ScrapeObservedAt; at != nil && (at.IsZero() || at.After(base.GeneratedAt.Add(api.ObsHealthClockSkewTolerance))) {
		return false
	}
	bridge := RuntimeHandoffItem{TargetID: item.TargetID, ScrapeObservedAt: item.ScrapeObservedAt}
	if !runtimeHandoffSignalValid(base, bridge, item.Collection.RuntimeHandoffSignal, item.Collection.Values != nil, []string{"available"}) || !runtimeHandoffSignalValid(base, bridge, item.Running.RuntimeHandoffSignal, item.Running.Values != nil, []string{"nonempty", "empty"}) || !runtimeHandoffSignalValid(base, bridge, item.Cleanup.RuntimeHandoffSignal, item.Cleanup.Values != nil, []string{"nonempty", "empty"}) {
		return false
	}
	if v := item.Collection.Values; v != nil && (!runtimeHandoffSampleClockValid(v.LastSuccessAt, item.Collection.ObservedAt, base.GeneratedAt) || v.Success && v.LastSuccessAt == nil) {
		return false
	}
	prerequisite := runtimeBuildRecoveryPrerequisite(item.Collection, base.EvidenceAfter, base.GeneratedAt)
	for _, s := range []RuntimeHandoffSignal{item.Running.RuntimeHandoffSignal, item.Cleanup.RuntimeHandoffSignal} {
		if prerequisite.State != "" {
			if s.State != prerequisite.State || s.Reason != prerequisite.Reason {
				return false
			}
		} else if slices.Contains([]string{"collection_failed", "collection_not_observed", "collection_before_boundary", "collection_stale"}, s.Reason) {
			return false
		}
	}
	if v := item.Running.Values; v != nil {
		if !runtimeBuildRecoveryPairValid(v.Count, v.OldestStartedAt, v.OldestAgeSeconds, base.GeneratedAt) || !runtimeHandoffSampleClockValid(v.OldestStartedAt, item.Running.ObservedAt, base.GeneratedAt) || (item.Running.State == "nonempty") != (v.Count > 0) || item.Collection.ObservedAt == nil || !item.Running.ObservedAt.Equal(*item.Collection.ObservedAt) {
			return false
		}
	}
	if v := item.Cleanup.Values; v != nil {
		if !runtimeBuildRecoveryPairValid(v.Pending, v.OldestCreatedAt, v.OldestAgeSeconds, base.GeneratedAt) || !runtimeBuildRecoveryCountValid(v.Retried) || v.Retried > v.Pending || !runtimeHandoffSampleClockValid(v.OldestCreatedAt, item.Cleanup.ObservedAt, base.GeneratedAt) || (item.Cleanup.State == "nonempty") != (v.Pending > 0) || item.Collection.ObservedAt == nil || !item.Cleanup.ObservedAt.Equal(*item.Collection.ObservedAt) {
			return false
		}
	}
	return true
}
