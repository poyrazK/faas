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
	_, _ = fmt.Fprintln(w)
	if markdown {
		_, _ = fmt.Fprintln(w, "| Route | Contract | Current policy kinds | Matching test profiles | Observed p95 delta |")
		_, _ = fmt.Fprintln(w, "|---|---|---|---|---|")
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
		for _, action := range route.NextActions {
			_, _ = fmt.Fprintf(w, "- %s: %s\n", previewReportText(previewReportRouteKey(route.Method, route.Path)), previewReportText(action))
		}
	}
	_, _ = fmt.Fprintln(w)
	for _, note := range report.Notes {
		_, _ = fmt.Fprintf(w, "- %s\n", previewReportText(note))
	}
	renderPreviewRequestFindings(w, report, markdown)
	renderPreviewSecurityFindings(w, report, markdown)
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
	fields := []string{previewReportRouteKey(route.Method, route.Path), contract, policy, tests, performance}
	for i, field := range fields {
		fields[i] = previewReportText(field)
		if markdown {
			fields[i] = mdCell(fields[i])
		}
	}
	if markdown {
		_, _ = fmt.Fprintf(w, "| %s |\n", strings.Join(fields, " | "))
	} else {
		_, _ = fmt.Fprintf(w, "%s: %s\n  policy: %s\n  tests: %s\n  performance: %s\n", fields[0], fields[1], fields[2], fields[3], fields[4])
	}
}

func previewReportText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}
