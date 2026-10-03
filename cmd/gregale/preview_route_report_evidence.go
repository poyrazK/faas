package main

import (
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

func marshalPreviewReportDocument(doc map[string]any) ([]byte, error) { return json.Marshal(doc) }

func comparePreviewReportContracts(before, after *openapidiff.Spec) []previewReportRoute {
	rows, _ := comparePreviewContractsWithRequests(before, after)
	return rows
}

func comparePreviewContractsWithRequests(before, after *openapidiff.Spec) ([]previewReportRoute, previewReportEvidence) {
	rows := map[string]*previewReportRoute{}
	for _, spec := range []*openapidiff.Spec{before, after} {
		for path, item := range spec.Paths {
			if item == nil {
				continue
			}
			for method := range item.Methods {
				key := previewReportRouteKey(method, path)
				rows[key] = newPreviewReportRoute(method, path)
			}
		}
	}
	for _, row := range rows {
		row.Change = "unchanged"
		row.RouteSource = "captured_deployment_contract"
		base, candidate := previewReportOperation(before, row), previewReportOperation(after, row)
		switch {
		case base == nil:
			row.Change = "added"
		case candidate == nil:
			row.Change = "removed"
		}
	}
	for _, b := range openapidiff.Compare(before, after) {
		row := rows[previewReportRouteKey(b.Method, b.Path)]
		row.Breaks = append(row.Breaks, previewReportBreak{Kind: string(b.Kind), Status: b.Status, PathInSchema: b.PathInSchema})
		if row.Change != "removed" {
			row.Change = "changed"
		}
	}
	for _, a := range openapidiff.CompareAdditive(before, after) {
		if row := rows[previewReportRouteKey(a.Method, a.Path)]; row != nil && row.Change == "unchanged" {
			row.Change = "changed"
		}
	}
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]previewReportRoute, 0, len(keys))
	for _, key := range keys {
		result = append(result, *rows[key])
	}
	evidence := attachPreviewRequestComparison(result, before, after)
	return result, evidence
}

func newPreviewReportRoute(method, path string) *previewReportRoute {
	return &previewReportRoute{Method: strings.ToUpper(method), Path: path, Change: "unknown",
		RouteSource: "current_policy_or_observation",
		PolicyKinds: []string{}, TestProfiles: []previewReportTest{}, SourceTestProfiles: []previewReportTest{}, NextActions: []string{}, PolicyScope: "current_app"}
}

func previewReportOperation(spec *openapidiff.Spec, row *previewReportRoute) *openapidiff.Operation {
	if item := spec.Paths[row.Path]; item != nil {
		return item.Methods[strings.ToLower(row.Method)]
	}
	return nil
}

func attachPreviewReportPolicy(report *previewRouteReport, policy api.AppOpenAPIPolicyPreviewResponse, err error) {
	if err != nil {
		report.Policy = previewReportEvidence{Status: "unavailable", Reason: previewReportReadReason(err)}
		return
	}
	report.Policy = previewReportEvidence{Status: "available", Reason: "current_app_policy"}
	if policy.ObservedCapHit || (policy.ObservedAvailable && policy.ObservedSource != "live") {
		report.Notes = append(report.Notes, "Observed route inventory is bounded or partial; absence does not establish that a route is unused.")
	}
	seen := map[string]bool{}
	for _, row := range report.Routes {
		seen[previewReportRouteKey(row.Method, row.Path)] = true
	}
	for _, route := range policy.Routes {
		key := previewReportRouteKey(route.Method, route.Path)
		if !seen[key] {
			report.Routes = append(report.Routes, *newPreviewReportRoute(route.Method, route.Path))
			seen[key] = true
		}
	}
	sort.Slice(report.Routes, func(i, j int) bool {
		return previewReportRouteKey(report.Routes[i].Method, report.Routes[i].Path) < previewReportRouteKey(report.Routes[j].Method, report.Routes[j].Path)
	})
	rows := map[string]*previewReportRoute{}
	for i := range report.Routes {
		row := &report.Routes[i]
		rows[previewReportRouteKey(row.Method, row.Path)] = row
	}
	// Uncaptured operations remain unknown, even when current declarations or
	// observations establish useful policy/traffic context for them.
	for _, route := range policy.Routes {
		row := rows[previewReportRouteKey(route.Method, route.Path)]
		if row == nil || row.Change == "removed" {
			continue
		}
		kinds := map[string]bool{}
		for _, rule := range route.Rules {
			if rule.Enabled {
				kinds[rule.Kind] = true
			}
		}
		for kind := range kinds {
			row.PolicyKinds = append(row.PolicyKinds, kind)
		}
		sort.Strings(row.PolicyKinds)
	}
}

