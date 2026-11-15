package main

import (
	"slices"
	"time"
)

func obsDeploymentStabilityResetDetails(reason string, at, previous time.Time, first, last, current obsDeploymentStabilityEvidence, blockers *obsDeploymentStabilityPreview) *obsDeploymentStabilityReset {
	out := &obsDeploymentStabilityReset{At: at.UTC(), Reason: reason, PreviousObservedAt: obsDeploymentStabilityClock(previous), SourceContext: "previous_window_blockers", Sources: cloneObsDeploymentStabilityPreview(blockers)}
	switch reason {
	case "observation_gap":
		out.ObservationGapSeconds = max(time.Duration(0), at.Sub(previous)).Seconds()
	case "evidence_set_changed":
		out.SourceContext = "reset_cause"
		out.Sources = obsDeploymentStabilityChangedSources(first, last, current)
	case "evidence_clock_regressed":
		out.SourceContext = "reset_cause"
		var items []obsDeploymentStabilityDetail
		for key, latest := range current {
			if latest.Before(last[key]) {
				items = append(items, obsDeploymentStabilitySourceDetail(key, "source_clock_regressed", first[key], last[key], latest))
			}
		}
		slices.SortFunc(items, compareObsDeploymentStabilityDetails)
		out.Sources = retainObsDeploymentStabilityPreview(items)
	}
	return out
}

func obsDeploymentStabilityChangedSources(first, last, current obsDeploymentStabilityEvidence) *obsDeploymentStabilityPreview {
	// Match identities without policy so a changed probe budget/age is one
	// explained change per clock, rather than an unrelated add/remove pair.
	previousByIdentity := map[obsDeploymentStabilitySource]obsDeploymentStabilitySource{}
	for key := range last {
		previousByIdentity[obsDeploymentStabilityIdentity(key)] = key
	}
	var items []obsDeploymentStabilityDetail
	for key, latest := range current {
		oldKey, found := previousByIdentity[obsDeploymentStabilityIdentity(key)]
		if !found {
			items = append(items, obsDeploymentStabilitySourceDetail(key, "source_added", time.Time{}, time.Time{}, latest))
			continue
		}
		delete(previousByIdentity, obsDeploymentStabilityIdentity(key))
		if key != oldKey {
			item := obsDeploymentStabilitySourceDetail(key, "probe_policy_changed", first[oldKey], last[oldKey], latest)
			item.PreviousExpectedFirstByteSeconds, item.PreviousMaxResultAgeSeconds = &oldKey.budget, &oldKey.maxAge
			items = append(items, item)
		}
	}
	for _, key := range previousByIdentity {
		items = append(items, obsDeploymentStabilitySourceDetail(key, "source_removed", first[key], last[key], time.Time{}))
	}
	slices.SortFunc(items, compareObsDeploymentStabilityDetails)
	return retainObsDeploymentStabilityPreview(items)
}

func obsDeploymentStabilityIdentity(key obsDeploymentStabilitySource) obsDeploymentStabilitySource {
	key.budget, key.maxAge = 0, 0
	return key
}
