package operatorobs

import (
	"math"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ServingProbeFailureCodes is the stable numeric metric code table. Unknown
// is an observed, unclassified failure; it is distinct from missing evidence.
func ServingProbeFailureCodes() []string {
	return []string{"none", "dns", "connection", "tls", "timeout", "http_status", "latency", "body_mismatch", "unknown"}
}

func ServingProbeDiagnosticReasons() []string {
	return []string{"diagnostics_missing", "query_failed", "coverage_unavailable", "diagnostics_not_current", "diagnostics_inconsistent"}
}

func unknownProbeDiagnostics(reason string) *api.ObsServingProbeDiagnostics {
	return &api.ObsServingProbeDiagnostics{State: "unknown", Reason: reason}
}

func assessServingProbeDiagnostics(probe api.ObsServingProbe, samples servingProbeSamples, queryErr error, now time.Time) *api.ObsServingProbeDiagnostics {
	if !servingDiagnosticsCoverageCurrent(probe, now) {
		return unknownProbeDiagnostics("coverage_unavailable")
	}
	if queryErr != nil {
		return unknownProbeDiagnostics("query_failed")
	}
	values := make([]int, 4)
	metrics := []string{probeFailure, probeFirstFailure, probeAttempts, probeHTTPStatus}
	for i, metric := range metrics {
		key := servingProbeKey{metric: metric, node: probe.Node, url: probe.URLID}
		value, found := samples.values[key]
		if !found {
			return unknownProbeDiagnostics("diagnostics_missing")
		}
		at, valid := inputSampleTime(samples.times[key], now)
		if !valid || !at.Equal(*probe.EvaluatedAt) {
			return unknownProbeDiagnostics("diagnostics_not_current")
		}
		if value < 0 || value > 599 || value != math.Trunc(value) {
			return unknownProbeDiagnostics("diagnostics_inconsistent")
		}
		values[i] = int(value)
	}
	codes := ServingProbeFailureCodes()
	if values[0] >= len(codes) || values[1] >= len(codes) {
		return unknownProbeDiagnostics("diagnostics_inconsistent")
	}
	recovered := values[2] == 2 && values[0] == 0
	out := &api.ObsServingProbeDiagnostics{State: "available", EvaluatedAt: probe.EvaluatedAt, FailureCode: codes[values[0]], FirstFailureCode: codes[values[1]], Attempts: &values[2], HTTPStatus: &values[3], RecoveredOnRetry: &recovered}
	probe.Diagnostics = out
	if !ServingProbeDiagnosticsValid(probe, now) {
		return unknownProbeDiagnostics("diagnostics_inconsistent")
	}
	return out
}

func servingDiagnosticsCoverageCurrent(probe api.ObsServingProbe, now time.Time) bool {
	return slices.Contains([]string{"healthy", "failing", "content_disabled", "budget_mismatch"}, probe.State) && probe.Reporting != nil && *probe.Reporting && probe.EvaluatedAt != nil && !probe.EvaluatedAt.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) && probe.LastRunAt != nil && float64(now.UnixMilli()-probe.LastRunAt.UnixMilli())/1000 <= probe.MaxResultAgeSeconds
}

// ServingProbeDiagnosticsValid also validates the operator CLI's optional
// section before either JSON or human rendering can expose source metadata.
func ServingProbeDiagnosticsValid(probe api.ObsServingProbe, now time.Time) bool {
	d := probe.Diagnostics
	if d == nil {
		return true // Older servers omit this optional section.
	}
	if d.State == "unknown" {
		return slices.Contains(ServingProbeDiagnosticReasons(), d.Reason) && d.EvaluatedAt == nil && d.FailureCode == "" && d.FirstFailureCode == "" && d.Attempts == nil && d.HTTPStatus == nil && d.RecoveredOnRetry == nil
	}
	if d.State != "available" || d.Reason != "" || d.EvaluatedAt == nil || !servingDiagnosticsCoverageCurrent(probe, now) || !d.EvaluatedAt.Equal(*probe.EvaluatedAt) || d.Attempts == nil || d.HTTPStatus == nil || d.RecoveredOnRetry == nil {
		return false
	}
	codes := ServingProbeFailureCodes()
	if !slices.Contains(codes, d.FailureCode) || !slices.Contains(codes, d.FirstFailureCode) || *d.Attempts < 1 || *d.Attempts > 2 || *d.HTTPStatus < 0 || *d.HTTPStatus > 599 || (*d.HTTPStatus > 0 && *d.HTTPStatus < 100) {
		return false
	}
	if *d.Attempts == 1 && (d.FirstFailureCode != d.FailureCode || d.FailureCode != "none") || *d.Attempts == 2 && d.FirstFailureCode == "none" || *d.RecoveredOnRetry != (*d.Attempts == 2 && d.FailureCode == "none") {
		return false
	}
	success := *d.HTTPStatus >= 200 && *d.HTTPStatus < 300
	if probe.State == "healthy" && d.FailureCode != "none" || probe.State == "failing" && d.FailureCode == "none" {
		return false
	}
	switch d.FailureCode {
	case "none", "latency", "body_mismatch":
		return success
	case "http_status":
		return *d.HTTPStatus != 0 && !success
	case "dns":
		return *d.HTTPStatus == 0
	default:
		return *d.HTTPStatus == 0 || success
	}
}
