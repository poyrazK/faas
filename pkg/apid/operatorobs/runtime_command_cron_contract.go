package operatorobs

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimeCommandCronCountValues(c RuntimeCommandCronCounts) []int {
	return []int{c.Expected, c.Observed, c.Complete, c.Missing, c.Stale, c.Conflicting, c.Unavailable, c.Unexpected, c.NonemptyScopes, c.EmptyScopes, c.UnknownAgeScopes, c.UnsuccessfulCollections, c.EnabledScopes, c.DisabledScopes, c.DueScopes, c.InactiveScopes, c.SweepRunningScopes, c.SweepIdleScopes, c.SweepNeverStartedScopes, c.SweepNeverSucceededScopes}
}

func runtimeCommandCronTotal(c RuntimeCommandCronCounts) int {
	return c.Complete + c.Missing + c.Stale + c.Conflicting + c.Unavailable
}

func RuntimeCommandCronSummaryValid(base api.ObsRuntimeBuilds, h RuntimeCommandCronSummary) bool {
	jobs := runtimeCommandCronJobs(base.ExpectedRole)
	if len(jobs) == 0 || h.Scope != RuntimeCommandCronScope {
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
	for _, n := range runtimeCommandCronCountValues(c) {
		if n < 0 || n > maximum {
			return false
		}
	}
	if c.DueScopes > c.NonemptyScopes || c.InactiveScopes > c.NonemptyScopes || c.SweepRunningScopes+c.SweepIdleScopes > c.Observed || c.Complete > c.SweepRunningScopes+c.SweepIdleScopes || c.SweepNeverStartedScopes > min(c.SweepIdleScopes, c.SweepNeverSucceededScopes) || c.SweepNeverSucceededScopes > c.SweepRunningScopes+c.SweepIdleScopes {
		return false
	}
	total := runtimeCommandCronTotal(c)
	if c.Expected != expected || total < c.Expected || total > maximum || c.Observed > min(total, base.Counts.Observed) || c.Unexpected > c.Observed || c.NonemptyScopes+c.EmptyScopes+c.UnsuccessfulCollections > c.Observed || c.UnknownAgeScopes > c.NonemptyScopes || c.EnabledScopes+c.DisabledScopes > c.Observed || c.Complete > min(c.EnabledScopes+c.DisabledScopes, c.NonemptyScopes+c.EmptyScopes) {
		return false
	}
	if len(h.Classes) != len(runtimeCommandCronClasses()) {
		return false
	}
	waiting, unknown, due, inactive := 0, 0, 0, 0
	for i, p := range h.Classes {
		if runtimeCommandCronUnknownAgeClass(p.Class) && p.NonemptyScopes != p.UnknownAgeScopes {
			return false
		}
		if p.NonemptyScopes > c.Observed || p.EmptyScopes > c.Observed || p.UnknownAgeScopes > c.Observed {
			return false
		}
		if p.Class != runtimeCommandCronClasses()[i] || p.NonemptyScopes < 0 || p.EmptyScopes < 0 || p.UnknownAgeScopes < 0 || p.NonemptyScopes+p.EmptyScopes+c.UnsuccessfulCollections > c.Observed || p.UnknownAgeScopes > min(p.NonemptyScopes, c.UnknownAgeScopes) || p.NonemptyScopes > c.NonemptyScopes || p.EmptyScopes < c.EmptyScopes || p.NonemptyScopes+p.EmptyScopes < c.Complete {
			return false
		}
		if runtimeCommandCronDueClass(p.Class) {
			due += p.NonemptyScopes
			if p.NonemptyScopes > c.DueScopes {
				return false
			}
		}
		if runtimeCommandCronInactiveClass(p.Class) {
			inactive += p.NonemptyScopes
			if p.NonemptyScopes > c.InactiveScopes {
				return false
			}
		}
		waiting += p.NonemptyScopes
		unknown += p.UnknownAgeScopes
	}
	if waiting < c.NonemptyScopes || unknown < c.UnknownAgeScopes || due < c.DueScopes || inactive < c.InactiveScopes || c.DueScopes+c.InactiveScopes+h.Classes[len(h.Classes)-1].NonemptyScopes < c.NonemptyScopes {
		return false
	}
	incomplete := runtimeCommandCronIncomplete(c, inventoryUnknown)
	state, reason := runtimeCommandCronState(c, incomplete)
	return h.Incomplete == incomplete && h.State == state && h.Reason == reason
}

func RuntimeCommandCronHealthValid(base api.ObsRuntimeBuilds, h *RuntimeCommandCronHealth) bool {
	if h == nil || !RuntimeCommandCronSummaryValid(base, h.RuntimeCommandCronSummary) || len(h.Items) > api.ObsRuntimeBuildPreviewMax || h.OmittedItems < 0 || len(h.Items)+h.OmittedItems != runtimeCommandCronTotal(h.Counts) {
		return false
	}
	part, seen := newRuntimeCommandCronSummary(), map[string]bool{}
	for _, item := range h.Items {
		key := item.TargetID
		if key == "" {
			key = item.Job + "/missing/" + item.Node
		}
		if seen[key] || !runtimeCommandCronItemValid(base, item) {
			return false
		}
		seen[key] = true
		accountRuntimeCommandCronItem(&part, item)
	}
	full, preview := runtimeCommandCronCountValues(h.Counts), runtimeCommandCronCountValues(part.Counts)
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

func runtimeCommandCronItemValid(base api.ObsRuntimeBuilds, item RuntimeCommandCronItem) bool {
	if !slices.Contains(runtimeCommandCronJobs(base.ExpectedRole), item.Job) || len(item.Classes) != len(runtimeCommandCronClasses()) || item.Node != "" && !ServingProbeNodeValid(item.Node) || item.TargetID != "" && !runtimeTargetIDValid(item.TargetID) || item.Unexpected && item.TargetID == "" || item.State != runtimeCommandCronItemState(item) {
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
	if !runtimeHandoffSignalValid(base, bridge, item.Sweep.RuntimeHandoffSignal, item.Sweep.Values != nil, []string{"available"}) {
		return false
	}
	if v := item.Sweep.Values; v != nil && !runtimeCommandCronSweepValuesValid(*v, base.GeneratedAt, item.Sweep.ObservedAt) {
		return false
	}
	prerequisite := runtimeCommandCronPrerequisite(item.Collection, base.EvidenceAfter, base.GeneratedAt)
	for i, p := range item.Classes {
		if p.Class != runtimeCommandCronClasses()[i] || !runtimeHandoffSignalValid(base, bridge, p.RuntimeHandoffSignal, p.Values != nil, []string{"nonempty", "empty"}) {
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
			if !runtimeCommandCronClassValuesValid(*v, base.GeneratedAt) || runtimeCommandCronUnknownAgeClass(p.Class) && v.Schedules != v.UnknownAge || !runtimeHandoffSampleClockValid(v.OldestDueAt, p.ObservedAt, base.GeneratedAt) || (p.State == "nonempty") != (v.Schedules > 0) || item.Collection.ObservedAt == nil || !p.ObservedAt.Equal(*item.Collection.ObservedAt) {
				return false
			}
		}
	}
	return true
}
