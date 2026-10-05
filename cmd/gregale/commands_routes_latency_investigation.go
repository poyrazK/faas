package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func renderRouteLatencyDiagnostics(w api.RouteHealthWindowEvidence, d *api.RouteHealthLatencyDiagnostics, slug string) {
	candidate, stable := "missing", "missing"
	if w.Candidate.P95LatencyMS != nil {
		candidate = fmt.Sprintf("%.1fms", *w.Candidate.P95LatencyMS)
	}
	if w.Stable.P95LatencyMS != nil {
		stable = fmt.Sprintf("%.1fms", *w.Stable.P95LatencyMS)
	}
	_, _ = fmt.Fprintf(osStdout, "  Full observed route p95: candidate %s; stable %s — %s (%s)\n", candidate, stable, w.LatencyStatus, previewReportText(w.LatencyReason))
	if w.LatencyDeltaMS != nil {
		_, _ = fmt.Fprintf(osStdout, "  Route p95 change: %+.1fms\n", *w.LatencyDeltaMS)
	}
	for i, s := range []api.RouteHealthLatencySample{d.Candidate, d.Stable} {
		name := "candidate"
		if i == 1 {
			name = "stable"
		}
		_, _ = fmt.Fprintf(osStdout, "  %s recent sample: %d rows representing %d requests; capped: %t; spans: %d in %d rows; missing spans: %d; spans capped: %t; timing incomplete: %t\n", name, s.SampledRows, s.SampledRequests, s.SamplesTruncated, s.SpanSamples, s.SpanRows, s.MissingSpanRows, s.SpansTruncated, s.TimingIncomplete)
		_, _ = fmt.Fprintf(osStdout, "    Guest p95: %s (%d measured rows, %d represented requests); cold boot: %d represented requests; wake boot p95: %s (%d distinct wakes)\n", routeLatencyMS(s.GuestP95MS), s.GuestRows, s.GuestRequests, s.ColdBootRequests, routeLatencyMS(s.WakeBootP95MS), s.WakeSamples)
	}
	_, _ = fmt.Fprintf(osStdout, "  Retained dependency comparisons (groups capped: %t):\n", d.DependenciesTruncated)
	for _, dep := range d.Dependencies {
		_, _ = fmt.Fprintf(osStdout, "    %s/%s: %s; p95 candidate %s / stable %s; change %s; exclusive change %s\n", dep.Type, dep.Kind, dep.Status, routeLatencyMS(dep.Candidate.P95MS), routeLatencyMS(dep.Stable.P95MS), routeLatencyDeltaMS(dep.P95DeltaMS), routeLatencyDeltaMS(dep.ExclusiveP95DeltaMS))
		for i, side := range []api.RouteHealthDependencyTiming{dep.Candidate, dep.Stable} {
			name := "candidate"
			if i == 1 {
				name = "stable"
			}
			_, _ = fmt.Fprintf(osStdout, "      %s: %d retained spans representing %d calls (%d errors); exclusive p95 %s\n", name, side.SpanSamples, side.RepresentedCalls, side.ErrorCalls, routeLatencyMS(side.ExclusiveP95MS))
			for _, e := range side.Examples {
				_, _ = fmt.Fprintf(osStdout, "        gregale debug requests inspect %s %s\n", slug, e.TelemetryID)
			}
		}
	}
	_, _ = fmt.Fprintln(osStdout, "  Retained dependency/stage percentiles are sample evidence, cannot be added, and do not establish a cause.")
}

func routeLatencyMS(ms *int64) string {
	if ms == nil {
		return "missing"
	}
	return fmt.Sprintf("%dms", *ms)
}

func routeLatencyDeltaMS(ms *int64) string {
	if ms == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%+dms", *ms)
}
