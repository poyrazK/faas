package main

import (
	"fmt"
	"io"
	"strings"
)

func renderPreviewReleaseRouteReview(w io.Writer, report previewReleaseRouteReview, markdown bool) {
	if markdown {
		_, _ = fmt.Fprintln(w, "# Release-wide route review")
		_, _ = fmt.Fprintf(w, "\nOutcome: **%s**\n\n", mdText(report.Outcome))
		_, _ = fmt.Fprintf(w, "Previews: %d available, %d unavailable; captured routes: %d; source routes without a captured match: %d; review items: %d.\n\n",
			report.Summary.AvailablePreviews, report.Summary.UnavailablePreviews, report.Summary.CapturedRoutes,
			report.Summary.SourceUnmatchedRoutes, report.Summary.ReviewItems)
	} else {
		_, _ = fmt.Fprintf(w, "Release-wide route review: %s\nPreviews: %d available, %d unavailable; captured routes: %d; source routes without a captured match: %d; review items: %d.\n\n",
			report.Outcome, report.Summary.AvailablePreviews, report.Summary.UnavailablePreviews, report.Summary.CapturedRoutes,
			report.Summary.SourceUnmatchedRoutes, report.Summary.ReviewItems)
	}
	if len(report.ReviewPriorities) > 0 {
		if markdown {
			_, _ = fmt.Fprintln(w, "## Release-wide priorities")
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, "| App | Route or scope | Priority | Observed exposure | Reasons |")
			_, _ = fmt.Fprintln(w, "|---|---|---|---|---|")
		} else {
			_, _ = fmt.Fprintln(w, "Release-wide priorities")
		}
		for _, item := range report.ReviewPriorities {
			label := item.Scope
			if item.Method != "" && item.Path != "" {
				label = previewReportRouteKey(item.Method, item.Path)
			}
			exposure := previewReleaseReviewExposureText(item)
			reasons := strings.Join(item.Reasons, ", ")
			fields := []string{item.Preview, label, item.Priority, exposure, reasons}
			for index := range fields {
				fields[index] = previewReportText(fields[index])
				if markdown {
					fields[index] = mdCell(fields[index])
				}
			}
			if markdown {
				_, _ = fmt.Fprintf(w, "| %s |\n", strings.Join(fields, " | "))
			} else {
				_, _ = fmt.Fprintf(w, "%s %s: %s (%s; %s)\n", fields[0], fields[1], fields[2], fields[3], fields[4])
			}
		}
		_, _ = fmt.Fprintln(w)
	}
	for _, preview := range report.Previews {
		if preview.Report == nil {
			if markdown {
				_, _ = fmt.Fprintf(w, "## Preview %s\n\nStatus: %s (%s)\n\n", mdText(previewReportText(preview.Slug)), mdText(preview.Status), mdText(preview.Reason))
			} else {
				_, _ = fmt.Fprintf(w, "Preview %s: %s (%s)\n\n", previewReportText(preview.Slug), previewReportText(preview.Status), previewReportText(preview.Reason))
			}
			continue
		}
		renderPreviewRouteReport(w, *preview.Report, markdown)
	}
}

func previewReleaseReviewExposureText(item previewReleaseReviewPriority) string {
	reach, requests := previewReleaseReviewExposure(item)
	if item.CustomerImpact != nil && item.CustomerImpact.Usage != nil {
		usage := item.CustomerImpact.Usage
		return fmt.Sprintf("%d tenants, %d consumers, %d requests", usage.PlatformTenantCount, usage.ConsumerCount, usage.Requests)
	}
	if item.BaselineTraffic != nil {
		return fmt.Sprintf("%d requests; customer reach unavailable", requests)
	}
	if reach > 0 || requests > 0 {
		return fmt.Sprintf("reach %d, %d requests", reach, requests)
	}
	return "unavailable"
}
