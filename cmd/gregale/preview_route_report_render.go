package main

import (
	"fmt"
	"io"
	"strings"
	"unicode"
)

func renderPreviewRouteReport(w io.Writer, report previewRouteReport, markdown bool) {
	if markdown {
		_, _ = fmt.Fprintf(w, "## Route change report: %s\n\n", mdText(previewReportText(report.Preview)))
	} else {
		_, _ = fmt.Fprintf(w, "Route change report: %s\n", previewReportText(report.Preview))
	}
	_, _ = fmt.Fprintf(w, "Outcome: %s\n\nBaseline: %s / %s (%s)\nCandidate: %s / %s\n\n",
		report.Outcome, previewReportText(report.Parent), previewReportText(report.BaselineDeployment), report.BaselineSelection,
		previewReportText(report.Preview), previewReportText(report.CandidateDeployment))
	for _, evidence := range []struct {
		name string
		data previewReportEvidence
	}{{"Readiness", report.Readiness}, {"Contract", report.Contract}, {"Requests", report.Requests}, {"Security", report.Security}, {"Policy", report.Policy}, {"Performance", report.Performance}, {"Tests", report.Tests}} {
		_, _ = fmt.Fprintf(w, "%s: %s", evidence.name, evidence.data.Status)
		if evidence.data.Reason != "" {
			_, _ = fmt.Fprintf(w, " (%s)", previewReportText(evidence.data.Reason))
		}
		_, _ = fmt.Fprintln(w)
	}
	_, _ = fmt.Fprintf(w, "Policy drift: %s", previewReportText(report.PolicyDrift.Status))
	if report.PolicyDrift.Reason != "" {
		_, _ = fmt.Fprintf(w, " (%s)", previewReportText(report.PolicyDrift.Reason))
	}
	_, _ = fmt.Fprintf(w, "; changed routes: %d; unknown routes: %d\n", report.PolicyDrift.ChangedRoutes, report.PolicyDrift.UnknownRoutes)
	_, _ = fmt.Fprintln(w)
	if report.RouteRemovalGate != nil {
		if markdown {
			_, _ = fmt.Fprint(w, "### Route removal gate\n\n```text\n")
		}
		renderRouteRemovalGate(w, *report.RouteRemovalGate)
		if markdown {
			_, _ = fmt.Fprint(w, "```\n\n")
		} else {
			_, _ = fmt.Fprintln(w)
		}
	}
	if markdown {
		_, _ = fmt.Fprintln(w, "| Route | Contract | Current policy kinds | Policy drift | Matching test profiles | Observed p95 delta |")
		_, _ = fmt.Fprintln(w, "|---|---|---|---|---|---|")
	}
	for _, route := range report.Routes {
		renderPreviewReportRoute(w, route, markdown)
	}
	if len(report.Routes) == 0 {
		_, _ = fmt.Fprintln(w, "No captured route comparison is available.")
	}
	if markdown {
		_, _ = fmt.Fprint(w, "\n### Evidence and next actions\n\n")
	}
	for _, route := range report.Routes {
		for _, b := range route.Breaks {
			_, _ = fmt.Fprintf(w, "- %s: %s %s %s\n", previewReportText(previewReportRouteKey(route.Method, route.Path)),
				b.Kind, previewReportText(b.Status), previewReportText(b.PathInSchema))
		}
		for _, unknown := range route.Unknowns {
			_, _ = fmt.Fprintf(w, "- %s: unknown response schema %s %s %s\n",
				previewReportText(previewReportRouteKey(route.Method, route.Path)), unknown.Code,
				previewReportText(unknown.Status), previewReportText(unknown.PathInSchema))
		}
		for _, action := range route.NextActions {
			_, _ = fmt.Fprintf(w, "- %s: %s\n", previewReportText(previewReportRouteKey(route.Method, route.Path)), previewReportText(action))
		}
		renderPreviewRoutePolicyDrift(w, route, markdown)
	}
	_, _ = fmt.Fprintln(w)
	for _, note := range report.Notes {
		_, _ = fmt.Fprintf(w, "- %s\n", previewReportText(note))
	}
	renderPreviewRequestFindings(w, report, markdown)
	renderPreviewSecurityFindings(w, report, markdown)
	renderPreviewCustomers(w, report, markdown)
	if report.SourceImpact == nil && len(report.ReviewPriorities) > 0 {
		if markdown {
			_, _ = fmt.Fprint(w, "\n### Route review priorities\n\n")
		} else {
			_, _ = fmt.Fprint(w, "\nRoute review priorities\n")
		}
		renderPreviewReviewQueue(w, report, markdown)
	}
	renderPreviewSourceReview(w, report, markdown)
	renderPreviewRequirements(w, report.Requirements, markdown)
}

