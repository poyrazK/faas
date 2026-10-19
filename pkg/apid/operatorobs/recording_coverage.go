package operatorobs

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
	"github.com/onebox-faas/faas/pkg/promql"
)

const RecordingCoveragePath = "/v1/admin/obs/recording-coverage"

func RecordingCoverageLimitations() []string {
	return []string{"expectation_selected_per_request_not_deployment_evidence", "selected_set_is_a_minimum_not_an_exclusive_inventory", "recording_definitions_not_evaluation_or_metric_health", "dependencies_are_expected_catalog_syntax_not_proven_impact", "nonexact_selectors_duplicate_names_and_cycles_leave_uncertainty", "missing_recording_labels_are_empty_per_prometheus_wire_contract", "group_intervals_offsets_annotations_and_source_paths_not_compared", "raw_queries_labels_and_errors_excluded", "previews_bounded_totals_cover_complete_inputs"}
}

func RecordingCoverage(ctx context.Context, client *promql.Client, selected string, now time.Time) api.ObsRecordingCoverage {
	out := api.ObsRecordingCoverage{GeneratedAt: now.UTC(), Scope: "selected_expected_recording_rules", FingerprintAlgorithm: obsrulecatalog.FingerprintAlgorithm, SelectionSource: "none", State: "unknown", Reason: "expected_set_not_selected", AvailableSets: []api.ObsRuleCoverageSet{}, Items: []api.ObsRecordingCoverageItem{}, Limit: api.ObsRuleCoverageItemsMax, Limitations: RecordingCoverageLimitations()}
	if selected != "" {
		out.SelectionSource = "explicit_request"
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsRuleCoverageBudget)
	defer cancel()
	catalog, err := obsrulecatalog.LoadRecordings()
	if err != nil {
		out.Reason = "expected_catalog_unavailable"
		return out
	}
	var expected *obsrulecatalog.RecordingSet
	for i := range catalog.Sets {
		set := &catalog.Sets[i]
		meta := coverageSetMetadata(set.Expected)
		meta.Revision = set.Revision
		out.AvailableSets = append(out.AvailableSets, meta)
		if set.Expected.ID == selected {
			expected = set
			out.ExpectedSet = &meta
		}
	}
	if selected == "" {
		return out
	}
	if expected == nil {
		out.Reason = "expected_set_unavailable"
		return out
	}
	out.Dependencies = recordingDependencyMapping(*expected)
	if client == nil || client.BaseURL() == "" {
		out.Reason = "prometheus_not_configured"
		return out
	}
	groups, err := client.RecordingRules(ctx)
	if err != nil {
		out.Reason = "source_unavailable"
		return out
	}
	base := api.ObsRuleCoverage{Items: []api.ObsRuleCoverageItem{}, Limit: out.Limit}
	assessed, err := assessDefinitionCoverage(ctx, groups, expected.Expected, base, "recording")
	if err != nil || ctx.Err() != nil {
		out.Reason = "deadline_exceeded"
		return out
	}
	out.State, out.Reason, out.Totals = assessed.State, assessed.Reason, assessed.Totals
	out.TotalItems, out.Truncated = assessed.TotalItems, assessed.Truncated
	deps := map[string][]obsrulecatalog.RuleReference{}
	for _, dep := range expected.Dependents {
		deps[dep.RuleID] = dep.Alerts
	}
	for _, item := range assessed.Items {
		out.Items = append(out.Items, api.ObsRecordingCoverageItem{Comparison: item, DependentAlerts: recordingAlertReferences(deps[item.ID], api.ObsRecordingDependentAlertsMax)})
	}
	if ctx.Err() != nil {
		out.Totals = nil
		out.Items = []api.ObsRecordingCoverageItem{}
		out.TotalItems = 0
		out.Truncated = false
		out.State, out.Reason = "unknown", "deadline_exceeded"
	}
	return out
}

func recordingDependencyMapping(set obsrulecatalog.RecordingSet) *api.ObsRecordingDependencyMapping {
	state := "complete"
	if len(set.UncertainAlerts) > 0 {
		state = "partial"
	}
	return &api.ObsRecordingDependencyMapping{Scope: "selected_expected_catalog_syntax", State: state, AlertSetRevision: set.AlertSetRevision, TotalAlerts: set.AlertCount, UncertainAlerts: recordingAlertReferences(set.UncertainAlerts, api.ObsRecordingUnknownAlertsMax)}
}

func recordingAlertReferences(refs []obsrulecatalog.RuleReference, limit int) api.ObsRecordingAlertReferences {
	out := api.ObsRecordingAlertReferences{Items: []api.ObsRecordingAlertReference{}, TotalItems: len(refs), Limit: limit, Truncated: len(refs) > limit}
	for _, r := range refs[:min(len(refs), limit)] {
		out.Items = append(out.Items, api.ObsRecordingAlertReference{ID: r.ID, FileName: r.FileName, GroupName: r.GroupName, RuleName: r.RuleName})
	}
	return out
}
