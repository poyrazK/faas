package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) assessProfileRegression(ctx context.Context, acct state.Account, app state.App, out api.ProfileRegressionAssessment, baseline, candidate api.ProfileResponse) api.ProfileRegressionAssessment {
	out.Attribution = profiling.CompareAttribution(baseline, candidate)
	out.Options = api.NormalizeProfileRegressionOptions(out.Options)
	out.BaselineCoverage, out.CandidateCoverage = baseline.Coverage, candidate.Coverage
	fallback := func(reason string) api.ProfileRegressionAssessment {
		if out.Options.Metric != "cpu_per_request" {
			return profiling.AssessRegression(out, baseline, candidate)
		}
		out.Reason = reason
		return out
	}
	if !api.MustLimitsFor(acct.Plan).DebugTelemetryEnabled {
		return fallback("Request telemetry is unavailable on this plan; use CPU per second or change plans.")
	}
	reader, ok := s.store.(state.ProfileRequestCountReader)
	if !ok {
		return fallback("Observed request telemetry is unavailable on this installation.")
	}
	// Optional traffic context must not delay a CPU/s assessment indefinitely.
	if out.Options.Metric != "cpu_per_request" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	baselineRequests, baselineAvailable, err := reader.ProfileRequestCount(ctx, acct.ID, app.ID, out.Baseline)
	if err != nil {
		return fallback("Baseline request telemetry could not be read. Retry when request history is available.")
	}
	candidateRequests, candidateAvailable, err := reader.ProfileRequestCount(ctx, acct.ID, app.ID, out.Candidate)
	if err != nil {
		return fallback("Candidate request telemetry could not be read. Retry when request history is available.")
	}
	var baselineCount, candidateCount *int64
	if baselineAvailable {
		baselineCount = &baselineRequests
	}
	if candidateAvailable {
		candidateCount = &candidateRequests
	}
	return profiling.AssessRegressionWithRequests(out, baseline, candidate, baselineCount, candidateCount)
}
