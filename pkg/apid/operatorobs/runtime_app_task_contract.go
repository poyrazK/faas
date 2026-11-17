package operatorobs

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimeAppTaskCountValues(c RuntimeAppTaskCounts) []int {
	return []int{c.Expected, c.Observed, c.Complete, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, c.NonemptyViews, c.EmptyViews, c.UnknownAgeViews, c.UnsuccessfulCollections, c.EnabledViews, c.DisabledViews, c.DispatchEnabledViews, c.DispatchDisabledViews, c.RunnerRunningViews, c.RunnerStoppedViews, c.OccupiedViews, c.SaturatedViews}
}

func runtimeAppTaskTotal(c RuntimeAppTaskCounts) int {
	return c.Complete + c.Missing + c.Stale + c.Conflicting + c.Unavailable
}

func RuntimeAppTaskSummaryValid(base api.ObsRuntimeBuilds, h RuntimeAppTaskSummary) bool {
	jobs := runtimeAppTaskJobs(base.ExpectedRole)
	if len(jobs) == 0 || h.Scope != RuntimeAppTaskScope {
		return false
	}
	expected, inventoryUnknown := 0, false
	for _, name := range jobs {
		i := slices.IndexFunc(base.Jobs, func(j api.ObsRuntimeBuildJob) bool { return j.Job == name && j.Daemon == "schedd" })
		if i < 0 {
			return false
		}
		expected += base.Jobs[i].Expected
		inventoryUnknown = inventoryUnknown || base.Jobs[i].State != "available"
	}
	maximum := len(jobs) * (api.ObsRuntimeBuildSamplesMax + api.ObsRuntimeBuildNodesMax)
	c := h.Counts
	for _, n := range runtimeAppTaskCountValues(c) {
		if n < 0 || n > maximum {
			return false
		}
	}
	if c.DispatchEnabledViews+c.DispatchDisabledViews > c.Observed || c.RunnerRunningViews+c.RunnerStoppedViews != c.DispatchEnabledViews || c.OccupiedViews > c.DispatchEnabledViews+c.DispatchDisabledViews || c.SaturatedViews > c.OccupiedViews || c.Complete > c.DispatchEnabledViews+c.DispatchDisabledViews {
		return false
	}
	total := runtimeAppTaskTotal(c)
	if c.Expected != expected || total < c.Expected || total > maximum || c.Observed > min(total, base.Counts.Observed) || c.Unexpected > c.Observed || c.NonemptyViews+c.EmptyViews+c.UnsuccessfulCollections > c.Observed || c.UnknownAgeViews > c.NonemptyViews || c.EnabledViews+c.DisabledViews > c.Observed || c.Complete > min(c.EnabledViews+c.DisabledViews, c.NonemptyViews+c.EmptyViews) {
		return false
	}
	if len(h.Classes) != len(runtimeAppTaskClasses()) {
		return false
	}
	waiting, unknown := 0, 0
	for i, p := range h.Classes {
		if runtimeAppTaskUnknownAgeClass(p.Class) && p.NonemptyViews != p.UnknownAgeViews {
			return false
		}
		if p.NonemptyViews > c.Observed || p.EmptyViews > c.Observed || p.UnknownAgeViews > c.Observed {
			return false
		}
		if p.Kind+"/"+p.Class != runtimeAppTaskClasses()[i] || p.NonemptyViews < 0 || p.EmptyViews < 0 || p.UnknownAgeViews < 0 || p.NonemptyViews+p.EmptyViews+c.UnsuccessfulCollections > c.Observed || p.UnknownAgeViews > min(p.NonemptyViews, c.UnknownAgeViews) || p.NonemptyViews > c.NonemptyViews || p.EmptyViews < c.EmptyViews || p.NonemptyViews+p.EmptyViews < c.Complete {
			return false
		}
		waiting += p.NonemptyViews
		unknown += p.UnknownAgeViews
	}
	if waiting < c.NonemptyViews || unknown < c.UnknownAgeViews {
		return false
	}
	incomplete := runtimeAppTaskIncomplete(c, inventoryUnknown)
	state, reason := runtimeAppTaskState(c, incomplete)
	return h.Incomplete == incomplete && h.State == state && h.Reason == reason
}