func attachPreviewReportTraffic(report *previewRouteReport, before, after api.RequestAnalyticsResponse, beforeErr, afterErr error) {
	report.Performance = previewReportEvidence{Status: "unavailable", Reason: "no_comparable_route_traffic"}
	if beforeErr != nil || afterErr != nil {
		if beforeErr != nil {
			report.Performance.Reason = "baseline:" + previewReportReadReason(beforeErr)
		} else {
			report.Performance.Reason = "candidate:" + previewReportReadReason(afterErr)
		}
		return
	}
	if before.WindowClamped || after.WindowClamped || before.RoutesTruncated || after.RoutesTruncated {
		report.Notes = append(report.Notes, "Traffic windows or route lists were clamped; reported observations are bounded.")
	}
	baselineRows := previewReportTrafficRows(before, report.BaselineDeployment)
	candidateRows := previewReportTrafficRows(after, report.CandidateDeployment)
	for i := range report.Routes {
		row := &report.Routes[i]
		key := previewReportRouteKey(row.Method, row.Path)
		row.BaselineTraffic, row.CandidateTraffic = baselineRows[key], candidateRows[key]
		if row.BaselineTraffic != nil && row.CandidateTraffic != nil {
			delta := row.CandidateTraffic.P95MS - row.BaselineTraffic.P95MS
			row.P95ChangeMS = &delta
			report.Performance = previewReportEvidence{Status: "advisory", Reason: "single_deployment_observed_traffic"}
		}
	}
}

func previewReportTrafficRows(analytics api.RequestAnalyticsResponse, deploymentID string) map[string]*previewReportTraffic {
	result := map[string]*previewReportTraffic{}
	if deploymentID == "" {
		return result
	}
	for _, row := range analytics.Routes {
		// Aggregate latency is safe to attribute only when every request in
		// this row belongs to the selected revision. Never mix deployments.
		if row.Requests <= 0 || len(row.DeploymentObservations) != 1 || row.OtherDeploymentRequests != 0 {
			continue
		}
		observation := row.DeploymentObservations[0]
		if observation.DeploymentID != deploymentID || observation.Requests != row.Requests {
			continue
		}
		path := strings.TrimPrefix(row.Route, strings.ToUpper(row.Method)+" ")
		result[previewReportRouteKey(row.Method, path)] = &previewReportTraffic{
			Requests: row.Requests, P95MS: row.P95MS, ErrorRatePct: row.ErrorRatePct,
			ColdRequests: row.ColdBoots, From: analytics.From, Until: analytics.Until,
		}
	}
	return result
}

func attachPreviewReportTests(report *previewRouteReport, receipts []testRunReceipt, digest string) {
	if digest == "" {
		return
	}
	report.TestReportSHA256 = digest
	report.Tests = previewReportEvidence{Status: "unavailable", Reason: "no_matching_deployment"}
	for _, receipt := range receipts {
		deploymentMatch := report.CandidateDeployment != "" && receipt.DeploymentID == report.CandidateDeployment && receipt.AppSlug == report.Preview
		sourceMatch := previewReportSourceMatches(report.CandidateSourceSHA256, receipt.SourceSHA256)
		if (!deploymentMatch && !sourceMatch) || receipt.Engine != "real-vm" || receipt.StartedAt.IsZero() || receipt.FinishedAt.Before(receipt.StartedAt) ||
			(receipt.Profile != "warm" && receipt.Profile != "cold" && receipt.Profile != "restored") {
			report.UnboundTestRuns++
			continue
		}
		for _, request := range receipt.Requests {
			for i := range report.Routes {
				row := &report.Routes[i]
				if strings.EqualFold(request.Method, row.Method) && previewReportPathMatches(row.Path, request.Path) {
					passed := request.Passed && receipt.Status == "passed" && receipt.Error == "" && receipt.CleanupError == ""
					if deploymentMatch {
						row.TestProfiles = attachPreviewReportTest(row.TestProfiles, receipt.Profile, passed)
						report.Tests = previewReportEvidence{Status: "available", Reason: "supplied_matching_deployment_receipts"}
					} else {
						row.SourceTestProfiles = attachPreviewReportTest(row.SourceTestProfiles, receipt.Profile, passed)
						if report.Tests.Status != "available" {
							report.Tests = previewReportEvidence{Status: "supplemental", Reason: "same_source_different_environment"}
						}
					}
				}
			}
		}
	}
	if report.UnboundTestRuns > 0 {
		report.Notes = append(report.Notes, "Test runs for local code or other deployments are supplemental evidence and do not establish candidate route coverage.")
	}
	if report.Tests.Status == "supplemental" {
		report.Notes = append(report.Notes, "Source-matched tests ran on a separate isolated deployment; its configuration and dependencies may differ from the preview.")
	}
}

