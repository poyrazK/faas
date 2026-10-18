package operatorobs

import (
	"context"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

const RuleCoveragePath = "/v1/admin/obs/rule-coverage"

// RuleCoverageLimitations returns the closed, safe scope of this comparison.
func RuleCoverageLimitations() []string {
	return []string{"expectation_selected_per_request_not_deployment_evidence", "selected_set_is_a_minimum_not_an_exclusive_inventory", "alert_rules_only_recording_rules_not_checked", "coverage_not_evaluation_metric_or_notification_health", "canonical_syntax_not_semantic_equivalence", "group_intervals_offsets_annotations_and_source_paths_not_compared", "raw_queries_labels_and_errors_excluded", "preview_bounded_totals_cover_complete_source"}
}

func RuleCoverageSelection(raw string, forceQuery bool) (string, error) {
	q, err := url.ParseQuery(raw)
	if err != nil || forceQuery || len(q) > 1 {
		return "", state.ErrInvalidArgument
	}
	if len(q) == 0 {
		if raw != "" {
			return "", state.ErrInvalidArgument
		}
		return "", nil
	}
	if len(q["expected_set"]) != 1 || !obsrulecatalog.KnownSetID(q.Get("expected_set")) {
		return "", state.ErrInvalidArgument
	}
	return q.Get("expected_set"), nil
}

// RuleCoverage reads inventory only. It never writes history or infers a
// deployment expectation from the catalog's presence in the apid binary.
func RuleCoverage(ctx context.Context, client *promql.Client, selected string, now time.Time) api.ObsRuleCoverage {
	out := api.ObsRuleCoverage{GeneratedAt: now.UTC(), Scope: "selected_expected_alert_rules", FingerprintAlgorithm: obsrulecatalog.FingerprintAlgorithm, SelectionSource: "none", State: "unknown", Reason: "expected_set_not_selected", AvailableSets: []api.ObsRuleCoverageSet{}, Items: []api.ObsRuleCoverageItem{}, Limit: api.ObsRuleCoverageItemsMax,
		Limitations: RuleCoverageLimitations()}
	if selected != "" {
		out.SelectionSource = "explicit_request"
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsRuleCoverageBudget)
	defer cancel()
	catalog, err := obsrulecatalog.Load()
	if err != nil {
		out.Reason = "expected_catalog_unavailable"
		return out
	}
	var expected *obsrulecatalog.Set
	for i := range catalog.Sets {
		set := &catalog.Sets[i]
		meta := coverageSetMetadata(*set)
		out.AvailableSets = append(out.AvailableSets, meta)
		if set.ID == selected {
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
	if client == nil || client.BaseURL() == "" {
		out.Reason = "prometheus_not_configured"
		return out
	}
	groups, err := client.AlertRules(ctx)
	if err != nil {
		out.Reason = "source_unavailable"
		return out
	}
	assessed, err := assessRuleCoverage(ctx, groups, *expected, out)
	if err != nil || ctx.Err() != nil {
		out.Reason = "deadline_exceeded"
		return out
	}
	return assessed
}

func coverageSetMetadata(set obsrulecatalog.Set) api.ObsRuleCoverageSet {
	return api.ObsRuleCoverageSet{ID: set.ID, Revision: set.Revision, Files: append([]string{}, set.Files...), ExpectedRules: len(set.Rules)}
}