func RuntimeAppTaskHealthValid(base api.ObsRuntimeBuilds, h *RuntimeAppTaskHealth) bool {
	if h == nil || !RuntimeAppTaskSummaryValid(base, h.RuntimeAppTaskSummary) || len(h.Items) > api.ObsRuntimeBuildPreviewMax || h.OmittedItems < 0 || len(h.Items)+h.OmittedItems != runtimeAppTaskTotal(h.Counts) {
		return false
	}
	part, seen := newRuntimeAppTaskSummary(), map[string]bool{}
	for _, item := range h.Items {
		key := item.TargetID
		if key == "" {
			key = item.Job + "/missing/" + item.Node
		}
		if seen[key] || !runtimeAppTaskItemValid(base, item) {
			return false
		}
		seen[key] = true
		accountRuntimeAppTaskItem(&part, item)
	}
	full, preview := runtimeAppTaskCountValues(h.Counts), runtimeAppTaskCountValues(part.Counts)
	for i := 1; i < len(full); i++ {
		if preview[i] > full[i] || h.OmittedItems == 0 && preview[i] != full[i] {
			return false
		}
	}
	for i, p := range part.Classes {
		f := h.Classes[i]
		if p.NonemptyViews > f.NonemptyViews || p.EmptyViews > f.EmptyViews || p.UnknownAgeViews > f.UnknownAgeViews || h.OmittedItems == 0 && p != f {
			return false
		}
	}
	return true
}

func runtimeAppTaskItemValid(base api.ObsRuntimeBuilds, item RuntimeAppTaskItem) bool {
	if !slices.Contains(runtimeAppTaskJobs(base.ExpectedRole), item.Job) || len(item.Classes) != len(runtimeAppTaskClasses()) || item.Node != "" && !ServingProbeNodeValid(item.Node) || item.TargetID != "" && !runtimeTargetIDValid(item.TargetID) || item.Unexpected && item.TargetID == "" || item.State != runtimeAppTaskItemState(item) {
		return false
	}
	if at := item.ScrapeObservedAt; at != nil && (at.IsZero() || at.After(base.GeneratedAt.Add(api.ObsHealthClockSkewTolerance))) {
		return false
	}
	bridge := RuntimeHandoffItem{TargetID: item.TargetID, ScrapeObservedAt: item.ScrapeObservedAt}
	if !runtimeHandoffSignalValid(base, bridge, item.Collection.RuntimeHandoffSignal, item.Collection.Values != nil, []string{"available"}) || !runtimeHandoffSignalValid(base, bridge, item.Observation.RuntimeHandoffSignal, item.Observation.Values != nil, []string{"available"}) {
		return false
	}
	if v := item.Collection.Values; v != nil && (!runtimeHandoffSampleClockValid(v.LastSuccessAt, item.Collection.ObservedAt, base.GeneratedAt) || v.Success && v.LastSuccessAt == nil) {
		return false
	}
	if !runtimeHandoffSignalValid(base, bridge, item.Dispatch.RuntimeHandoffSignal, item.Dispatch.Values != nil, []string{"disabled", "running", "stopped"}) {
		return false
	}
	if v := item.Dispatch.Values; v != nil && (!runtimeAppTaskDispatchValuesValid(*v, base.GeneratedAt, item.Dispatch.ObservedAt) || item.Dispatch.State != runtimeAppTaskDispatchState(*v)) {
		return false
	}
	prerequisite := runtimeAppTaskPrerequisite(item.Collection, base.EvidenceAfter, base.GeneratedAt)
	for i, p := range item.Classes {
		if p.Kind+"/"+p.Class != runtimeAppTaskClasses()[i] || !runtimeHandoffSignalValid(base, bridge, p.RuntimeHandoffSignal, p.Values != nil, []string{"nonempty", "empty"}) {
			return false
		}
		if prerequisite.State != "" {
			if p.Values != nil || p.State != prerequisite.State || p.Reason != prerequisite.Reason {
				return false
			}
		} else if slices.Contains([]string{"collection_failed", "collection_not_observed", "collection_before_boundary", "collection_stale"}, p.Reason) {
			return false
		}
		if v := p.Values; v != nil {
			if !runtimeAppTaskClassValuesValid(*v, base.GeneratedAt) || runtimeAppTaskUnknownAgeClass(p.Class) && v.Pending != v.UnknownAge || !runtimeHandoffSampleClockValid(v.OldestBoundaryAt, p.ObservedAt, base.GeneratedAt) || (p.State == "nonempty") != (v.Pending > 0) || item.Collection.ObservedAt == nil || !p.ObservedAt.Equal(*item.Collection.ObservedAt) {
				return false
			}
		}
	}
	return true
}