func renderPreviewReportRoute(w io.Writer, route previewReportRoute, markdown bool) {
	contract := route.Change
	if len(route.Breaks) > 0 {
		contract += fmt.Sprintf(" (%d breaks)", len(route.Breaks))
	}
	if len(route.Unknowns) > 0 {
		contract += fmt.Sprintf(" (%d unknown schema changes)", len(route.Unknowns))
	}
	if route.RequestCompatibility != nil {
		contract += "; requests: " + route.RequestCompatibility.Status
	} else if route.RequestContractChanged {
		contract += "; request review required"
	}
	if route.SecurityCompatibility != nil {
		contract += "; security: " + route.SecurityCompatibility.Status
	}
	policy := strings.Join(route.PolicyKinds, ", ")
	if policy == "" {
		policy = "not established"
	}
	profiles := []string{}
	for _, profile := range route.TestProfiles {
		profiles = append(profiles, fmt.Sprintf("%s: %d passed/%d failed", profile.Profile, profile.Passed, profile.Failed))
	}
	for _, profile := range route.SourceTestProfiles {
		profiles = append(profiles, fmt.Sprintf("%s (same source, separate environment): %d passed/%d failed", profile.Profile, profile.Passed, profile.Failed))
	}
	tests := strings.Join(profiles, ", ")
	if tests == "" {
		tests = "not established"
	}
	performance := "unavailable"
	if route.P95ChangeMS != nil {
		performance = fmt.Sprintf("%d -> %d ms (%+d ms; advisory; %d/%d requests)",
			route.BaselineTraffic.P95MS, route.CandidateTraffic.P95MS, *route.P95ChangeMS,
			route.BaselineTraffic.Requests, route.CandidateTraffic.Requests)
	}
	policyDrift := "unknown"
	if route.PolicyDrift != nil {
		policyDrift = route.PolicyDrift.Status
		if route.PolicyDrift.Status == "changed" {
			policyDrift = fmt.Sprintf("%d rule changes", len(route.PolicyDrift.Changes))
		}
	}
	fields := []string{previewReportRouteKey(route.Method, route.Path), contract, policy, policyDrift, tests, performance}
	for i, field := range fields {
		fields[i] = previewReportText(field)
		if markdown {
			fields[i] = mdCell(fields[i])
		}
	}
	if markdown {
		_, _ = fmt.Fprintf(w, "| %s |\n", strings.Join(fields, " | "))
	} else {
		_, _ = fmt.Fprintf(w, "%s: %s\n  policy: %s\n  policy drift: %s\n  tests: %s\n  performance: %s\n", fields[0], fields[1], fields[2], fields[3], fields[4], fields[5])
	}
}

func renderPreviewRoutePolicyDrift(w io.Writer, route previewReportRoute, markdown bool) {
	if route.PolicyDrift == nil || len(route.PolicyDrift.Changes) == 0 {
		return
	}
	label := previewReportRouteKey(route.Method, route.Path)
	for _, change := range route.PolicyDrift.Changes {
		before, after := previewPolicyRuleSourceLabel(change.Before), previewPolicyRuleSourceLabel(change.After)
		fields := strings.Join(change.ChangedFields, ", ")
		if fields == "" {
			fields = "rule configuration"
		}
		line := fmt.Sprintf("%s: route rule %s; before %s; after %s; changed %s", label, change.Change, before, after, fields)
		if markdown {
			_, _ = fmt.Fprintf(w, "- %s\n", mdCell(previewReportText(line)))
		} else {
			_, _ = fmt.Fprintf(w, "- %s\n", previewReportText(line))
		}
	}
}

func previewPolicyRuleSourceLabel(rule *previewPolicyRuleSource) string {
	if rule == nil {
		return "none"
	}
	return fmt.Sprintf("%s %s (priority %d, path %s, id %s)", rule.Kind,
		map[bool]string{true: "enabled", false: "disabled"}[rule.Enabled], rule.Priority, rule.MatchPath, rule.ID)
}

func previewReportText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}
