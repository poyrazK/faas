package main

import (
	"fmt"
	"io"
	"strings"
)

func renderPreviewReviewQueue(w io.Writer, report previewRouteReport, markdown bool) {
	if markdown {
		_, _ = fmt.Fprintln(w, "| Priority | Scope / route | Source change | Request compatibility | Security compatibility | Policy drift | Candidate checks | Baseline requests | Reasons |")
		_, _ = fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|")
	}
	for _, review := range report.ReviewPriorities {
		label := review.Scope
		if review.Method != "" {
			label += ": " + previewReportRouteKey(review.Method, review.Path)
		}
		requests := "unavailable"
		if review.BaselineTraffic != nil {
			requests = fmt.Sprintf("%d (observed window)", review.BaselineTraffic.Requests)
		}
		if markdown {
			_, _ = fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", review.Priority, previewSourceDisplay(label, true), review.SourceChange, review.RequestCompatibility, review.SecurityCompatibility, review.PolicyDrift, review.CandidateChecks, requests, strings.Join(review.Reasons, ", "))
		} else {
			_, _ = fmt.Fprintf(w, "%s: %s; source %s; requests %s; security %s; policy drift %s; checks %s; baseline requests %s\n  reasons: %s\n", review.Priority, previewSourceDisplay(label, false), review.SourceChange, review.RequestCompatibility, review.SecurityCompatibility, review.PolicyDrift, review.CandidateChecks, requests, strings.Join(review.Reasons, ", "))
		}
	}
	_, _ = fmt.Fprintln(w)
	for _, review := range report.ReviewPriorities {
		for _, action := range review.NextActions {
			label := review.Scope
			if review.Method != "" {
				label = previewReportRouteKey(review.Method, review.Path)
			}
			_, _ = fmt.Fprintf(w, "- %s: %s\n", previewSourceDisplay(label, markdown), action)
		}
	}
}
