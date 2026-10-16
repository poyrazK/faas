package main

import (
	"slices"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
)

func coverageHash(value string) bool {
	return operatorobs.ValidateReference("platform_alert", value) == nil
}

func validCoverageSet(s api.ObsRuleCoverageSet) bool {
	if !obsrulecatalog.KnownSetID(s.ID) || !coverageHash(s.Revision) || s.ExpectedRules < 1 || s.ExpectedRules > api.ObsAlertSnapshotMaxRules || len(s.Files) < 1 || len(s.Files) > api.ObsRuleCoverageExpectedFilesMax || !sort.StringsAreSorted(s.Files) {
		return false
	}
	for i, file := range s.Files {
		if !obsrulecatalog.SafeIdentity(file) || i > 0 && file == s.Files[i-1] {
			return false
		}
	}
	return true
}

func validObsRuleCoverage(out api.ObsRuleCoverage, selected string) bool {
	if out.GeneratedAt.IsZero() || out.Scope != "selected_expected_alert_rules" || out.FingerprintAlgorithm != obsrulecatalog.FingerprintAlgorithm || out.State == "" || out.Items == nil || out.AvailableSets == nil || !slices.Equal(out.Limitations, operatorobs.RuleCoverageLimitations()) || out.Limit != api.ObsRuleCoverageItemsMax || out.TotalItems < 0 || out.TotalItems > api.ObsAlertSnapshotMaxRules || len(out.Items) != min(out.TotalItems, out.Limit) || out.Truncated != (out.TotalItems > len(out.Items)) {
		return false
	}
	sets := map[string]api.ObsRuleCoverageSet{}
	for _, s := range out.AvailableSets {
		if !validCoverageSet(s) || sets[s.ID].ID != "" {
			return false
		}
		sets[s.ID] = s
	}
	if len(sets) > api.ObsRuleCoverageExpectedSetsMax {
		return false
	}
	if out.ExpectedSet != nil {
		s := out.ExpectedSet
		available := sets[s.ID]
		if selected == "" || s.ID != selected || !validCoverageSet(*s) || available.Revision != s.Revision || available.ExpectedRules != s.ExpectedRules || !slices.Equal(available.Files, s.Files) {
			return false
		}
	}
	if selected == "" && (out.SelectionSource != "none" || out.ExpectedSet != nil) || selected != "" && out.SelectionSource != "explicit_request" {
		return false
	}
	if out.Totals == nil {
		if out.State != "unknown" || out.TotalItems != 0 {
			return false
		}
		switch out.Reason {
		case "expected_set_not_selected":
			return selected == "" && len(sets) == 2
		case "expected_catalog_unavailable":
			return len(sets) == 0 && out.ExpectedSet == nil
		case "expected_set_unavailable":
			return selected != "" && out.ExpectedSet == nil
		case "prometheus_not_configured", "source_unavailable", "deadline_exceeded":
			return selected != "" && out.ExpectedSet != nil
		default:
			return false
		}
	}
	if out.ExpectedSet == nil || selected == "" || out.TotalItems != out.ExpectedSet.ExpectedRules {
		return false
	}
	t := out.Totals
	for _, n := range []int{t.ExpectedRules, t.ObservedRules, t.AdditionalRules, t.InvalidObservedRules, t.InvalidObservedGroups, t.Matching, t.Missing, t.Changed, t.Ambiguous, t.Unknown} {
		if n < 0 || n > api.ObsAlertSnapshotMaxRules {
			return false
		}
	}
	if t.ExpectedRules != out.ExpectedSet.ExpectedRules || t.Matching+t.Missing+t.Changed+t.Ambiguous+t.Unknown != t.ExpectedRules || t.InvalidObservedRules+t.AdditionalRules > t.ObservedRules {
		return false
	}
	state, reason := "covered", ""
	if t.Missing+t.Changed+t.Ambiguous > 0 {
		state, reason = "degraded", "expected_rules_differ"
	} else if t.Unknown+t.InvalidObservedRules+t.InvalidObservedGroups > 0 {
		state, reason = "partial", "comparison_incomplete"
		if t.Matching == 0 {
			state = "unknown"
		}
	}
	if out.State != state || out.Reason != reason {
		return false
	}
	counts := map[string]int{}
	seen := map[string]bool{}
	observed := 0
	for i, r := range out.Items {
		if !validCoverageItem(r, out.ExpectedSet.Files) || seen[r.ID] {
			return false
		}
		seen[r.ID] = true
		if i > 0 {
			previous := out.Items[i-1]
			if coverageItemPriority(r.State) < coverageItemPriority(previous.State) || r.State == previous.State && r.ID <= previous.ID {
				return false
			}
		}
		counts[r.State]++
		observed += r.ObservedCandidates
	}
	for state, total := range map[string]int{"matching": t.Matching, "missing": t.Missing, "changed": t.Changed, "ambiguous": t.Ambiguous, "unknown": t.Unknown} {
		if counts[state] > total || !out.Truncated && counts[state] != total {
			return false
		}
	}
	attributable := t.ObservedRules - t.AdditionalRules - t.InvalidObservedRules
	return observed <= attributable && (out.Truncated || observed == attributable)
}

func validCoverageItem(r api.ObsRuleCoverageItem, files []string) bool {
	id, ok := obsrulecatalog.Identity(r.FileName, r.GroupName, r.RuleName)
	if !ok || r.ID != id || !slices.Contains(files, r.FileName) || !coverageHash(r.ExpectedFingerprint) || r.ChangedFields == nil || r.ObservedCandidates < 0 || r.ObservedCandidates > api.ObsAlertSnapshotMaxRules {
		return false
	}
	switch r.State {
	case "matching":
		return r.Reason == "" && r.ObservedCandidates == 1 && r.ObservedFingerprint == r.ExpectedFingerprint && len(r.ChangedFields) == 0
	case "missing":
		return r.Reason == "rule_missing" && r.ObservedCandidates == 0 && r.ObservedFingerprint == "" && len(r.ChangedFields) == 0
	case "changed":
		if r.Reason != "definition_changed" || r.ObservedCandidates != 1 || !coverageHash(r.ObservedFingerprint) || r.ObservedFingerprint == r.ExpectedFingerprint || len(r.ChangedFields) == 0 {
			return false
		}
		last := -1
		for _, field := range r.ChangedFields {
			p := slices.Index([]string{"expression", "for", "keep_firing_for", "labels"}, field)
			if p <= last {
				return false
			}
			last = p
		}
		return true
	case "ambiguous":
		return r.Reason == "ambiguous_identity" && r.ObservedCandidates >= 1 && r.ObservedFingerprint == "" && len(r.ChangedFields) == 0
	case "unknown":
		return r.ObservedFingerprint == "" && len(r.ChangedFields) == 0 && (r.Reason == "source_identity_unavailable" && r.ObservedCandidates == 0 || r.Reason == "definition_unavailable" && r.ObservedCandidates == 1)
	default:
		return false
	}
}

func coverageItemPriority(state string) int {
	switch state {
	case "ambiguous":
		return 0
	case "changed":
		return 1
	case "missing":
		return 2
	case "unknown":
		return 3
	default:
		return 4
	}
}
