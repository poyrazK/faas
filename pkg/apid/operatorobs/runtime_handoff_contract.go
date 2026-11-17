package operatorobs

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimeHandoffCountValues(c RuntimeHandoffCounts) []int {
	return []int{c.Expected, c.Observed, c.Complete, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, c.WaitingScopes, c.EmptyScopes, c.UnsuccessfulCollections, c.WorkingRecoveryScopes, c.WaitingRecoveryScopes, c.StoppingRecoveryScopes, c.NotStartedRecoveryScopes}
}

func runtimeHandoffTotal(c RuntimeHandoffCounts) int {
	return c.Complete + c.Missing + c.Stale + c.Conflicting + c.Unavailable
}

func RuntimeHandoffSummaryValid(base api.ObsRuntimeBuilds, h RuntimeHandoffSummary) bool {
	job := slices.IndexFunc(base.Jobs, func(j api.ObsRuntimeBuildJob) bool { return j.Job == "imaged" && j.Daemon == "imaged" })
	if job < 0 || base.ExpectedRole != "single-box-v2" && base.ExpectedRole != "control-plane-v2" {
		return false
	}
	c := h.Counts
	for _, n := range runtimeHandoffCountValues(c) {
		if n < 0 || n > api.ObsRuntimeBuildSamplesMax+api.ObsRuntimeBuildNodesMax {
			return false
		}
	}
	total, backlog, recovery := runtimeHandoffTotal(c), c.WaitingScopes+c.EmptyScopes, c.WorkingRecoveryScopes+c.WaitingRecoveryScopes+c.StoppingRecoveryScopes+c.NotStartedRecoveryScopes
	if c.Expected != base.Jobs[job].Expected || total < c.Expected || total > api.ObsRuntimeBuildSamplesMax+api.ObsRuntimeBuildNodesMax || c.Observed > total || c.Observed > base.Counts.Observed || c.Complete > min(backlog, recovery) || backlog+c.UnsuccessfulCollections > c.Observed || recovery > c.Observed || c.Unexpected > c.Observed {
		return false
	}
	incomplete := runtimeHandoffIncomplete(c, base.Jobs[job].State != "available")
	state, reason := runtimeHandoffState(c, incomplete)
	return h.Incomplete == incomplete && h.State == state && h.Reason == reason
}

func RuntimeHandoffHealthValid(base api.ObsRuntimeBuilds, h *RuntimeHandoffHealth) bool {
	if h == nil || !RuntimeHandoffSummaryValid(base, h.RuntimeHandoffSummary) || len(h.Items) > api.ObsRuntimeBuildPreviewMax || h.OmittedItems < 0 || len(h.Items)+h.OmittedItems != runtimeHandoffTotal(h.Counts) {
		return false
	}
	part, seen := RuntimeHandoffCounts{}, map[string]bool{}
	for _, item := range h.Items {
		key := item.TargetID
		if key == "" {
			key = "missing/" + item.Node
		}
		if seen[key] || !runtimeHandoffItemValid(base, item) {
			return false
		}
		seen[key] = true
		accountRuntimeHandoffItem(&part, item)
	}
	full, preview := runtimeHandoffCountValues(h.Counts), runtimeHandoffCountValues(part)
	for i := 1; i < len(full); i++ {
		if preview[i] > full[i] || h.OmittedItems == 0 && preview[i] != full[i] {
			return false
		}
	}
	return true
}

func runtimeHandoffSignalValid(base api.ObsRuntimeBuilds, item RuntimeHandoffItem, s RuntimeHandoffSignal, values bool, validStates []string) bool {
	if slices.Contains(validStates, s.State) {
		return values && s.Reason == "" && item.TargetID != "" && runtimeProcessTimeValid(item.ScrapeObservedAt, base.GeneratedAt, base.EvidenceAfter) && runtimeProcessTimeValid(s.ObservedAt, base.GeneratedAt, base.EvidenceAfter)
	}
	if values || s.ObservedAt != nil {
		return false
	}
	switch s.State {
	case "missing":
		return slices.Contains([]string{"expected_target_missing", "metric_missing"}, s.Reason)
	case "stale":
		return slices.Contains([]string{"scrape_stale", "metric_stale"}, s.Reason) || slices.Contains(validStates, "empty") && s.Reason == "collection_stale"
	case "conflicting":
		return slices.Contains([]string{"conflicting_targets", "conflicting_series", "conflicting_sample_times"}, s.Reason)
	case "unavailable":
		return slices.Contains([]string{"source_unavailable", "invalid_evidence", "invalid_sample_time", "scrape_before_boundary", "scrape_down", "metric_before_boundary", "invalid_value"}, s.Reason) || slices.Contains(validStates, "empty") && slices.Contains([]string{"collection_failed", "collection_not_observed", "collection_before_boundary"}, s.Reason)
	default:
		return false
	}
}

func runtimeHandoffItemValid(base api.ObsRuntimeBuilds, item RuntimeHandoffItem) bool {
	if item.Node != "" && !ServingProbeNodeValid(item.Node) || item.TargetID != "" && !runtimeTargetIDValid(item.TargetID) || item.Unexpected && item.TargetID == "" || item.State != runtimeHandoffItemState(item) {
		return false
	}
	if at := item.ScrapeObservedAt; at != nil && (at.IsZero() || at.After(base.GeneratedAt.Add(api.ObsHealthClockSkewTolerance))) {
		return false
	}
	if !runtimeHandoffSignalValid(base, item, item.Collection.RuntimeHandoffSignal, item.Collection.Values != nil, []string{"available"}) || !runtimeHandoffSignalValid(base, item, item.Backlog.RuntimeHandoffSignal, item.Backlog.Values != nil, []string{"waiting", "empty"}) || !runtimeHandoffSignalValid(base, item, item.Recovery.RuntimeHandoffSignal, item.Recovery.Values != nil, []string{"waiting", "working", "stopping", "not_started"}) {
		return false
	}
	if v := item.Collection.Values; v != nil && (!runtimeHandoffSampleClockValid(v.LastSuccessAt, item.Collection.ObservedAt, base.GeneratedAt) || v.Success && v.LastSuccessAt == nil) {
		return false
	}
	prerequisite := runtimeHandoffBacklogPrerequisite(item.Collection, base.EvidenceAfter, base.GeneratedAt)
	if prerequisite.State != "" {
		if item.Backlog.Values != nil || item.Backlog.State != prerequisite.State || item.Backlog.Reason != prerequisite.Reason {
			return false
		}
	} else if slices.Contains([]string{"collection_failed", "collection_not_observed", "collection_before_boundary", "collection_stale"}, item.Backlog.Reason) {
		return false
	}
	if v := item.Backlog.Values; v != nil {
		if !runtimeHandoffBacklogValid(*v, base.GeneratedAt) || !runtimeHandoffSampleClockValid(v.OldestFinishedAt, item.Backlog.ObservedAt, base.GeneratedAt) || (item.Backlog.State == "waiting") != (v.Pending > 0) || item.Collection.ObservedAt == nil || !item.Backlog.ObservedAt.Equal(*item.Collection.ObservedAt) {
			return false
		}
	}
	if v := item.Recovery.Values; v != nil {
		state, ok := runtimeHandoffRecoveryState(*v, base.GeneratedAt)
		if !ok || state != item.Recovery.State || !runtimeHandoffSampleClockValid(v.LastAttemptAt, item.Recovery.ObservedAt, base.GeneratedAt) || !runtimeHandoffSampleClockValid(v.LastCompletedAt, item.Recovery.ObservedAt, base.GeneratedAt) {
			return false
		}
	}
	return true
}
