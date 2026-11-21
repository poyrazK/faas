package operatorobs

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimeSnapshotPrimeCountValues(c RuntimeSnapshotPrimeCounts) []int {
	return []int{c.Expected, c.Observed, c.Complete, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, c.NonemptyScopes, c.EmptyScopes, c.UnknownAgeScopes, c.UnsuccessfulCollections, c.EnabledScopes, c.DisabledScopes}
}

func runtimeSnapshotPrimeTotal(c RuntimeSnapshotPrimeCounts) int {
	return c.Complete + c.Missing + c.Stale + c.Conflicting + c.Unavailable
}

func RuntimeSnapshotPrimeSummaryValid(base api.ObsRuntimeBuilds, h RuntimeSnapshotPrimeSummary) bool {
	jobs := runtimeSnapshotPrimeJobs(base.ExpectedRole)
	if len(jobs) == 0 {
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
	for _, n := range runtimeSnapshotPrimeCountValues(c) {
		if n < 0 || n > maximum {
			return false
		}
	}
	total := runtimeSnapshotPrimeTotal(c)
	if c.Expected != expected || total < c.Expected || total > maximum || c.Observed > min(total, base.Counts.Observed) || c.Unexpected > c.Observed || c.NonemptyScopes+c.EmptyScopes+c.UnsuccessfulCollections > c.Observed || c.UnknownAgeScopes > c.NonemptyScopes || c.EnabledScopes+c.DisabledScopes > c.Observed || c.Complete > min(c.EnabledScopes+c.DisabledScopes, c.NonemptyScopes+c.EmptyScopes) {
		return false
	}
	if len(h.Classes) != len(runtimeSnapshotPrimeClasses()) {
		return false
	}
	waiting, unknown := 0, 0
	for i, p := range h.Classes {
		if p.Class == "unknown" && p.NonemptyScopes != p.UnknownAgeScopes {
			return false
		}
		if p.NonemptyScopes > c.Observed || p.EmptyScopes > c.Observed || p.UnknownAgeScopes > c.Observed {
			return false
		}
		if p.Class != runtimeSnapshotPrimeClasses()[i] || p.NonemptyScopes < 0 || p.EmptyScopes < 0 || p.UnknownAgeScopes < 0 || p.NonemptyScopes+p.EmptyScopes+c.UnsuccessfulCollections > c.Observed || p.UnknownAgeScopes > min(p.NonemptyScopes, c.UnknownAgeScopes) || p.NonemptyScopes > c.NonemptyScopes || p.EmptyScopes < c.EmptyScopes || p.NonemptyScopes+p.EmptyScopes < c.Complete {
			return false
		}
		waiting += p.NonemptyScopes
		unknown += p.UnknownAgeScopes
	}
	if waiting < c.NonemptyScopes || unknown < c.UnknownAgeScopes {
		return false
	}
	incomplete := runtimeSnapshotPrimeIncomplete(c, inventoryUnknown)
	state, reason := runtimeSnapshotPrimeState(c, incomplete)
	return h.Incomplete == incomplete && h.State == state && h.Reason == reason
}

func RuntimeSnapshotPrimeHealthValid(base api.ObsRuntimeBuilds, h *RuntimeSnapshotPrimeHealth) bool {
	if h == nil || !RuntimeSnapshotPrimeSummaryValid(base, h.RuntimeSnapshotPrimeSummary) || len(h.Items) > api.ObsRuntimeBuildPreviewMax || h.OmittedItems < 0 || len(h.Items)+h.OmittedItems != runtimeSnapshotPrimeTotal(h.Counts) {
		return false
	}
	part, seen := newRuntimeSnapshotPrimeSummary(), map[string]bool{}
	for _, item := range h.Items {
		key := item.TargetID
		if key == "" {
			key = item.Job + "/missing/" + item.Node
		}
		if seen[key] || !runtimeSnapshotPrimeItemValid(base, item) {
			return false
		}
		seen[key] = true
		accountRuntimeSnapshotPrimeItem(&part, item)
	}
	full, preview := runtimeSnapshotPrimeCountValues(h.Counts), runtimeSnapshotPrimeCountValues(part.Counts)
	for i := 1; i < len(full); i++ {
		if preview[i] > full[i] || h.OmittedItems == 0 && preview[i] != full[i] {
			return false
		}
	}
	for i, p := range part.Classes {
		f := h.Classes[i]
		if p.NonemptyScopes > f.NonemptyScopes || p.EmptyScopes > f.EmptyScopes || p.UnknownAgeScopes > f.UnknownAgeScopes || h.OmittedItems == 0 && p != f {
			return false
		}
	}
	return true
}

func runtimeSnapshotPrimeItemValid(base api.ObsRuntimeBuilds, item RuntimeSnapshotPrimeItem) bool {
	if !slices.Contains(runtimeSnapshotPrimeJobs(base.ExpectedRole), item.Job) || len(item.Classes) != len(runtimeSnapshotPrimeClasses()) || item.Node != "" && !ServingProbeNodeValid(item.Node) || item.TargetID != "" && !runtimeTargetIDValid(item.TargetID) || item.Unexpected && item.TargetID == "" || item.State != runtimeSnapshotPrimeItemState(item) {
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
	prerequisite := runtimeSnapshotPrimePrerequisite(item.Collection, base.EvidenceAfter, base.GeneratedAt)
	for i, p := range item.Classes {
		if p.Class != runtimeSnapshotPrimeClasses()[i] || !runtimeHandoffSignalValid(base, bridge, p.RuntimeHandoffSignal, p.Values != nil, []string{"nonempty", "empty"}) {
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
			if !runtimeSnapshotPrimeClassValuesValid(*v, base.GeneratedAt) || p.Class == "unknown" && v.Pending != v.UnknownAge || !runtimeHandoffSampleClockValid(v.OldestAttemptAt, p.ObservedAt, base.GeneratedAt) || (p.State == "nonempty") != (v.Pending > 0) || item.Collection.ObservedAt == nil || !p.ObservedAt.Equal(*item.Collection.ObservedAt) {
				return false
			}
		}
	}
	return true
}