func previewReportSourceMatches(candidate, tested string) bool {
	if candidate != tested || len(candidate) != 64 {
		return false
	}
	_, err := hex.DecodeString(candidate)
	return err == nil
}

func previewReportPathMatches(template, requestPath string) bool {
	u, err := url.ParseRequestURI(requestPath)
	if err != nil || u.IsAbs() {
		return false
	}
	want, got := strings.Split(template, "/"), strings.Split(u.Path, "/")
	if len(want) != len(got) {
		return false
	}
	for i, segment := range want {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") && len(segment) > 2 && got[i] != "" {
			continue
		}
		if segment != got[i] {
			return false
		}
	}
	return true
}

func attachPreviewReportTest(profiles []previewReportTest, profile string, passed bool) []previewReportTest {
	for i := range profiles {
		if profiles[i].Profile == profile {
			if passed {
				profiles[i].Passed++
			} else {
				profiles[i].Failed++
			}
			return profiles
		}
	}
	test := previewReportTest{Profile: profile}
	if passed {
		test.Passed = 1
	} else {
		test.Failed = 1
	}
	profiles = append(profiles, test)
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Profile < profiles[j].Profile })
	return profiles
}

func finishPreviewRouteReport(report *previewRouteReport) {
	report.Outcome = "no_findings"
	if report.Readiness.Status != "available" || report.Contract.Status != "available" || report.Policy.Status != "available" || report.Performance.Status == "unavailable" || report.Tests.Status != "available" {
		report.Outcome = "incomplete"
	}
	if report.Requirements != nil {
		switch report.Requirements.Status {
		case "violated":
			report.Outcome = "policy_violations"
		case "unknown":
			report.Outcome = "incomplete"
		}
	}
	for i := range report.Routes {
		row := &report.Routes[i]
		if row.Change == "unknown" {
			row.NextActions = append(row.NextActions, "Capture this route's deployment contract; current declaration or observation does not establish revision compatibility.")
			if report.Outcome == "no_findings" {
				report.Outcome = "incomplete"
			}
		}
		if row.RequestContractChanged && (row.RequestCompatibility == nil || !row.RequestCompatibility.Complete) && report.Outcome == "no_findings" {
			report.Outcome = "review_required"
		}
		if row.Change != "removed" && len(row.TestProfiles) == 0 {
			action := "Add an HTTP assertion for this route; no matching candidate test receipt was supplied."
			if len(row.SourceTestProfiles) > 0 {
				action = "Validate the source-matched assertions against candidate configuration; separate-environment results are supplemental."
			}
			row.NextActions = append(row.NextActions, action)
			if report.Outcome == "no_findings" {
				report.Outcome = "incomplete"
			}
		}
		if len(row.Breaks) > 0 {
			report.Outcome = "breaking_changes"
			row.NextActions = append(row.NextActions, "Review the removed route or response-schema compatibility break before release.")
		}
		for _, profile := range row.TestProfiles {
			if profile.Failed > 0 {
				if report.Outcome != "breaking_changes" {
					report.Outcome = "test_failures"
				}
				row.NextActions = append(row.NextActions, "Inspect the failing "+profile.Profile+" test in the original test report.")
			}
		}
		for _, profile := range row.SourceTestProfiles {
			if profile.Failed > 0 {
				row.NextActions = append(row.NextActions, "Inspect the failing source-matched "+profile.Profile+" test; its environment differs from the preview.")
			}
		}
	}
	finishPreviewRequestComparison(report)
	finishPreviewSecurityComparison(report)
}
