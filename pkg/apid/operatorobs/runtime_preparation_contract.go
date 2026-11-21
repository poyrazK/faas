package operatorobs

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimePreparationCountValues(c RuntimePreparationCounts) []int {
	return []int{c.Expected, c.Observed, c.Complete, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, c.WaitingScopes, c.EmptyScopes, c.UnknownAgeScopes, c.UnsuccessfulCollections, c.EnabledScopes, c.DisabledScopes}
}

func runtimePreparationTotal(c RuntimePreparationCounts) int {
	return c.Complete + c.Missing + c.Stale + c.Conflicting + c.Unavailable
}

func RuntimePreparationSummaryValid(base api.ObsRuntimeBuilds, h RuntimePreparationSummary) bool {
	job := slices.IndexFunc(base.Jobs, func(j api.ObsRuntimeBuildJob) bool { return j.Job == "imaged" && j.Daemon == "imaged" })
	if job < 0 || base.ExpectedRole != "single-box-v2" && base.ExpectedRole != "control-plane-v2" {
		return false
	}
	c := h.Counts
	for _, n := range runtimePreparationCountValues(c) {
		if n < 0 || n > api.ObsRuntimeBuildSamplesMax+api.ObsRuntimeBuildNodesMax {
			return false
		}
	}
	total := runtimePreparationTotal(c)
	if c.Expected != base.Jobs[job].Expected || total < c.Expected || total > api.ObsRuntimeBuildSamplesMax+api.ObsRuntimeBuildNodesMax || c.Observed > min(total, base.Counts.Observed) || c.Unexpected > c.Observed || c.WaitingScopes+c.EmptyScopes+c.UnsuccessfulCollections > c.Observed || c.UnknownAgeScopes > c.WaitingScopes || c.EnabledScopes+c.DisabledScopes > c.Observed || c.Complete > min(c.EnabledScopes+c.DisabledScopes, c.WaitingScopes+c.EmptyScopes) {
		return false
	}
	if len(h.Phases) != len(runtimePreparationPhases()) {
		return false
	}
	waiting, unknown := 0, 0
	for i, p := range h.Phases {
		if p.WaitingScopes > c.Observed || p.EmptyScopes > c.Observed || p.UnknownAgeScopes > c.Observed {
			return false
		}
		if p.Phase != runtimePreparationPhases()[i] || p.WaitingScopes < 0 || p.EmptyScopes < 0 || p.UnknownAgeScopes < 0 || p.WaitingScopes+p.EmptyScopes+c.UnsuccessfulCollections > c.Observed || p.UnknownAgeScopes > min(p.WaitingScopes, c.UnknownAgeScopes) || p.WaitingScopes > c.WaitingScopes || p.EmptyScopes < c.EmptyScopes || p.WaitingScopes+p.EmptyScopes < c.Complete {
			return false
		}
		waiting += p.WaitingScopes
		unknown += p.UnknownAgeScopes
	}
	if waiting < c.WaitingScopes || unknown < c.UnknownAgeScopes {
		return false
	}
	incomplete := runtimePreparationIncomplete(c, base.Jobs[job].State != "available")
	state, reason := runtimePreparationState(c, incomplete)
	return h.Incomplete == incomplete && h.State == state && h.Reason == reason
}

func RuntimePreparationHealthValid(base api.ObsRuntimeBuilds, h *RuntimePreparationHealth) bool {
	if h == nil || !RuntimePreparationSummaryValid(base, h.RuntimePreparationSummary) || len(h.Items) > api.ObsRuntimeBuildPreviewMax || h.OmittedItems < 0 || len(h.Items)+h.OmittedItems != runtimePreparationTotal(h.Counts) {
		return false
	}
	part, seen := newRuntimePreparationSummary(), map[string]bool{}
	for _, item := range h.Items {
		key := item.TargetID
		if key == "" {
			key = "missing/" + item.Node
		}
		if seen[key] || !runtimePreparationItemValid(base, item) {
			return false
		}
		seen[key] = true
		accountRuntimePreparationItem(&part, item)
	}
	full, preview := runtimePreparationCountValues(h.Counts), runtimePreparationCountValues(part.Counts)
	for i := 1; i < len(full); i++ {
		if preview[i] > full[i] || h.OmittedItems == 0 && preview[i] != full[i] {
			return false
		}
	}
	for i, p := range part.Phases {
		f := h.Phases[i]
		if p.WaitingScopes > f.WaitingScopes || p.EmptyScopes > f.EmptyScopes || p.UnknownAgeScopes > f.UnknownAgeScopes || h.OmittedItems == 0 && p != f {
			return false
		}
	}
	return true
}

func runtimePreparationItemValid(base api.ObsRuntimeBuilds, item RuntimePreparationItem) bool {
	if len(item.Phases) != len(runtimePreparationPhases()) || item.Node != "" && !ServingProbeNodeValid(item.Node) || item.TargetID != "" && !runtimeTargetIDValid(item.TargetID) || item.Unexpected && item.TargetID == "" || item.State != runtimePreparationItemState(item) {
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
	prerequisite := runtimePreparationPrerequisite(item.Collection, base.EvidenceAfter, base.GeneratedAt)
	for i, p := range item.Phases {
		if p.Phase != runtimePreparationPhases()[i] || !runtimeHandoffSignalValid(base, bridge, p.RuntimeHandoffSignal, p.Values != nil, []string{"waiting", "empty"}) {
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
			if !runtimePreparationPhaseValuesValid(*v, base.GeneratedAt) || !runtimeHandoffSampleClockValid(v.OldestPhaseStartedAt, p.ObservedAt, base.GeneratedAt) || (p.State == "waiting") != (v.Pending > 0) || item.Collection.ObservedAt == nil || !p.ObservedAt.Equal(*item.Collection.ObservedAt) {
				return false
			}
		}
	}
	return true
}
