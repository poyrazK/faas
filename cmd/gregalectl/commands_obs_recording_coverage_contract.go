package main

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
)

func recordingCoverageBase(out api.ObsRecordingCoverage) api.ObsRuleCoverage {
	base := api.ObsRuleCoverage{GeneratedAt: out.GeneratedAt, Scope: out.Scope, FingerprintAlgorithm: out.FingerprintAlgorithm, SelectionSource: out.SelectionSource, State: out.State, Reason: out.Reason, ExpectedSet: out.ExpectedSet, AvailableSets: out.AvailableSets, Totals: out.Totals, Items: []api.ObsRuleCoverageItem{}, TotalItems: out.TotalItems, Limit: out.Limit, Truncated: out.Truncated, Limitations: out.Limitations}
	if out.Items == nil {
		base.Items = nil
	}
	for _, item := range out.Items {
		base.Items = append(base.Items, item.Comparison)
	}
	return base
}

func validObsRecordingCoverage(out api.ObsRecordingCoverage, selected string) bool {
	if out.Scope != "selected_expected_recording_rules" || !slices.Equal(out.Limitations, operatorobs.RecordingCoverageLimitations()) {
		return false
	}
	base := recordingCoverageBase(out)
	base.Scope = "selected_expected_alert_rules"
	base.Limitations = operatorobs.RuleCoverageLimitations()
	if !validObsRuleCoverage(base, selected) {
		return false
	}
	if out.ExpectedSet == nil {
		return out.Dependencies == nil
	}
	d := out.Dependencies
	if d == nil || d.Scope != "selected_expected_catalog_syntax" || !coverageHash(d.AlertSetRevision) || d.TotalAlerts < 1 || d.TotalAlerts > api.ObsAlertSnapshotMaxRules || !validRecordingReferences(d.UncertainAlerts, out.ExpectedSet.Files, d.TotalAlerts, api.ObsRecordingUnknownAlertsMax) {
		return false
	}
	state := "complete"
	if d.UncertainAlerts.TotalItems > 0 {
		state = "partial"
	}
	if d.State != state {
		return false
	}
	for _, item := range out.Items {
		if !validRecordingReferences(item.DependentAlerts, out.ExpectedSet.Files, d.TotalAlerts, api.ObsRecordingDependentAlertsMax) {
			return false
		}
		for _, field := range item.Comparison.ChangedFields {
			if field != "expression" && field != "labels" {
				return false
			}
		}
	}
	return true
}

func validRecordingReferences(out api.ObsRecordingAlertReferences, files []string, total, limit int) bool {
	if out.Items == nil || out.TotalItems < 0 || out.TotalItems > total || out.Limit != limit || len(out.Items) != min(out.TotalItems, limit) || out.Truncated != (out.TotalItems > len(out.Items)) {
		return false
	}
	previous := ""
	for _, ref := range out.Items {
		id, ok := obsrulecatalog.Identity(ref.FileName, ref.GroupName, ref.RuleName)
		if !ok || id != ref.ID || ref.ID <= previous || !slices.Contains(files, ref.FileName) {
			return false
		}
		previous = ref.ID
	}
	return true
}
